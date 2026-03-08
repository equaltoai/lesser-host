package controlplane

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/equaltoai/lesser-host/internal/secrets"
)

type depsStubSSM struct {
	value string
	err   error
}

func (s depsStubSSM) GetParameter(_ context.Context, _ *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &ssm.GetParameterOutput{
		Parameter: &ssmtypes.Parameter{Value: aws.String(s.value)},
	}, nil
}

type depsRoundTripFunc func(*http.Request) (*http.Response, error)

func (f depsRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func seedControlplaneSSMParam(t *testing.T, name string, value string) {
	t.Helper()
	_, err := secrets.GetSSMParameterCached(context.Background(), depsStubSSM{value: value}, name, time.Hour)
	if err != nil {
		t.Fatalf("seed parameter %q: %v", name, err)
	}
}

func rewriteDefaultTransport(t *testing.T, host string, target string) {
	t.Helper()

	targetURL, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse target url: %v", err)
	}

	prev := http.DefaultTransport
	http.DefaultTransport = depsRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		cloned := req.Clone(req.Context())
		if strings.EqualFold(cloned.URL.Host, host) {
			cloned.URL.Scheme = targetURL.Scheme
			cloned.URL.Host = targetURL.Host
		}
		return prev.RoundTrip(cloned)
	})
	t.Cleanup(func() {
		http.DefaultTransport = prev
	})
}

type telnyxHTTPTestState struct {
	sawSearch bool
	sawDelete bool
}

func newTelnyxHTTPTestServer(t *testing.T) (*httptest.Server, *telnyxHTTPTestState) {
	t.Helper()

	state := &telnyxHTTPTestState{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/available_phone_numbers":
			state.sawSearch = true
			assertTelnyxSearchRequest(t, r)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"phone_number": "  "},
					{"phone_number": "+15551234567"},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v2/number_orders":
			handleTelnyxNumberOrderRequest(t, w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/v2/phone_numbers":
			handleTelnyxPhoneLookupRequest(w, r)
		case r.Method == http.MethodDelete && r.URL.Path == "/v2/phone_numbers/num-1":
			state.sawDelete = true
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/v2/messages":
			handleTelnyxMessageRequest(t, w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	return server, state
}

func assertTelnyxSearchRequest(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.URL.Query().Get("filter[country_code]"); got != "US" {
		t.Fatalf("unexpected country code: %q", got)
	}
	if got := r.URL.Query().Get("filter[limit]"); got != "50" {
		t.Fatalf("unexpected limit: %q", got)
	}
	if got := r.Header.Get("authorization"); got != "Bearer telnyx-key" {
		t.Fatalf("unexpected auth header: %q", got)
	}
}

func handleTelnyxNumberOrderRequest(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var body telnyxCreateNumberOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode number order: %v", err)
	}
	if body.PhoneNumbers[0].PhoneNumber == "+15550000000" {
		w.WriteHeader(http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "order-1"}})
}

func handleTelnyxPhoneLookupRequest(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Query().Get("filter[phone_number]") {
	case "+15551234567":
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"id": "num-1"}}})
	case "+15557654321":
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{}})
	default:
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func handleTelnyxMessageRequest(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	var body telnyxSendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode sms request: %v", err)
	}
	if body.From != "+15551234567" || body.To != "+15557654321" || body.Text != "hello there" || body.Type != "SMS" {
		t.Fatalf("unexpected sms body: %#v", body)
	}
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": "msg-1"}})
}

type smtpCapture struct {
	from string
	rcpt []string
	data string
}

func startSMTPCaptureServer(t *testing.T, ln net.Listener, capture *smtpCapture) chan error {
	t.Helper()

	done := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		done <- serveSMTPConnection(conn, capture)
	}()
	return done
}

func serveSMTPConnection(conn net.Conn, capture *smtpCapture) error {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	writeLine := func(line string) error {
		if _, err := writer.WriteString(line + "\r\n"); err != nil {
			return err
		}
		return writer.Flush()
	}
	if err := writeLine("220 localhost ESMTP"); err != nil {
		return err
	}

	inData := false
	var dataLines []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			nextInData, dataErr := handleSMTPDataLine(line, &dataLines, capture, writeLine)
			if dataErr != nil {
				return dataErr
			}
			inData = nextInData
			continue
		}
		nextInData, done, cmdErr := handleSMTPCommand(line, capture, writeLine)
		if cmdErr != nil {
			return cmdErr
		}
		if done {
			return nil
		}
		inData = nextInData
	}
}

func handleSMTPDataLine(line string, dataLines *[]string, capture *smtpCapture, writeLine func(string) error) (bool, error) {
	if line == "." {
		capture.data = strings.Join(*dataLines, "\n")
		return false, writeLine("250 queued")
	}
	*dataLines = append(*dataLines, line)
	return true, nil
}

func handleSMTPCommand(line string, capture *smtpCapture, writeLine func(string) error) (bool, bool, error) {
	switch {
	case strings.HasPrefix(strings.ToUpper(line), "EHLO "):
		if err := writeLine("250-localhost"); err != nil {
			return false, false, err
		}
		return false, false, writeLine("250 OK")
	case strings.HasPrefix(strings.ToUpper(line), "MAIL FROM:"):
		capture.from = line
		return false, false, writeLine("250 OK")
	case strings.HasPrefix(strings.ToUpper(line), "RCPT TO:"):
		capture.rcpt = append(capture.rcpt, line)
		return false, false, writeLine("250 OK")
	case strings.EqualFold(line, "DATA"):
		return true, false, writeLine("354 End data with <CR><LF>.<CR><LF>")
	case strings.EqualFold(line, "QUIT"):
		return false, true, writeLine("221 Bye")
	default:
		return false, false, errors.New("unexpected smtp command: " + line)
	}
}

func TestDefaultTelnyxSearchAndOrderPhoneNumber(t *testing.T) {
	seedControlplaneSSMParam(t, secrets.TelnyxAPITokenSSMParameterName, `{"api_key":"telnyx-key","messaging_profile_id":"profile-1","connection_id":"conn-1"}`)

	server, state := newTelnyxHTTPTestServer(t)
	defer server.Close()

	rewriteDefaultTransport(t, "api.telnyx.com", server.URL)

	nums, err := defaultTelnyxSearchAvailablePhoneNumbers(context.Background(), "", 99)
	if err != nil {
		t.Fatalf("search numbers: %v", err)
	}
	if len(nums) != 1 || nums[0] != "+15551234567" || !state.sawSearch {
		t.Fatalf("unexpected search result: %#v", nums)
	}

	if _, validationErr := defaultTelnyxOrderPhoneNumber(context.Background(), ""); validationErr == nil {
		t.Fatalf("expected validation error for empty phone number")
	}
	orderID, err := defaultTelnyxOrderPhoneNumber(context.Background(), "+15551234567")
	if err != nil || orderID != "order-1" {
		t.Fatalf("order number: id=%q err=%v", orderID, err)
	}
	orderID, err = defaultTelnyxOrderPhoneNumber(context.Background(), "+15550000000")
	if err != nil || orderID != "" {
		t.Fatalf("expected conflict to be treated as success, got id=%q err=%v", orderID, err)
	}
}

func TestDefaultTelnyxLookupAndReleasePhoneNumber(t *testing.T) {
	seedControlplaneSSMParam(t, secrets.TelnyxAPITokenSSMParameterName, `{"api_key":"telnyx-key","messaging_profile_id":"profile-1","connection_id":"conn-1"}`)

	server, state := newTelnyxHTTPTestServer(t)
	defer server.Close()

	rewriteDefaultTransport(t, "api.telnyx.com", server.URL)

	id, ok, err := defaultTelnyxLookupPhoneNumberID(context.Background(), "+15551234567")
	if err != nil || !ok || id != "num-1" {
		t.Fatalf("lookup existing number: id=%q ok=%v err=%v", id, ok, err)
	}
	id, ok, err = defaultTelnyxLookupPhoneNumberID(context.Background(), "+15557654321")
	if err != nil || ok || id != "" {
		t.Fatalf("lookup missing number: id=%q ok=%v err=%v", id, ok, err)
	}

	releaseErr := defaultTelnyxReleasePhoneNumber(context.Background(), "+15551234567")
	if releaseErr != nil {
		t.Fatalf("release number: %v", releaseErr)
	}
	if !state.sawDelete {
		t.Fatalf("expected release delete call")
	}
	releaseMissingErr := defaultTelnyxReleasePhoneNumber(context.Background(), "+15557654321")
	if releaseMissingErr != nil {
		t.Fatalf("release missing number should be no-op: %v", releaseMissingErr)
	}
}

func TestDefaultTelnyxSendSMS(t *testing.T) {
	seedControlplaneSSMParam(t, secrets.TelnyxAPITokenSSMParameterName, `{"api_key":"telnyx-key","messaging_profile_id":"profile-1","connection_id":"conn-1"}`)

	server, _ := newTelnyxHTTPTestServer(t)
	defer server.Close()

	rewriteDefaultTransport(t, "api.telnyx.com", server.URL)

	if _, sendValidationErr := defaultTelnyxSendSMS(context.Background(), "", "+15557654321", "hello"); sendValidationErr == nil {
		t.Fatalf("expected validation error for empty sender")
	}
	msgID, err := defaultTelnyxSendSMS(context.Background(), "+15551234567", "+15557654321", " hello there ")
	if err != nil || msgID != "msg-1" {
		t.Fatalf("send sms: id=%q err=%v", msgID, err)
	}
}

func TestDefaultMigaduCreateMailbox_SuccessConflictAndErrors(t *testing.T) {
	seedControlplaneSSMParam(t, secrets.MigaduAPITokenSSMParameterName, `{"username":"aron@equal-to.ai","token":"migadu-token"}`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/domains/lessersoul.ai/mailboxes" {
			http.NotFound(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "aron@equal-to.ai" || pass != "migadu-token" {
			t.Fatalf("unexpected basic auth: user=%q ok=%v", user, ok)
		}

		var body migaduCreateMailboxRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode mailbox request: %v", err)
		}
		switch body.LocalPart {
		case "exists":
			w.WriteHeader(http.StatusConflict)
		case "broken":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("nope"))
		default:
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer server.Close()

	rewriteDefaultTransport(t, "api.migadu.com", server.URL)

	if err := defaultMigaduCreateMailbox(context.Background(), "", "Agent", "pw"); err == nil {
		t.Fatalf("expected validation error for empty local part")
	}
	if err := defaultMigaduCreateMailbox(context.Background(), "agent", "", "pw"); err != nil {
		t.Fatalf("create mailbox: %v", err)
	}
	if err := defaultMigaduCreateMailbox(context.Background(), "exists", "Agent", "pw"); err != nil {
		t.Fatalf("expected conflict to be treated as success: %v", err)
	}
	if err := defaultMigaduCreateMailbox(context.Background(), "broken", "Agent", "pw"); err == nil {
		t.Fatalf("expected server error")
	}
}

func TestDefaultMigaduSendSMTP_Validation(t *testing.T) {
	if err := defaultMigaduSendSMTP(context.Background(), "", "pw", "from@example.com", []string{"to@example.com"}, []byte("x")); err == nil {
		t.Fatalf("expected validation error for empty username")
	}
}

func TestDefaultMigaduSendSMTP_Success(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen smtp: %v", err)
	}
	defer ln.Close()

	host, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split host/port: %v", err)
	}
	t.Setenv("MIGADU_SMTP_HOST", host)
	t.Setenv("MIGADU_SMTP_PORT", port)

	capture := &smtpCapture{}
	done := startSMTPCaptureServer(t, ln, capture)

	msg := []byte("Subject: hi\r\n\r\nbody")
	if err := defaultMigaduSendSMTP(context.Background(), "user", "pw", "from@example.com", []string{"to@example.com", " "}, msg); err != nil {
		t.Fatalf("send smtp: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("smtp server error: %v", err)
	}
	if !strings.Contains(capture.from, "from@example.com") {
		t.Fatalf("unexpected mail from command: %q", capture.from)
	}
	if len(capture.rcpt) != 1 || !strings.Contains(capture.rcpt[0], "to@example.com") {
		t.Fatalf("unexpected rcpt commands: %#v", capture.rcpt)
	}
	if !strings.Contains(capture.data, "Subject: hi") || !strings.Contains(capture.data, "body") {
		t.Fatalf("unexpected smtp data: %q", capture.data)
	}
}
