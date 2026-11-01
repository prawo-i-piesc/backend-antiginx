package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

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
			if _, reason := normalizeTarget(raw, false); reason != "" {
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
			if _, reason := normalizeTarget(raw, false); reason != targetInvalid {
				t.Errorf("reason = %q, want %q", reason, targetInvalid)
			}
		})
	}
}

// internalTargets to adresy, które bez ANTIGINX_ALLOW_PRIVATE_TARGETS muszą
// zostać odrzucone, a z nim przepuszczone.
var internalTargets = []string{
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
}

func TestNormalizeTargetRejectsInternalHosts(t *testing.T) {
	for _, raw := range internalTargets {
		t.Run(raw, func(t *testing.T) {
			if _, reason := normalizeTarget(raw, false); reason != targetNotPublic {
				t.Errorf("reason = %q, want %q", reason, targetNotPublic)
			}
		})
	}
}

func TestNormalizeTargetAllowsInternalHostsWhenEnabled(t *testing.T) {
	for _, raw := range append([]string{"http://juice-shop-service:3000", "http://juice-shop.dev.svc.cluster.local:3000/#/login"}, internalTargets...) {
		t.Run(raw, func(t *testing.T) {
			if _, reason := normalizeTarget(raw, true); reason != "" {
				t.Errorf("odrzucono adres wewnętrzny mimo przełącznika: %s", reason)
			}
		})
	}
}

// Przełącznik zdejmuje tylko sprawdzanie, czy adres jest publiczny; śmieci
// dalej odpadają.
func TestNormalizeTargetRejectsMalformedURLsWhenEnabled(t *testing.T) {
	for _, raw := range []string{"", "juice-shop-service:3000", "ftp://example.com", "file:///etc/passwd", "https://", "http://user:pass@10.0.0.1", "http://%zz", "http://[fe80::1%25eth0]/"} {
		t.Run(raw, func(t *testing.T) {
			if _, reason := normalizeTarget(raw, true); reason != targetInvalid {
				t.Errorf("reason = %q, want %q", reason, targetInvalid)
			}
		})
	}
}

func TestNormalizeTargetGivesTheEngineATargetWithoutScheme(t *testing.T) {
	for _, tc := range []struct {
		raw, url, engine string
		allowPrivate     bool
	}{
		{raw: "https://example.com", url: "https://example.com", engine: "example.com"},
		{raw: "http://example.com:8080/path?q=1", url: "http://example.com:8080/path?q=1", engine: "example.com:8080/path?q=1"},
		{raw: "https://EXAMPLE.com./", url: "https://EXAMPLE.com./", engine: "example.com/"},
		{raw: "https://example.com/a%20b/c?x=1&y=2#frag", url: "https://example.com/a%20b/c?x=1&y=2#frag", engine: "example.com/a%20b/c?x=1&y=2"},
		{raw: "https://[2606:2800:220:1:248:1893:25c8:1946]/", url: "https://[2606:2800:220:1:248:1893:25c8:1946]/", engine: "[2606:2800:220:1:248:1893:25c8:1946]/"},
		{raw: "https://huubi.dev", url: "https://huubi.dev", engine: "huubi.dev"},
		{raw: "http://juice-shop-service:3000", url: "http://juice-shop-service:3000", engine: "juice-shop-service:3000", allowPrivate: true},
		{raw: "http://[::1]:8080/x", url: "http://[::1]:8080/x", engine: "[::1]:8080/x", allowPrivate: true},
		{raw: "http://10.0.0.5", url: "http://10.0.0.5", engine: "10.0.0.5", allowPrivate: true},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			target, reason := normalizeTarget(tc.raw, tc.allowPrivate)
			if reason != "" {
				t.Fatalf("odrzucono: %s", reason)
			}
			if target.URL != tc.url || target.Engine != tc.engine {
				t.Errorf("got URL %q, Engine %q; want %q, %q", target.URL, target.Engine, tc.url, tc.engine)
			}
		})
	}
}

// Silnik przerywa skan, gdy cel zaczyna się od "http" (TargetFormatter.Format),
// więc zadanie na kolejce musi nieść cel bez schematu.
func TestScanTaskCarriesTheTargetWithoutScheme(t *testing.T) {
	target, reason := normalizeTarget("https://huubi.dev/login?next=%2F", false)
	if reason != "" {
		t.Fatalf("odrzucono: %s", reason)
	}
	scanID := uuid.MustParse("019a3b6e-1111-7000-8000-000000000001")

	body, err := json.Marshal(newScanTask(scanID, target, []string{"https", "csp"}, true))
	if err != nil {
		t.Fatal(err)
	}

	var task struct {
		Target     string
		TaskId     string
		Parameters []struct {
			Name      string
			Arguments []string
		}
	}
	if err := json.Unmarshal(body, &task); err != nil {
		t.Fatal(err)
	}
	if task.Target != "huubi.dev/login?next=%2F" || strings.HasPrefix(task.Target, "http") {
		t.Errorf("Target = %q, want huubi.dev/login?next=%%2F", task.Target)
	}
	if task.TaskId != scanID.String() {
		t.Errorf("TaskId = %q", task.TaskId)
	}
	if len(task.Parameters) != 2 || task.Parameters[0].Name != "--tests" || strings.Join(task.Parameters[0].Arguments, ",") != "https,csp" || task.Parameters[1].Name != "--antiBotDetection" {
		t.Errorf("Parameters = %+v", task.Parameters)
	}
}
