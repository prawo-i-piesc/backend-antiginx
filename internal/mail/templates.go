package mail

import (
	"fmt"
	"html"
)

func VerificationMessage(to, link string) Message {
	return Message{
		To:      to,
		Subject: "Potwierdź swój adres e-mail w AntiGinx",
		Text: fmt.Sprintf(
			"Potwierdź swój adres e-mail, otwierając poniższy link:\n\n%s\n\n"+
				"Link jest ważny 24 godziny. Jeżeli to nie Ty zakładałeś konto, zignoruj tę wiadomość.",
			link,
		),
		HTML: fmt.Sprintf(
			`<p>Potwierdź swój adres e-mail, klikając poniższy link:</p>`+
				`<p><a href="%s">Potwierdź adres e-mail</a></p>`+
				`<p>Link jest ważny 24 godziny. Jeżeli to nie Ty zakładałeś konto, zignoruj tę wiadomość.</p>`,
			html.EscapeString(link),
		),
	}
}

func PasswordResetMessage(to, link string) Message {
	return Message{
		To:      to,
		Subject: "Resetowanie hasła w AntiGinx",
		Text: fmt.Sprintf(
			"Aby ustawić nowe hasło, otwórz poniższy link:\n\n%s\n\n"+
				"Link jest ważny 30 minut i można go użyć raz. "+
				"Jeżeli to nie Ty prosiłeś o zmianę hasła, zignoruj tę wiadomość — hasło pozostanie bez zmian.",
			link,
		),
		HTML: fmt.Sprintf(
			`<p>Aby ustawić nowe hasło, kliknij poniższy link:</p>`+
				`<p><a href="%s">Ustaw nowe hasło</a></p>`+
				`<p>Link jest ważny 30 minut i można go użyć raz. `+
				`Jeżeli to nie Ty prosiłeś o zmianę hasła, zignoruj tę wiadomość — hasło pozostanie bez zmian.</p>`,
			html.EscapeString(link),
		),
	}
}
