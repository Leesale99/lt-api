package mailer

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"math/rand/v2"
	"time"

	ht "html/template"
	tt "text/template"

	"github.com/wneessen/go-mail"
)

// Retry behaviour for Send: maxRetries total attempts, base delay doubling
// per attempt, with full jitter (sleep = random(0, base·2^attempt)) so that
// concurrent senders failing together desynchronize instead of retrying in
// synchronized waves. See the phase note / AWS "Exponential Backoff and Jitter".
const (
	maxRetries = 3
	baseDelay  = 500 * time.Millisecond
)

// ErrMissingCredentials is returned by New when required SMTP configuration
// is empty. It exists so callers can distinguish a config error (crash at
// startup) from a transport error (retry at send time).
var ErrMissingCredentials = errors.New("mailer: missing required SMTP configuration")

//go:embed "templates"
var templateFS embed.FS

type Mailer struct {
	client *mail.Client
	sender string
}

func New(host string, port int, username, password, sender string) (*Mailer, error) {
	// Fail fast: an unauthenticated or senderless SMTP client is unusable, so
	// refuse to construct rather than deferring the failure to the first send.
	if host == "" || username == "" || password == "" || sender == "" {
		return nil, ErrMissingCredentials
	}

	client, err := mail.NewClient(
		host,
		mail.WithSMTPAuth(mail.SMTPAuthLogin),
		mail.WithPort(port),
		mail.WithUsername(username),
		mail.WithPassword(password),
		mail.WithTimeout(5*time.Second),
	)
	if err != nil {
		return nil, err
	}

	mailer := &Mailer{
		client: client,
		sender: sender,
	}

	return mailer, nil
}

// Send renders and delivers one email, retrying with exponential backoff
// and full jitter. ctx cancels the whole send: checked before every redial
// and during every backoff sleep, so a shutdown cuts the retry ladder short
// instead of letting it run to completion after the server has stopped.
func (m *Mailer) Send(ctx context.Context, recipient string, templateFile string, data any) error {
	textTempl, err := tt.New("").ParseFS(templateFS, "templates/"+templateFile)
	if err != nil {
		return err
	}

	subject := new(bytes.Buffer)
	err = textTempl.ExecuteTemplate(subject, "subject", data)
	if err != nil {
		return err
	}

	plainBody := new(bytes.Buffer)
	err = textTempl.ExecuteTemplate(plainBody, "plainBody", data)
	if err != nil {
		return err
	}

	htmlTempl, err := ht.ParseFS(templateFS, "templates/"+templateFile)
	if err != nil {
		return err
	}

	htmlBody := new(bytes.Buffer)
	err = htmlTempl.ExecuteTemplate(htmlBody, "htmlBody", data)
	if err != nil {
		return err
	}

	msg := mail.NewMsg()

	err = msg.To(recipient)
	if err != nil {
		return err
	}

	err = msg.From(m.sender)
	if err != nil {
		return err
	}

	msg.Subject(subject.String())
	msg.SetBodyString(mail.TypeTextPlain, plainBody.String())
	msg.AddAlternativeString(mail.TypeTextHTML, htmlBody.String())

	for attempt := 0; attempt < maxRetries; attempt++ {
		err = m.client.DialAndSendWithContext(ctx, msg)
		if err == nil {
			return nil
		}

		// No sleep after the final attempt.
		if attempt == maxRetries-1 {
			break
		}

		// Exponential: 500ms -> 1s -> 2s. Full jitter: wait a random
		// duration in [0, backoff) instead of the exact backoff.
		backoff := baseDelay << attempt
		backoff += time.Duration(rand.Int64N(int64(backoff)))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}

	return err
}
