// Package netguard rozstrzyga, czy adres albo nazwa hosta prowadzi do
// publicznego internetu. Korzysta z niego walidacja celu skanu i każde
// połączenie, które backend sam nawiązuje z adresem podanym przez użytkownika.
package netguard

import (
	"net/netip"
	"strings"
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

// PublicAddr mówi, czy adres leży w publicznym internecie.
func PublicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsGlobalUnicast() &&
		!addr.IsPrivate() &&
		!sharedAddressSpace.Contains(addr) &&
		!thisNetwork.Contains(addr)
}

// PublicHostname odrzuca nazwy jednoczłonowe (rozwiązywane przez search
// domains, np. "rabbitmq" w klastrze), wewnętrzne sufiksy oraz nazwy, których
// ostatni człon nie zaczyna się literą. Żadna domena najwyższego poziomu nie
// zaczyna się cyfrą, a takie zapisy ("127.1", "0x7f.1") niektóre biblioteki
// interpretują jako adresy IP. Nazwa ma być już w postaci kanonicznej: małe
// litery, bez kropki na końcu.
func PublicHostname(host string) bool {
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
