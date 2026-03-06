package controlplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/equaltoai/lesser-host/internal/secrets"
)

const telnyxBaseURL = "https://api.telnyx.com/v2"

type telnyxAvailablePhoneNumbersResponse struct {
	Data []struct {
		PhoneNumber string `json:"phone_number"`
	} `json:"data"`
}

func defaultTelnyxSearchAvailablePhoneNumbers(ctx context.Context, countryCode string, limit int) ([]string, error) {
	countryCode = strings.ToUpper(strings.TrimSpace(countryCode))
	if countryCode == "" {
		countryCode = "US"
	}
	if limit <= 0 {
		limit = 5
	}
	if limit > 50 {
		limit = 50
	}

	creds, err := secrets.TelnyxCreds(ctx, nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(creds.APIKey) == "" {
		return nil, fmt.Errorf("telnyx api key missing")
	}

	u, err := url.Parse(telnyxBaseURL + "/available_phone_numbers")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("filter[country_code]", countryCode)
	q.Set("filter[phone_number_type]", "local")
	q.Set("filter[limit]", strconv.Itoa(limit))
	// Request SMS+voice capable numbers by default.
	q.Set("filter[features]", "sms,voice")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("authorization", "Bearer "+strings.TrimSpace(creds.APIKey))

	client := &http.Client{Timeout: 10 * time.Second}
	//nolint:gosec // Request target is the fixed Telnyx HTTPS API host.
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telnyx available numbers: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telnyx available numbers: status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed telnyxAvailablePhoneNumbersResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("telnyx available numbers: decode: %w", err)
	}

	out := make([]string, 0, len(parsed.Data))
	for _, it := range parsed.Data {
		num := strings.TrimSpace(it.PhoneNumber)
		if num == "" {
			continue
		}
		out = append(out, num)
	}
	return out, nil
}

type telnyxCreateNumberOrderRequest struct {
	PhoneNumbers []struct {
		PhoneNumber string `json:"phone_number"`
	} `json:"phone_numbers"`
	MessagingProfileID string `json:"messaging_profile_id"`
	ConnectionID       string `json:"connection_id,omitempty"`
}

type telnyxCreateNumberOrderResponse struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

func defaultTelnyxOrderPhoneNumber(ctx context.Context, phoneNumber string) (string, error) {
	phoneNumber = strings.TrimSpace(phoneNumber)
	if phoneNumber == "" {
		return "", fmt.Errorf("telnyx phoneNumber is required")
	}

	creds, err := secrets.TelnyxCreds(ctx, nil)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(creds.APIKey) == "" {
		return "", fmt.Errorf("telnyx api key missing")
	}
	if strings.TrimSpace(creds.MessagingProfileID) == "" {
		return "", fmt.Errorf("telnyx messaging_profile_id missing")
	}

	reqBody, err := json.Marshal(telnyxCreateNumberOrderRequest{
		PhoneNumbers: []struct {
			PhoneNumber string `json:"phone_number"`
		}{{PhoneNumber: phoneNumber}},
		MessagingProfileID: strings.TrimSpace(creds.MessagingProfileID),
		ConnectionID:       strings.TrimSpace(creds.ConnectionID),
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, telnyxBaseURL+"/number_orders", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+strings.TrimSpace(creds.APIKey))

	client := &http.Client{Timeout: 10 * time.Second}
	//nolint:gosec // Request target is the fixed Telnyx HTTPS API host.
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("telnyx number order: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted:
		// ok
	case http.StatusConflict:
		// Idempotency: treat already-ordered numbers as success.
		return "", nil
	default:
		return "", fmt.Errorf("telnyx number order: status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed telnyxCreateNumberOrderResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("telnyx number order: decode: %w", err)
	}
	return strings.TrimSpace(parsed.Data.ID), nil
}

type telnyxListPhoneNumbersResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func defaultTelnyxLookupPhoneNumberID(ctx context.Context, phoneNumber string) (string, bool, error) {
	phoneNumber = strings.TrimSpace(phoneNumber)
	if phoneNumber == "" {
		return "", false, fmt.Errorf("telnyx phoneNumber is required")
	}

	creds, err := secrets.TelnyxCreds(ctx, nil)
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(creds.APIKey) == "" {
		return "", false, fmt.Errorf("telnyx api key missing")
	}

	u, err := url.Parse(telnyxBaseURL + "/phone_numbers")
	if err != nil {
		return "", false, err
	}
	q := u.Query()
	q.Set("filter[phone_number]", phoneNumber)
	q.Set("page[size]", "1")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("authorization", "Bearer "+strings.TrimSpace(creds.APIKey))

	client := &http.Client{Timeout: 10 * time.Second}
	//nolint:gosec // Request target is the fixed Telnyx HTTPS API host.
	resp, err := client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("telnyx phone number lookup: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("telnyx phone number lookup: status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed telnyxListPhoneNumbersResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", false, fmt.Errorf("telnyx phone number lookup: decode: %w", err)
	}
	if len(parsed.Data) == 0 || strings.TrimSpace(parsed.Data[0].ID) == "" {
		return "", false, nil
	}
	return strings.TrimSpace(parsed.Data[0].ID), true, nil
}

func defaultTelnyxReleasePhoneNumber(ctx context.Context, phoneNumber string) error {
	id, ok, err := defaultTelnyxLookupPhoneNumberID(ctx, phoneNumber)
	if err != nil {
		return err
	}
	if !ok || strings.TrimSpace(id) == "" {
		return nil
	}

	creds, err := secrets.TelnyxCreds(ctx, nil)
	if err != nil {
		return err
	}
	if strings.TrimSpace(creds.APIKey) == "" {
		return fmt.Errorf("telnyx api key missing")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, telnyxBaseURL+"/phone_numbers/"+url.PathEscape(id), nil)
	if err != nil {
		return err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("authorization", "Bearer "+strings.TrimSpace(creds.APIKey))

	client := &http.Client{Timeout: 10 * time.Second}
	//nolint:gosec // Request target is the fixed Telnyx HTTPS API host.
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("telnyx phone number delete: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	switch resp.StatusCode {
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("telnyx phone number delete: status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

type telnyxSendMessageRequest struct {
	From string `json:"from"`
	To   string `json:"to"`
	Text string `json:"text"`
	Type string `json:"type,omitempty"`
}

type telnyxSendMessageResponse struct {
	Data struct {
		ID string `json:"id"`
	} `json:"data"`
}

func defaultTelnyxSendSMS(ctx context.Context, from string, to string, text string) (string, error) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	text = strings.TrimSpace(text)
	if from == "" || to == "" || text == "" {
		return "", fmt.Errorf("telnyx sms requires from, to, and text")
	}

	creds, err := secrets.TelnyxCreds(ctx, nil)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(creds.APIKey) == "" {
		return "", fmt.Errorf("telnyx api key missing")
	}

	reqBody, err := json.Marshal(telnyxSendMessageRequest{
		From: from,
		To:   to,
		Text: text,
		Type: "SMS",
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, telnyxBaseURL+"/messages", bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+strings.TrimSpace(creds.APIKey))

	client := &http.Client{Timeout: 10 * time.Second}
	//nolint:gosec // Request target is the fixed Telnyx HTTPS API host.
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("telnyx sms send: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted:
		// ok
	default:
		return "", fmt.Errorf("telnyx sms send: status=%d body=%q", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed telnyxSendMessageResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("telnyx sms send: decode: %w", err)
	}
	return strings.TrimSpace(parsed.Data.ID), nil
}
