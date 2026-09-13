package mail

import (
	"context"
	"errors"
	"log"
)

var ErrNotConfigured = errors.New("mail: no transport configured")

type Message struct {
	To      string
	Subject string
	HTML    string
	Text    string

	// Obrazki wysyłane razem z wiadomością i przywoływane w HTML przez
	// cid:<ContentID>. Zdalny adres wymagałby publicznie osiągalnego hosta,
	// a backend stoi w sieci wewnętrznej.
	Inline []InlineImage
}

type InlineImage struct {
	ContentID string
	Filename  string
	MIMEType  string
	Content   []byte
}

type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

type NoopMailer struct{}

func (NoopMailer) Send(context.Context, Message) error {
	return ErrNotConfigured
}

type LogMailer struct{}

func (LogMailer) Send(_ context.Context, msg Message) error {
	log.Printf("MAIL_TRANSPORT=log, wiadomość nie została wysłana\nDo: %s\nTemat: %s\n%s", msg.To, msg.Subject, msg.Text)
	return nil
}
