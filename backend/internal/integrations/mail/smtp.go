package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	stdmail "net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"time"

	"launchpad/internal/core/integrations"
)

type Relay struct {
	host, address, username, password, from string
	tlsConfig                               *tls.Config
}

func NewRelay(host, port, username, password, from string) (*Relay, error) {
	number, err := strconv.Atoi(port)
	if host == "" || err != nil || number < 1 || number > 65535 || (username == "") != (password == "") || !validAddress(from) {
		return nil, errors.New("SMTP requires a host, port, sender address and paired credentials if authentication is used")
	}
	return &Relay{host: host, address: net.JoinHostPort(host, port), username: username, password: password, from: from,
		tlsConfig: &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}}, nil
}

func validAddress(value string) bool {
	addr, err := stdmail.ParseAddress(value)
	return err == nil && addr.Address == value && addr.Name == ""
}

func (r *Relay) Send(ctx context.Context, email integrations.IntakeEmail) (bool, error) {
	return r.SendMessage(ctx, email.Recipient, intakeSubject, intakeBody(email))
}

func (r *Relay) SendMessage(ctx context.Context, recipient, subject, body string) (bool, error) {
	if !validAddress(recipient) {
		return false, errors.New("email recipient address is invalid")
	}
	message := formattedMessage(r.from, recipient, subject, body)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", r.address)
	if err != nil {
		return false, errors.New("could not connect to SMTP relay")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	client, err := smtp.NewClient(conn, r.host)
	if err != nil {
		return false, errors.New("could not start SMTP session")
	}
	defer client.Close()
	if err := client.StartTLS(r.tlsConfig); err != nil {
		return false, errors.New("SMTP relay did not establish a trusted TLS connection")
	}
	if r.username != "" {
		if err := client.Auth(smtp.PlainAuth("", r.username, r.password, r.host)); err != nil {
			return false, errors.New("SMTP relay authentication failed")
		}
	}
	if err := client.Mail(r.from); err != nil {
		return false, errors.New("SMTP relay rejected the sender")
	}
	if err := client.Rcpt(recipient); err != nil {
		return false, errors.New("SMTP relay rejected the recipient")
	}
	writer, err := client.Data()
	if err != nil {
		return false, errors.New("SMTP relay rejected the message")
	}
	if _, err := writer.Write(message); err != nil {
		return false, errors.New("could not write email to SMTP relay")
	}
	if err := writer.Close(); err != nil {
		var reply *textproto.Error
		if errors.As(err, &reply) && reply.Code >= 400 && reply.Code <= 599 {
			return false, errors.New("SMTP relay rejected the message")
		}
		return true, errors.New("SMTP acknowledgement was lost; delivery may have succeeded")
	}
	return false, nil
}

const intakeSubject = "Group Launchpad student intake"

func intakeBody(email integrations.IntakeEmail) string {
	if email.Message != "" {
		return email.Message + "\n\n" + email.ResponderURL + "\n"
	}
	return fmt.Sprintf("Hello,\n\nPlease complete the intake form for %s:\n\n%s\n", email.GroupName, email.ResponderURL)
}

func formattedMessage(from, recipient, subject, body string) []byte {
	var message bytes.Buffer
	fmt.Fprintf(&message, "From: %s\r\nTo: %s\r\n", from, recipient)
	fmt.Fprintf(&message, "Date: %s\r\nSubject: %s\r\n", time.Now().Format(time.RFC1123Z), mime.QEncoding.Encode("UTF-8", subject))
	fmt.Fprint(&message, "MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	writer := quotedprintable.NewWriter(&message)
	_, _ = writer.Write([]byte(body))
	_ = writer.Close()
	return message.Bytes()
}
