// Package favicons pobiera ikony skanowanych witryn, trzyma je w bazie pod
// hashem zawartości i podaje ich hashe widokom, które pokazują adresy skanów.
//
// Ikonę pobiera backend, w tle i raz na witrynę: wątek obsługujący żądanie
// tylko zleca pobranie i nigdy na nie nie czeka. Ta sama ikona wielu witryn
// zajmuje jeden wiersz, a adres /api/favicons/<hash> nigdy nie zmienia treści,
// więc przeglądarka może go trzymać w pamięci podręcznej bez końca.
package favicons

import (
	"net"
	"net/url"
	"strings"
)

// Site to witryna, której ikonę trzymamy.
type Site struct {
	// Key to host w postaci kanonicznej, z portem, gdy nie jest domyślny dla
	// schematu: example.com, example.com:8443, [2001:db8::1]:8443.
	Key string
	// Origin to schemat i host, pod które idą zapytania o ikonę.
	Origin string
}

// SiteOf wyznacza witrynę z adresu celu skanu.
func SiteOf(targetURL string) (Site, bool) {
	u, err := url.Parse(targetURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return Site{}, false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return Site{}, false
	}

	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}

	key := host
	switch {
	case port != "":
		key = net.JoinHostPort(host, port)
	case strings.Contains(host, ":"):
		key = "[" + host + "]"
	}
	return Site{Key: key, Origin: u.Scheme + "://" + key}, true
}

// Icons to hashe ikon znanych witryn, po kluczu witryny.
type Icons map[string]string

// For zwraca hash ikony witryny, do której należy adres, albo nil.
func (i Icons) For(targetURL string) *string {
	if len(i) == 0 {
		return nil
	}
	site, ok := SiteOf(targetURL)
	if !ok {
		return nil
	}
	hash, ok := i[site.Key]
	if !ok {
		return nil
	}
	return &hash
}

// ValidHash mówi, czy napis wygląda jak hash ikony: 64 małe znaki szesnastkowe.
func ValidHash(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for i := 0; i < len(hash); i++ {
		c := hash[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
