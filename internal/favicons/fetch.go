package favicons

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/prawo-i-piesc/backend/internal/netguard"
)

const (
	// pageLimit to tyle strony, ile czytamy w poszukiwaniu <link rel="icon">.
	// <head> mieści się w tym prawie zawsze, a reszty dokumentu nie potrzeba.
	pageLimit = 256 << 10
	// downloadLimit to największy plik ikony, jaki pobieramy. Większy jest od
	// razu odrzucany. ICO z wieloma rozmiarami mieści się z zapasem, a SVG z
	// osadzonym obrazem z programu graficznego potrafi mieć ponad megabajt.
	downloadLimit = 2 << 20
	// maxAttempts to tyle kandydatów, ile najwyżej próbujemy na jedną witrynę.
	maxAttempts  = 4
	maxRedirects = 3
	userAgent    = "Mozilla/5.0 (compatible; AntiGinx favicon fetcher)"
)

var (
	// errNoIcon znaczy, że strona odpowiedziała, ale żadnej ikony nie dało się
	// pokazać. W odróżnieniu od błędu sieci to pewna odpowiedź.
	errNoIcon         = errors.New("favicons: strona nie ma ikony, którą da się pokazać")
	errPrivateAddress = errors.New("favicons: adres poza publicznym internetem")
	errTooLarge       = errors.New("favicons: plik za duży")
)

type statusError int

func (e statusError) Error() string { return fmt.Sprintf("favicons: odpowiedź HTTP %d", int(e)) }

// fetcher pobiera ikony z witryn podanych przez użytkowników, więc każde
// połączenie, także po przekierowaniu, sprawdza adres, z którym naprawdę się
// łączy. Sprawdzenie nazwy przed zapytaniem nie wystarcza: domena publiczna
// może się rozwiązywać na adres w klastrze albo zmienić adres między
// sprawdzeniem a połączeniem.
type fetcher struct {
	client       *http.Client
	allowPrivate bool
}

func newFetcher(allowPrivate bool) *fetcher {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	if !allowPrivate {
		dialer.Control = publicOnly
	}
	transport := &http.Transport{
		// Proxy ze zmiennych środowiska łączyłoby się zamiast celu, a jego adres
		// przeszedłby sprawdzenie za cel.
		Proxy:                  nil,
		DialContext:            dialer.DialContext,
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           16,
		MaxIdleConnsPerHost:    2,
		IdleConnTimeout:        30 * time.Second,
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  5 * time.Second,
		MaxResponseHeaderBytes: 32 << 10,
	}
	return &fetcher{
		allowPrivate: allowPrivate,
		client: &http.Client{
			Transport: transport,
			Timeout:   8 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= maxRedirects {
					return http.ErrUseLastResponse
				}
				if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
					return errPrivateAddress
				}
				return nil
			},
		},
	}
}

// publicOnly odrzuca połączenie z adresem spoza publicznego internetu. Dialer
// woła ją z adresem już po rozwiązaniu nazwy, tuż przed połączeniem.
func publicOnly(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !netguard.PublicAddr(addr) {
		return errPrivateAddress
	}
	return nil
}

// fetch znajduje i pobiera ikonę witryny. Zwraca errNoIcon, gdy strona
// odpowiadała, ale nic się nie nadało, a inny błąd, gdy nie dało się z nią
// połączyć, czyli warto spróbować później.
func (f *fetcher) fetch(ctx context.Context, site Site) (Icon, error) {
	origin, err := url.Parse(site.Origin)
	if err != nil {
		return Icon{}, err
	}
	if !f.allowPrivate && !publicHost(origin.Hostname()) {
		return Icon{}, errNoIcon
	}

	answered := false
	base := origin.JoinPath("/")
	page, final, err := f.get(ctx, base.String(), "text/html,application/xhtml+xml", pageLimit, true)
	if err == nil {
		answered = true
		base = final
	} else if isAnswer(err) {
		answered = true
	}

	candidates := discover(page, base)
	tried := 0
	for _, c := range candidates {
		if tried == maxAttempts {
			break
		}
		tried++

		raw := c.data
		if c.URL != "" {
			raw, _, err = f.get(ctx, c.URL, "image/*", downloadLimit, false)
			if err != nil {
				if isAnswer(err) {
					answered = true
				}
				continue
			}
		}
		answered = true
		if icon, err := normalize(raw); err == nil {
			return icon, nil
		}
	}

	if !answered {
		if err == nil {
			err = errors.New("favicons: witryna nie odpowiedziała")
		}
		return Icon{}, err
	}
	return Icon{}, errNoIcon
}

// get pobiera adres z limitem rozmiaru. Z truncate dłuższa treść jest ucinana
// (strona), bez niego odrzucana (plik ikony).
func (f *fetcher) get(ctx context.Context, rawURL, accept string, limit int64, truncate bool) ([]byte, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", accept)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, nil, statusError(resp.StatusCode)
	}
	if !truncate && resp.ContentLength > limit {
		return nil, nil, errTooLarge
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil && len(body) == 0 {
		return nil, nil, err
	}
	if int64(len(body)) > limit {
		if !truncate {
			return nil, nil, errTooLarge
		}
		body = body[:limit]
	}
	return body, resp.Request.URL, nil
}

// isAnswer mówi, czy błąd przyszedł od serwera (strona odpowiedziała czymś,
// czego nie chcemy), a nie z sieci.
func isAnswer(err error) bool {
	var status statusError
	return errors.As(err, &status) || errors.Is(err, errTooLarge)
}

func publicHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if addr, err := netip.ParseAddr(host); err == nil {
		return netguard.PublicAddr(addr)
	}
	return netguard.PublicHostname(host)
}
