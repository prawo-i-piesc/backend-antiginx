package mail

import (
	"fmt"
	"html"
	"net/url"
	"strings"
)

// Układ jest zbudowany na tabelach i stylach inline, bo Outlook renderuje HTML
// silnikiem Worda: nie zna flexboksa, gridu ani arkuszy w <style>. Przycisk
// też jest tabelą — Outlook ignoruje padding na <a> i zostałby z niego sam
// tekst bez tła.
//
// Nagłówek jest ciemny jak panel, ale reszta jasna. Cały ciemny szablon
// bywa odwracany przez tryb ciemny w Gmailu i Outlooku, co potrafi zrobić
// z niego nieczytelną mieszankę.
const (
	colorInk      = "#18181b"
	colorBody     = "#3f3f46"
	colorMuted    = "#71717a"
	colorPage     = "#f4f4f5"
	colorPanel    = "#09090b"
	colorAccent   = "#0891b2"
	colorHairline = "#e4e4e7"
)

// originOf wyciąga adres serwisu z samego linku. Logo musi pochodzić z tego
// samego miejsca co link, więc branie go stąd nie pozwala tym dwóm się
// rozjechać.
func originOf(link string) string {
	parsed, err := url.Parse(link)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func layout(preheader, heading, intro, action, link, note string) string {
	origin := originOf(link)
	safeLink := html.EscapeString(link)

	// Obrazy bywają domyślnie blokowane, więc marka musi czytać się też z
	// samego tekstu alternatywnego.
	logo := `<span style="color:#ffffff;font-size:18px;font-weight:700;letter-spacing:0.08em">ANTIGINX</span>`
	if origin != "" {
		logo = fmt.Sprintf(
			`<img src="%s/logotype.png" width="132" height="28" alt="AntiGinx"`+
				` style="display:block;border:0;outline:none;text-decoration:none;height:28px">`,
			html.EscapeString(origin),
		)
	}

	return fmt.Sprintf(`<!doctype html>
<html lang="pl">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;padding:0;background:%[1]s;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;">%[2]s</div>
<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" border="0" style="background:%[1]s;">
  <tr><td align="center" style="padding:32px 16px;">
    <table role="presentation" width="520" cellpadding="0" cellspacing="0" border="0" style="width:520px;max-width:100%%;">
      <tr><td style="background:%[3]s;padding:20px 28px;border-radius:12px 12px 0 0;">%[4]s</td></tr>
      <tr><td style="background:#ffffff;border:1px solid %[5]s;border-top:0;border-radius:0 0 12px 12px;padding:32px 28px;">
        <h1 style="margin:0 0 12px;font-family:Arial,Helvetica,sans-serif;font-size:20px;line-height:1.3;color:%[6]s;">%[7]s</h1>
        <p style="margin:0 0 24px;font-family:Arial,Helvetica,sans-serif;font-size:15px;line-height:1.6;color:%[8]s;">%[9]s</p>
        <table role="presentation" cellpadding="0" cellspacing="0" border="0"><tr>
          <td align="center" bgcolor="%[10]s" style="border-radius:8px;">
            <a href="%[11]s" style="display:inline-block;padding:14px 28px;font-family:Arial,Helvetica,sans-serif;font-size:15px;font-weight:bold;color:#ffffff;text-decoration:none;border-radius:8px;">%[12]s</a>
          </td>
        </tr></table>
        <p style="margin:24px 0 0;font-family:Arial,Helvetica,sans-serif;font-size:13px;line-height:1.6;color:%[13]s;">
          Jeśli przycisk nie działa, wklej ten adres w przeglądarkę:<br>
          <span style="word-break:break-all;color:%[10]s;">%[11]s</span>
        </p>
        <p style="margin:24px 0 0;padding-top:20px;border-top:1px solid %[5]s;font-family:Arial,Helvetica,sans-serif;font-size:13px;line-height:1.6;color:%[13]s;">%[14]s</p>
      </td></tr>
      <tr><td style="padding:20px 28px;font-family:Arial,Helvetica,sans-serif;font-size:12px;color:%[13]s;text-align:center;">
        AntiGinx — skaner bezpieczeństwa stron
      </td></tr>
    </table>
  </td></tr>
</table>
</body></html>`,
		colorPage, html.EscapeString(preheader), colorPanel, logo, colorHairline,
		colorInk, html.EscapeString(heading), colorBody, html.EscapeString(intro),
		colorAccent, safeLink, html.EscapeString(action), colorMuted, html.EscapeString(note))
}

func VerificationMessage(to, link string) Message {
	const note = "Link jest ważny 24 godziny. Jeżeli to nie Ty zakładałeś konto, zignoruj tę wiadomość."

	return Message{
		To:      to,
		Subject: "Potwierdź swój adres e-mail w AntiGinx",
		Text: strings.Join([]string{
			"Potwierdź swój adres e-mail, otwierając poniższy link:",
			"",
			link,
			"",
			note,
		}, "\n"),
		HTML: layout(
			"Potwierdź adres, żeby dokończyć zakładanie konta.",
			"Potwierdź swój adres e-mail",
			"Zostało ostatnie kliknięcie. Potwierdzenie adresu odblokowuje konto i pozwala nam odezwać się, gdybyś kiedyś resetował hasło.",
			"Potwierdź adres e-mail",
			link,
			note,
		),
	}
}

func PasswordResetMessage(to, link string) Message {
	const note = "Link jest ważny 30 minut i można go użyć raz. Jeżeli to nie Ty prosiłeś o zmianę hasła, zignoruj tę wiadomość — hasło pozostanie bez zmian."

	return Message{
		To:      to,
		Subject: "Resetowanie hasła w AntiGinx",
		Text: strings.Join([]string{
			"Aby ustawić nowe hasło, otwórz poniższy link:",
			"",
			link,
			"",
			note,
		}, "\n"),
		HTML: layout(
			"Ustaw nowe hasło do swojego konta.",
			"Ustaw nowe hasło",
			"Otrzymaliśmy prośbę o zmianę hasła do Twojego konta. Kliknij poniżej, żeby wybrać nowe.",
			"Ustaw nowe hasło",
			link,
			note,
		),
	}
}
