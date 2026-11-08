package handlers

import (
	"net"
	"net/netip"
	"net/url"
	"strings"

	"github.com/prawo-i-piesc/backend/internal/netguard"
)

// Powody odrzucenia target_url, zwracane w "fields" przy VALIDATION_FAILED.
const (
	targetInvalid   = "invalid"
	targetNotPublic = "not_public"
)

// scanTarget to zweryfikowany adres celu w dwóch postaciach.
type scanTarget struct {
	// URL to pełny adres ze schematem, zapisywany w bazie i pokazywany w
	// widokach oraz raportach.
	URL string
	// Engine to host[:port][/ścieżka][?zapytanie] bez schematu, w postaci, w
	// jakiej oczekuje go silnik: TargetFormatter przerywa skan, gdy cel
	// zaczyna się od "http", i sam dopisuje http:// albo https:// zależnie od
	// wybranych testów.
	Engine string
}

// normalizeTarget sprawdza, czy adres nadaje się do skanowania, i zwraca go
// w postaci dla bazy i dla silnika. Drugi wynik to powód odrzucenia albo
// pusty napis.
//
// Bez allowPrivate to bramka na oczywiste nadużycia, nie pełna ochrona przed
// SSRF: domena publiczna może się rozwiązywać na adres prywatny, a sprawdzenie
// DNS tutaj i tak by nic nie dało, bo silnik rozwiązuje nazwę ponownie i sam
// pilnuje adresu przy połączeniu. Właściwą granicą jest egress NetworkPolicy
// na podach silnika.
//
// allowPrivate (ANTIGINX_ALLOW_PRIVATE_TARGETS) przepuszcza adresy prywatne i
// nazwy wewnętrzne, ale składnię sprawdza tak samo.
func normalizeTarget(raw string, allowPrivate bool) (scanTarget, string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return scanTarget{}, targetInvalid
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return scanTarget{}, targetInvalid
	}
	if u.User != nil || u.Opaque != "" {
		return scanTarget{}, targetInvalid
	}

	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return scanTarget{}, targetInvalid
	}

	addr, err := netip.ParseAddr(host)
	isAddr := err == nil
	// Strefa IPv6 (fe80::1%eth0) ma sens tylko na maszynie, która ją nadała, a
	// silnik nie sparsuje jej po zdjęciu schematu.
	if isAddr && addr.Zone() != "" {
		return scanTarget{}, targetInvalid
	}
	if !allowPrivate {
		if isAddr && !netguard.PublicAddr(addr) {
			return scanTarget{}, targetNotPublic
		}
		if !isAddr && !netguard.PublicHostname(host) {
			return scanTarget{}, targetNotPublic
		}
	}

	return scanTarget{URL: u.String(), Engine: engineTarget(u, host, isAddr && addr.Is6())}, ""
}

// engineTarget składa cel dla silnika z już sprawdzonego adresu. Host jest w
// postaci kanonicznej (małe litery, bez kropki na końcu), żeby silnik i jego
// ochrona SSRF widziały tę samą nazwę co walidacja tutaj. Fragment znika, bo
// i tak nigdy nie trafia do serwera.
func engineTarget(u *url.URL, host string, ipv6 bool) string {
	if port := u.Port(); port != "" {
		host = net.JoinHostPort(host, port)
	} else if ipv6 {
		host = "[" + host + "]"
	}

	var b strings.Builder
	b.WriteString(host)
	b.WriteString(u.EscapedPath())
	if u.RawQuery != "" {
		b.WriteByte('?')
		b.WriteString(u.RawQuery)
	}
	return b.String()
}
