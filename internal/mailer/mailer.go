package mailer

import (
	"bytes"
	"embed"
	"time"

	ht "html/template"
	tt "text/template"

	"github.com/wneessen/go-mail"
)

//go:embed "templates"
var templateFS embed.FS

type Mailer struct {
	client *mail.Client
	sender string
}

func New(host string, port int, username, password, sender string) (*Mailer, error) {
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

func (m *Mailer) Send(recipient string, templateFile string, data any) error {
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

	for i := 1; i <= 3; i++ {
		err = m.client.DialAndSend(msg)
		if err == nil {
			return nil
		}

		if i != 3 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	return err
}
