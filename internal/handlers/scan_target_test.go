package handlers

import "testing"

func TestNormalizeTargetAcceptsPublicAddresses(t *testing.T) {
	for _, raw := range []string{
		"https://example.com",
		"http://example.com:8080/path?q=1",
		"https://sub.example.co.uk/",
		"https://EXAMPLE.com.",
		"https://xn--bcher-kva.example",
		"http://93.184.216.34/",
		"https://[2606:2800:220:1:248:1893:25c8:1946]/",
		"  https://example.com  ",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, reason := normalizeTarget(raw); reason != "" {
				t.Errorf("odrzucono publiczny adres: %s", reason)
			}
		})
	}
}

func TestNormalizeTargetRejectsMalformedURLs(t *testing.T) {
	for _, raw := range []string{
		"",
		"example.com",
		"ftp://example.com",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"https://",
		"https://user:pass@example.com",
		"http://%zz",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, reason := normalizeTarget(raw); reason != targetInvalid {
				t.Errorf("reason = %q, want %q", reason, targetInvalid)
			}
		})
	}
}

func TestNormalizeTargetRejectsInternalHosts(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1",
		"http://127.0.0.1:15672/api",
		"http://localhost:4000",
		"http://foo.localhost",
		"http://[::1]/",
		"http://[::ffff:127.0.0.1]/",
		"http://0.0.0.0",
		"http://0.1.2.3",
		"http://10.42.0.15",
		"http://172.16.0.1",
		"http://192.168.1.1",
		"http://100.64.0.1",
		"http://169.254.169.254/latest/meta-data/",
		"http://[fe80::1]/",
		"http://[fd00::1]/",
		"http://224.0.0.1",
		"http://printer.local",
		"http://rabbitmq.default.svc.cluster.local:15672",
		"http://rabbitmq.default.svc",
		"http://metadata.google.internal",
		"http://router.lan",
		"http://nas.home.arpa",
		"http://rabbitmq",
		"http://2130706433",
		"http://127.1",
		"http://0x7f.0x1",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, reason := normalizeTarget(raw); reason != targetNotPublic {
				t.Errorf("reason = %q, want %q", reason, targetNotPublic)
			}
		})
	}
}
