package controlplane

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"
)

const (
	migaduSMTPHost = "smtp.migadu.com"
	migaduSMTPPort = "587"
)

func migaduSMTPConfig() (string, string) {
	host := strings.TrimSpace(os.Getenv("MIGADU_SMTP_HOST"))
	if host == "" {
		host = migaduSMTPHost
	}
	port := strings.TrimSpace(os.Getenv("MIGADU_SMTP_PORT"))
	if port == "" {
		port = migaduSMTPPort
	}
	return host, port
}

func defaultMigaduSendSMTP(ctx context.Context, username string, password string, from string, recipients []string, data []byte) error {
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	from = strings.TrimSpace(from)
	if username == "" || password == "" || from == "" {
		return fmt.Errorf("smtp username/password/from required")
	}
	if len(recipients) == 0 {
		return fmt.Errorf("smtp recipients required")
	}
	if len(data) == 0 {
		return fmt.Errorf("smtp data required")
	}

	host, port := migaduSMTPConfig()
	addr := net.JoinHostPort(host, port)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp client: %w", err)
	}
	defer c.Close()

	if err := startTLSIfSupported(c, host); err != nil {
		return err
	}
	if err := authIfSupported(c, username, password, host); err != nil {
		return err
	}
	if err := setSMTPMailFrom(c, from); err != nil {
		return err
	}
	if err := setSMTPRecipients(c, recipients); err != nil {
		return err
	}
	if err := sendSMTPData(c, data); err != nil {
		return err
	}
	if err := c.Quit(); err != nil {
		return fmt.Errorf("smtp quit: %w", err)
	}
	return nil
}

func startTLSIfSupported(c *smtp.Client, host string) error {
	if ok, _ := c.Extension("STARTTLS"); !ok {
		return nil
	}

	tlsCfg := &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	}
	startTLSErr := c.StartTLS(tlsCfg)
	if startTLSErr != nil {
		return fmt.Errorf("smtp starttls: %w", startTLSErr)
	}
	return nil
}

func authIfSupported(c *smtp.Client, username string, password string, host string) error {
	if ok, _ := c.Extension("AUTH"); !ok {
		return nil
	}

	auth := smtp.PlainAuth("", username, password, host)
	authErr := c.Auth(auth)
	if authErr != nil {
		return fmt.Errorf("smtp auth: %w", authErr)
	}
	return nil
}

func setSMTPMailFrom(c *smtp.Client, from string) error {
	mailErr := c.Mail(from)
	if mailErr != nil {
		return fmt.Errorf("smtp mail from: %w", mailErr)
	}
	return nil
}

func setSMTPRecipients(c *smtp.Client, recipients []string) error {
	for _, rcpt := range recipients {
		rcpt = strings.TrimSpace(rcpt)
		if rcpt == "" {
			continue
		}
		rcptErr := c.Rcpt(rcpt)
		if rcptErr != nil {
			return fmt.Errorf("smtp rcpt %q: %w", rcpt, rcptErr)
		}
	}
	return nil
}

func sendSMTPData(c *smtp.Client, data []byte) error {
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, writeErr := w.Write(data); writeErr != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", writeErr)
	}
	closeErr := w.Close()
	if closeErr != nil {
		return fmt.Errorf("smtp close data: %w", closeErr)
	}
	return nil
}
