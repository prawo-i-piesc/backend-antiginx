package handlers

import (
	"net/netip"
	"net/url"
	"strings"
)

// Powody odrzucenia target_url, zwracane w "fields" przy VALIDATION_FAILED.
const (
	targetInvalid   = "invalid"
	targetNotPublic = "not_public"
)

// Sufiksy nazw, które rozwiązują się wyłącznie w sieci lokalnej albo wewnątrz
// klastra. "local" obejmuje też cluster.local z Kubernetesa.
var internalSuffixes = []string{"localhost", "local", "internal", "lan", "home.arpa", "svc"}

// sharedAddressSpace to 100.64.0.0/10 (RFC 6598, CGNAT). netip nie liczy go do
// IsPrivate, a w wielu sieciach operatorskich i VPN-ach jest wewnętrzny.
var sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")

// thisNetwork to 0.0.0.0/8, „ta sieć” z RFC 1122 — nic w internecie tam nie
// mieszka, a IsGlobalUnicast odrzuca z tego zakresu tylko samo 0.0.0.0.
var thisNetwork = netip.MustParsePrefix("0.0.0.0/8")

// normalizeTarget sprawdza, czy adres nadaje się do skanowania z zewnątrz, i
// zwraca go w postaci, w jakiej trafi do silnika. Drugi wynik to powód
// odrzucenia albo pusty napis.
//
// To bramka na oczywiste nadużycia, nie pełna ochrona przed SSRF: domena
// publiczna może się rozwiązywać na adres prywatny, a sprawdzenie DNS tutaj
// i tak by nic nie dało, bo silnik rozwiązuje nazwę ponownie. Właściwą
// granicą jest egress NetworkPolicy na podach silnika.
func normalizeTarget(raw string) (string, string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", targetInvalid
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", targetInvalid
	}
	if u.User != nil || u.Opaque != "" {
		return "", targetInvalid
	}

	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return "", targetInvalid
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		if !publicAddr(addr) {
			return "", targetNotPublic
		}
		return u.String(), ""
	}

	if !publicHostname(host) {
		return "", targetNotPublic
	}
	return u.String(), ""
}

func publicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsGlobalUnicast() &&
		!addr.IsPrivate() &&
		!sharedAddressSpace.Contains(addr) &&
		!thisNetwork.Contains(addr)
}

// publicHostname odrzuca nazwy jednoczłonowe (rozwiązywane przez search
// domains, np. "rabbitmq" w klastrze), wewnętrzne sufiksy oraz nazwy, których
// ostatni człon nie zaczyna się literą. Żadna domena najwyższego poziomu nie
// zaczyna się cyfrą, a takie zapisy ("127.1", "0x7f.1") niektóre biblioteki
// interpretują jako adresy IP.
func publicHostname(host string) bool {
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}

	tld := labels[len(labels)-1]
	if tld == "" || tld[0] < 'a' || tld[0] > 'z' {
		return false
	}

	for _, suffix := range internalSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return false
		}
	}
	return true
}
