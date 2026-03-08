package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/secrets"
)

const (
	migaduBaseURL     = "https://api.migadu.com/v1"
	migaduEmailDomain = "lessersoul.ai"
)

func defaultSSMGetParameter(ctx context.Context, name string) (string, error) {
	return secrets.GetSSMParameter(ctx, nil, name)
}

func defaultSSMPutSecureString(ctx context.Context, name string, value string, overwrite bool) error {
	return secrets.PutSSMSecureString(ctx, nil, name, value, overwrite)
}

type migaduCreateMailboxRequest struct {
	Name      string `json:"name"`
	LocalPart string `json:"local_part"`
	//nolint:gosec // This field is required by Migadu's mailbox-create API payload and is not persisted in code.
	Credential            string `json:"password"`
	PasswordRecoveryEmail any    `json:"password_recovery_email"`
}

func defaultMigaduCreateMailbox(ctx context.Context, localPart string, name string, password string) error {
	localPart = strings.TrimSpace(localPart)
	name = strings.TrimSpace(name)
	password = strings.TrimSpace(password)
	if localPart == "" || password == "" {
		return fmt.Errorf("migadu mailbox localPart and password are required")
	}
	if name == "" {
		name = localPart
	}

	creds, err := secrets.MigaduCreds(ctx, nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(creds.APIToken) == "" {
		return fmt.Errorf("migadu api key missing")
	}
	if strings.TrimSpace(creds.Username) == "" {
		return fmt.Errorf("migadu username missing")
	}

	//nolint:gosec // Password must be sent in the outbound Migadu mailbox creation request body.
	body, err := json.Marshal(migaduCreateMailboxRequest{
		Name:                  name,
		LocalPart:             localPart,
		Credential:            password,
		PasswordRecoveryEmail: nil,
	})
	if err != nil {
		return fmt.Errorf("migadu request encode: %w", err)
	}

	u := migaduBaseURL + "/domains/" + migaduEmailDomain + "/mailboxes"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("migadu request build: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.SetBasicAuth(strings.TrimSpace(creds.Username), strings.TrimSpace(creds.APIToken))

	client := &http.Client{Timeout: 10 * time.Second}
	//nolint:gosec // Request target is the fixed Migadu HTTPS API host.
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("migadu create mailbox: %w", err)
	}
	defer resp.Body.Close()

	// Migadu returns 201 Created on success, and typically 409 Conflict if the mailbox already exists.
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusConflict:
		return nil
	}

	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("migadu create mailbox: status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(msg)))
}
