package mail

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResendSendsExpectedRequest(t *testing.T) {
	var (
		authorization string
		contentType   string
		path          string
		body          map[string]any
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		contentType = r.Header.Get("Content-Type")
		path = r.URL.Path

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("ciało nie jest poprawnym JSON-em: %v", err)
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"abc"}`))
	}))
	defer server.Close()

	resend := NewResend("re_klucz", "AntiGinx <noreply@antiginx.pl>")
	resend.baseURL = server.URL

	msg := Message{To: "jan@example.com", Subject: "Temat", HTML: "<p>treść</p>", Text: "treść"}
	if err := resend.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if path != "/emails" {
		t.Errorf("ścieżka = %q", path)
	}
	if authorization != "Bearer re_klucz" {
		t.Errorf("Authorization = %q", authorization)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q", contentType)
	}
	if body["from"] != "AntiGinx <noreply@antiginx.pl>" {
		t.Errorf("from = %v", body["from"])
	}
	if body["subject"] != "Temat" {
		t.Errorf("subject = %v", body["subject"])
	}

	recipients, ok := body["to"].([]any)
	if !ok || len(recipients) != 1 || recipients[0] != "jan@example.com" {
		t.Errorf("to = %v", body["to"])
	}
}

func TestResendReportsAPIErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"domain is not verified"}`))
	}))
	defer server.Close()

	resend := NewResend("re_klucz", "noreply@antiginx.pl")
	resend.baseURL = server.URL

	err := resend.Send(context.Background(), Message{To: "jan@example.com"})
	if err == nil {
		t.Fatal("Send nie zgłosił błędu przy odpowiedzi 422")
	}
	if !strings.Contains(err.Error(), "422") || !strings.Contains(err.Error(), "domain is not verified") {
		t.Errorf("błąd nie niesie powodu odrzucenia: %v", err)
	}
}

func TestNoopMailerReportsMissingConfiguration(t *testing.T) {
	if err := (NoopMailer{}).Send(context.Background(), Message{}); err != ErrNotConfigured {
		t.Errorf("err = %v, want ErrNotConfigured", err)
	}
}

// Link musi przetrwać ucieczkę HTML w niezmienionej postaci, inaczej token
// przestaje działać po kliknięciu.
func TestTemplatesCarryTheLink(t *testing.T) {
	link := "https://antiginx.pl/reset-password?token=aBc-123_XYZ"

	for name, msg := range map[string]Message{
		"weryfikacja": VerificationMessage("jan@example.com", link),
		"reset hasła": PasswordResetMessage("jan@example.com", link),
	} {
		t.Run(name, func(t *testing.T) {
			if msg.To != "jan@example.com" {
				t.Errorf("To = %q", msg.To)
			}
			if msg.Subject == "" {
				t.Error("pusty temat")
			}
			if !strings.Contains(msg.Text, link) {
				t.Error("wersja tekstowa nie zawiera linku")
			}
			if !strings.Contains(msg.HTML, link) {
				t.Errorf("wersja HTML nie zawiera linku w użytecznej postaci: %s", msg.HTML)
			}
		})
	}
}

// Logo jedzie z wiadomością, bo backend stoi w sieci wewnętrznej i klient
// pocztowy nie miałby skąd pobrać go po adresie.
func TestTemplatesShipTheLogoWithTheMessage(t *testing.T) {
	msg := VerificationMessage("jan@example.com", "https://antiginx.pl/verify-email?token=abc")

	if len(msg.Inline) != 1 {
		t.Fatalf("załączników inline: %d, want 1", len(msg.Inline))
	}
	logo := msg.Inline[0]
	if len(logo.Content) == 0 {
		t.Error("załącznik jest pusty")
	}
	if !strings.Contains(msg.HTML, "cid:"+logo.ContentID) {
		t.Errorf("HTML nie przywołuje załącznika: %s", msg.HTML)
	}
	if strings.Contains(msg.HTML, "logotype.png\"") && strings.Contains(msg.HTML, "src=\"http") {
		t.Error("HTML nadal linkuje logo po adresie")
	}
	if !strings.Contains(msg.HTML, `alt="AntiGinx"`) {
		t.Error("brak tekstu alternatywnego, a obrazy bywają blokowane")
	}
}

// Wiadomość bez części tekstowej ląduje w spamie częściej i jest nieczytelna
// tam, gdzie HTML jest wyłączony.
func TestTemplatesKeepAPlainTextPart(t *testing.T) {
	link := "https://antiginx.pl/reset-password?token=abc"

	for name, msg := range map[string]Message{
		"weryfikacja": VerificationMessage("jan@example.com", link),
		"reset hasła": PasswordResetMessage("jan@example.com", link),
	} {
		t.Run(name, func(t *testing.T) {
			if strings.TrimSpace(msg.Text) == "" {
				t.Fatal("brak części tekstowej")
			}
			if strings.Contains(msg.Text, "<") {
				t.Error("część tekstowa zawiera znaczniki")
			}
		})
	}
}
