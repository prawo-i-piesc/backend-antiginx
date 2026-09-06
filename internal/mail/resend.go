package mail

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	resendBaseURL = "https://api.resend.com"
	resendTimeout = 15 * time.Second
)

type Resend struct {
	apiKey  string
	from    string
	baseURL string
	client  *http.Client
}

func NewResend(apiKey, from string) *Resend {
	return &Resend{
		apiKey:  apiKey,
		from:    from,
		baseURL: resendBaseURL,
		client:  &http.Client{Timeout: resendTimeout},
	}
}

func (r *Resend) Send(ctx context.Context, msg Message) error {
	body := map[string]any{
		"from":    r.from,
		"to":      []string{msg.To},
		"subject": msg.Subject,
		"html":    msg.HTML,
		"text":    msg.Text,
	}

	if len(msg.Inline) > 0 {
		attachments := make([]map[string]any, 0, len(msg.Inline))
		for _, image := range msg.Inline {
			attachments = append(attachments, map[string]any{
				"filename":     image.Filename,
				"content":      base64.StdEncoding.EncodeToString(image.Content),
				"content_id":   image.ContentID,
				"content_type": image.MIMEType,
			})
		}
		body["attachments"] = attachments
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/emails", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return nil
	}

	details, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("mail: resend returned %d: %s", resp.StatusCode, bytes.TrimSpace(details))
}
