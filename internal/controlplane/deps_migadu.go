package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

type migaduCreateForwardingRequest struct {
	Address string `json:"address"`
}

type migaduUpdateMailboxRequest struct {
	//nolint:gosec // This field is required by Migadu's mailbox-update API payload and is never logged.
	Credential string `json:"password"`
}

type migaduAPIError struct {
	operation  string
	statusCode int
}

func (e *migaduAPIError) Error() string {
	if e == nil {
		return "migadu api error"
	}
	return fmt.Sprintf("%s: status=%d", e.operation, e.statusCode)
}

func isMigaduStatus(err error, statusCode int) bool {
	var apiErr *migaduAPIError
	return errors.As(err, &apiErr) && apiErr.statusCode == statusCode
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

	// Migadu returns 201 Created on success. A 409 Conflict means the mailbox
	// belongs to an existing provider-side resource and must not be treated as
	// success; doing so would let a later forwarding call mutate a mailbox this
	// agent did not create.
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return nil
	}

	return &migaduAPIError{operation: "migadu create mailbox", statusCode: resp.StatusCode}
}

func defaultMigaduUpdateMailboxPassword(ctx context.Context, localPart string, password string) error {
	localPart = strings.TrimSpace(localPart)
	password = strings.TrimSpace(password)
	if localPart == "" || password == "" {
		return fmt.Errorf("migadu mailbox localPart and password are required")
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

	//nolint:gosec // Password must be sent in the outbound Migadu mailbox update request body.
	body, err := json.Marshal(migaduUpdateMailboxRequest{Credential: password})
	if err != nil {
		return fmt.Errorf("migadu update mailbox encode: %w", err)
	}

	u := migaduBaseURL + "/domains/" + migaduEmailDomain + "/mailboxes/" + localPart
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("migadu update mailbox build: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.SetBasicAuth(strings.TrimSpace(creds.Username), strings.TrimSpace(creds.APIToken))

	client := &http.Client{Timeout: 10 * time.Second}
	//nolint:gosec // Request target is the fixed Migadu HTTPS API host.
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("migadu update mailbox: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		return nil
	}

	return &migaduAPIError{operation: "migadu update mailbox", statusCode: resp.StatusCode}
}

func defaultMigaduCreateForwarding(ctx context.Context, localPart string, address string) error {
	localPart = strings.TrimSpace(localPart)
	address = strings.TrimSpace(address)
	if localPart == "" || address == "" {
		return fmt.Errorf("migadu forwarding localPart and address are required")
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

	body, err := json.Marshal(migaduCreateForwardingRequest{Address: address})
	if err != nil {
		return fmt.Errorf("migadu forwarding encode: %w", err)
	}

	u := migaduBaseURL + "/domains/" + migaduEmailDomain + "/mailboxes/" + localPart + "/forwardings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("migadu forwarding build: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.SetBasicAuth(strings.TrimSpace(creds.Username), strings.TrimSpace(creds.APIToken))

	client := &http.Client{Timeout: 10 * time.Second}
	//nolint:gosec // Request target is the fixed Migadu HTTPS API host.
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("migadu forwarding: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusConflict:
		return nil
	}

	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("migadu forwarding: status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(msg)))
}

func defaultMigaduDeleteMailbox(ctx context.Context, localPart string) error {
	localPart = strings.TrimSpace(localPart)
	if localPart == "" {
		return fmt.Errorf("migadu mailbox localPart is required")
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

	u := migaduBaseURL + "/domains/" + migaduEmailDomain + "/mailboxes/" + localPart
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return fmt.Errorf("migadu delete mailbox build: %w", err)
	}
	req.SetBasicAuth(strings.TrimSpace(creds.Username), strings.TrimSpace(creds.APIToken))

	client := &http.Client{Timeout: 10 * time.Second}
	//nolint:gosec // Request target is the fixed Migadu HTTPS API host.
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("migadu delete mailbox: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound:
		return nil
	}

	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("migadu delete mailbox: status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(msg)))
}
