package favicons

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/prawo-i-piesc/backend/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// refreshAfter: tyle trzymamy ikonę, zanim sprawdzimy, czy strona jej nie zmieniła.
	refreshAfter = 30 * 24 * time.Hour
	// retryAfter: tyle czekamy z kolejną próbą, gdy ikony nie ma albo strona nie odpowiadała.
	retryAfter = 24 * time.Hour
	// queueSize to tyle witryn, ile może czekać na pobranie. Pełna kolejka
	// gubi nowe zlecenia, a witryna i tak wróci przy następnym odczycie listy.
	queueSize = 256
	workers   = 2
	// siteTimeout obejmuje stronę i wszystkich kandydatów jednej witryny.
	siteTimeout = 20 * time.Second
	// lookupChunk to tyle witryn w jednym zapytaniu IN.
	lookupChunk = 500
)

// Service pobiera ikony w tle i podaje ich hashe. Metody wołane na nil nic nie
// robią, więc handler bez ikon (np. w testach) działa jak dotąd.
type Service struct {
	db    *gorm.DB
	fetch func(context.Context, Site) (Icon, error)
	now   func() time.Time
	jobs  chan Site

	mu     sync.Mutex
	queued map[string]bool
}

// New tworzy usługę. allowPrivate (ANTIGINX_ALLOW_PRIVATE_TARGETS) pozwala
// pobierać ikony z adresów prywatnych, tak samo jak skanować je, tylko na dev.
func New(db *gorm.DB, allowPrivate bool) *Service {
	return newService(db, newFetcher(allowPrivate).fetch)
}

func newService(db *gorm.DB, fetch func(context.Context, Site) (Icon, error)) *Service {
	return &Service{
		db:     db,
		fetch:  fetch,
		now:    func() time.Time { return time.Now().UTC() },
		jobs:   make(chan Site, queueSize),
		queued: make(map[string]bool),
	}
}

// Want zleca pobranie ikony witryny, do której należy adres. Nie czeka i nie
// czyta bazy; czy ikona jest potrzebna, rozstrzyga dopiero obsługa kolejki.
func (s *Service) Want(targetURL string) {
	if s == nil {
		return
	}
	if site, ok := SiteOf(targetURL); ok {
		s.enqueue(site)
	}
}

func (s *Service) enqueue(site Site) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queued[site.Key] {
		return
	}
	select {
	case s.jobs <- site:
		s.queued[site.Key] = true
	default:
	}
}

// Lookup zwraca hashe ikon witryn, do których należą adresy, jednym
// zapytaniem na lookupChunk witryn. Witryny bez wiersza albo z przeterminowaną
// próbą trafiają do kolejki, więc skany sprzed tej funkcji dostają ikony przy
// pierwszym odczycie listy. Błąd bazy kończy się pustym wynikiem: ikona to
// dodatek i nie może zepsuć odpowiedzi.
func (s *Service) Lookup(ctx context.Context, targetURLs ...string) Icons {
	if s == nil || len(targetURLs) == 0 {
		return nil
	}

	sites := make(map[string]Site)
	for _, target := range targetURLs {
		if site, ok := SiteOf(target); ok {
			sites[site.Key] = site
		}
	}
	keys := make([]string, 0, len(sites))
	for key := range sites {
		keys = append(keys, key)
	}

	icons := make(Icons)
	now := s.now()
	for start := 0; start < len(keys); start += lookupChunk {
		chunk := keys[start:min(start+lookupChunk, len(keys))]
		var rows []models.SiteIcon
		if err := s.db.WithContext(ctx).Where("host IN ?", chunk).Find(&rows).Error; err != nil {
			log.Printf("Favicons: nie udało się odczytać ikon: %v", err)
			return nil
		}
		for _, row := range rows {
			if row.FaviconHash != nil {
				icons[row.Host] = *row.FaviconHash
			}
			if due(row.FaviconHash != nil, row.CheckedAt, now) {
				s.enqueue(sites[row.Host])
			}
			delete(sites, row.Host)
		}
	}
	// Zostały witryny, o które nikt jeszcze nie pytał.
	for _, site := range sites {
		s.enqueue(site)
	}
	return icons
}

func due(hasIcon bool, checkedAt, now time.Time) bool {
	if hasIcon {
		return now.Sub(checkedAt) >= refreshAfter
	}
	return now.Sub(checkedAt) >= retryAfter
}

// Run obsługuje kolejkę, dopóki ctx żyje.
func (s *Service) Run(ctx context.Context) {
	if s == nil {
		return
	}
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case site := <-s.jobs:
					s.work(ctx, site)
				}
			}
		})
	}
	wg.Wait()
}

func (s *Service) work(ctx context.Context, site Site) {
	defer func() {
		s.mu.Lock()
		delete(s.queued, site.Key)
		s.mu.Unlock()
	}()

	claimed, err := s.claim(ctx, site.Key)
	if err != nil {
		log.Printf("Favicons: nie udało się zająć witryny %s: %v", site.Key, err)
		return
	}
	if !claimed {
		return
	}

	fetchCtx, cancel := context.WithTimeout(ctx, siteTimeout)
	icon, err := s.fetch(fetchCtx, site)
	cancel()

	switch {
	case err == nil:
		err = s.save(ctx, site.Key, &icon)
	case errors.Is(err, errNoIcon):
		err = s.save(ctx, site.Key, nil)
	default:
		// Strona nie odpowiedziała: zostaje to, co było, a kolejna próba
		// przyjdzie po retryAfter albo refreshAfter od zajęcia.
		return
	}
	if err != nil && ctx.Err() == nil {
		log.Printf("Favicons: nie udało się zapisać ikony %s: %v", site.Key, err)
	}
}

// claim zapisuje początek próby i mówi, czy to ta instancja ma pobrać ikonę.
// Jedno polecenie rozstrzyga to atomowo także między replikami backendu: wiersz
// dostaje ten, kto go wstawi, albo ten, kto przesunie przeterminowaną próbę.
func (s *Service) claim(ctx context.Context, key string) (bool, error) {
	now := s.now()
	var hosts []string
	err := s.db.WithContext(ctx).Raw(
		`INSERT INTO site_icons (host, checked_at) VALUES (?, ?)
		 ON CONFLICT (host) DO UPDATE SET checked_at = excluded.checked_at
		 WHERE (site_icons.favicon_hash IS NULL AND site_icons.checked_at <= ?)
		    OR (site_icons.favicon_hash IS NOT NULL AND site_icons.checked_at <= ?)
		 RETURNING host`,
		key, now, now.Add(-retryAfter), now.Add(-refreshAfter),
	).Scan(&hosts).Error
	return len(hosts) == 1, err
}

// save przypisuje witrynie ikonę albo, z nil, zapisuje, że ikony nie ma.
// Ikona, której po zmianie nie wskazuje już żadna witryna, znika z bazy.
func (s *Service) save(ctx context.Context, key string, icon *Icon) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var site models.SiteIcon
		if err := tx.Select("favicon_hash").Where("host = ?", key).Take(&site).Error; err != nil {
			return err
		}

		var hash *string
		if icon != nil {
			sum := sha256.Sum256(icon.Data)
			h := hex.EncodeToString(sum[:])
			hash = &h
			row := models.Favicon{Hash: h, ContentType: icon.ContentType, Data: icon.Data, CreatedAt: s.now()}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
				return err
			}
		}

		if err := tx.Model(&models.SiteIcon{}).Where("host = ?", key).Update("favicon_hash", hash).Error; err != nil {
			return err
		}

		old := site.FaviconHash
		if old == nil || (hash != nil && *old == *hash) {
			return nil
		}
		return tx.Exec(
			`DELETE FROM favicons WHERE hash = ? AND NOT EXISTS (SELECT 1 FROM site_icons WHERE favicon_hash = ?)`,
			*old, *old,
		).Error
	})
}
