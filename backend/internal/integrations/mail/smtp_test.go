package mail

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net"
	"net/http/httptest"
	stdmail "net/mail"
	"strings"
	"testing"
	"time"

	"launchpad/internal/core/integrations"
)

func TestRelaySendsIntakeEmailOverVerifiedSTARTTLS(t *testing.T) {
	certServer := httptest.NewTLSServer(nil)
	defer certServer.Close()
	trust := x509.NewCertPool()
	trust.AddCert(certServer.Certificate())
	for _, scenario := range []struct {
		name    string
		ack     string
		unknown bool
	}{
		{"accepted", "250 accepted", false},
		{"rejected", "550 rejected", false},
		{"lost acknowledgement", "", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			outcome := make(chan error, 1)
			go func() { outcome <- receiveTestEmail(listener, certServer.TLS.Certificates, scenario.ack) }()
			host, port, _ := net.SplitHostPort(listener.Addr().String())
			relay, err := NewRelay(host, port, "", "", "coordinator@example.test")
			if err != nil {
				t.Fatal(err)
			}
			relay.tlsConfig.RootCAs = trust
			unknown, err := relay.Send(t.Context(), integrations.IntakeEmail{Recipient: "student@example.test", GroupName: "Test Group", ResponderURL: "https://docs.google.com/forms/d/e/test/viewform"})
			if unknown != scenario.unknown || (err == nil) != (scenario.ack == "250 accepted") {
				t.Fatalf("unknown=%t err=%v", unknown, err)
			}
			if err := <-outcome; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func receiveTestEmail(listener net.Listener, certificates []tls.Certificate, ack string) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	write := func(response string) error {
		_, err := fmt.Fprintf(conn, "%s\r\n", response)
		return err
	}
	expect := func(prefix, reply string) error {
		line, err := reader.ReadString('\n')
		if err != nil || !strings.HasPrefix(line, prefix) {
			return fmt.Errorf("expected %s, got %q: %v", prefix, line, err)
		}
		return write(reply)
	}
	if err := write("220 test relay"); err != nil {
		return err
	}
	if err := expect("EHLO ", "250-test relay\r\n250 STARTTLS"); err != nil {
		return err
	}
	if err := expect("STARTTLS", "220 ready"); err != nil {
		return err
	}
	conn = tls.Server(conn, &tls.Config{Certificates: certificates, MinVersion: tls.VersionTLS12})
	reader = bufio.NewReader(conn)
	for _, step := range [][2]string{
		{"EHLO ", "250 test relay"},
		{"MAIL FROM:<coordinator@example.test>", "250 sender ok"},
		{"RCPT TO:<student@example.test>", "250 recipient ok"},
		{"DATA", "354 continue"},
	} {
		if err := expect(step[0], step[1]); err != nil {
			return err
		}
	}
	var content strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if line == ".\r\n" {
			break
		}
		content.WriteString(line)
	}
	message, err := stdmail.ReadMessage(strings.NewReader(content.String()))
	if err != nil {
		return err
	}
	body, err := io.ReadAll(quotedprintable.NewReader(message.Body))
	if err != nil || !strings.Contains(string(body), "Test Group") || !strings.Contains(string(body), "https://docs.google.com/forms/d/e/test/viewform") ||
		message.Header.Get("To") != "student@example.test" {
		return fmt.Errorf("unexpected test message: %q (%v)", body, err)
	}
	if ack == "" {
		return nil
	}
	return write(ack)
}
