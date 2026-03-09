package controlplane

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	theoryErrors "github.com/theory-cloud/tabletheory/pkg/errors"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/commworker"
	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

const (
	commVoiceReplyBody      = "Yes, please call back tomorrow."
	commVoiceReplyMessageID = "comm-msg-1-reply-call-1"
)

func voiceInstructionFixture() models.SoulCommVoiceInstruction {
	return models.SoulCommVoiceInstruction{
		MessageID: "comm-msg-1",
		AgentID:   "0xabc",
		From:      "+15550142",
		To:        "+15550143",
		Body:      "Hello from the soul",
		Voice:     telnyxDefaultOutboundVoice,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

func newVoiceGatherCaptureServer(t *testing.T) (*Server, func() *commworker.QueueMessage) {
	t.Helper()

	db := ttmocks.NewMockExtendedDB()
	qVoice := new(ttmocks.MockQuery)
	qStatus := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommVoiceInstruction")).Return(qVoice).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(qStatus).Run(func(args mock.Arguments) {
		if item := testutil.RequireMockArg[*models.SoulCommMessageStatus](t, args, 0); strings.TrimSpace(item.MessageID) == "comm-msg-1" && strings.TrimSpace(item.ReplyBody) != "" {
			if item.ReplyMessageID != commVoiceReplyMessageID {
				t.Fatalf("unexpected reply message id: %#v", item)
			}
			if item.ReplyBody != commVoiceReplyBody {
				t.Fatalf("unexpected reply body: %#v", item)
			}
			if item.ReplyConfidence == nil || *item.ReplyConfidence != 0.83 {
				t.Fatalf("unexpected reply confidence: %#v", item.ReplyConfidence)
			}
			if item.Status != models.SoulCommMessageStatusSent {
				t.Fatalf("expected status sent, got %#v", item)
			}
		}
	}).Maybe()
	qVoice.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qVoice).Maybe()
	qStatus.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qStatus).Maybe()
	qStatus.On("IfExists").Return(qStatus).Maybe()
	qStatus.On("Update", mock.Anything).Return(nil).Once()
	qVoice.On("First", mock.AnythingOfType("*models.SoulCommVoiceInstruction")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulCommVoiceInstruction](t, args, 0)
		*dest = voiceInstructionFixture()
	}).Once()
	qStatus.On("First", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulCommMessageStatus](t, args, 0)
		*dest = models.SoulCommMessageStatus{
			MessageID: "comm-msg-1",
			AgentID:   "0xabc",
			To:        "+15550143",
			Status:    models.SoulCommMessageStatusAccepted,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
	}).Once()

	var queued *commworker.QueueMessage
	s := &Server{
		store: store.New(db),
		cfg:   config.Config{SoulEnabled: true},
		enqueueCommMessage: func(_ context.Context, msg commworker.QueueMessage) error {
			cp := msg
			queued = &cp
			return nil
		},
	}
	return s, func() *commworker.QueueMessage { return queued }
}

func TestNormalizeSoulCommPublicBaseURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "https host", input: " https://lab.lesser.host/path?q=1 ", want: "https://lab.lesser.host"},
		{name: "http localhost", input: "http://localhost:5173/portal", want: "http://localhost:5173"},
		{name: "invalid scheme", input: "ftp://lab.lesser.host", want: ""},
		{name: "invalid host", input: "https://user@lab.lesser.host", want: ""},
		{name: "blank", input: "  ", want: ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeSoulCommPublicBaseURL(tc.input); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestSafeSoulCommHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host string
		want bool
	}{
		{host: "lab.lesser.host", want: true},
		{host: "localhost:5173", want: true},
		{host: "bad host", want: false},
		{host: "bad/host", want: false},
		{host: "user@lab.lesser.host", want: false},
		{host: "", want: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.host, func(t *testing.T) {
			t.Parallel()
			if got := safeSoulCommHost(tc.host); got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestSoulCommRequestBaseURL(t *testing.T) {
	t.Parallel()

	t.Run("prefers configured public base url", func(t *testing.T) {
		t.Parallel()
		ctx := &apptheory.Context{
			Request: apptheory.Request{Headers: map[string][]string{"host": {"ignored.example"}}},
		}
		if got := soulCommRequestBaseURL(ctx, "https://lab.lesser.host/path"); got != "https://lab.lesser.host" {
			t.Fatalf("expected configured base url, got %q", got)
		}
	})

	t.Run("falls back to request host", func(t *testing.T) {
		t.Parallel()
		ctx := &apptheory.Context{
			Request: apptheory.Request{Headers: map[string][]string{"host": {"portal.lesser.host"}}},
		}
		if got := soulCommRequestBaseURL(ctx, ""); got != "https://portal.lesser.host" {
			t.Fatalf("expected request host fallback, got %q", got)
		}
	})

	t.Run("rejects unsafe request host", func(t *testing.T) {
		t.Parallel()
		ctx := &apptheory.Context{
			Request: apptheory.Request{Headers: map[string][]string{"host": {"bad host"}}},
		}
		if got := soulCommRequestBaseURL(ctx, ""); got != "" {
			t.Fatalf("expected blank base url, got %q", got)
		}
	})
}

func TestBuildSoulCommVoiceTeXML(t *testing.T) {
	t.Parallel()

	t.Run("defaults and escaping", func(t *testing.T) {
		t.Parallel()
		payload, err := buildSoulCommVoiceTeXML(" hello <world> & friends ", "", "https://lab.lesser.host/webhooks/comm/voice/gather/comm-msg-1")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		got := string(payload)
		if !strings.Contains(got, `<Gather input="speech" action="https://lab.lesser.host/webhooks/comm/voice/gather/comm-msg-1" method="POST" timeout="5" speechTimeout="auto">`) {
			t.Fatalf("expected gather action, got %q", got)
		}
		if !strings.Contains(got, `hello &lt;world&gt; &amp; friends`) {
			t.Fatalf("expected escaped body, got %q", got)
		}
		if !strings.Contains(got, telnyxDefaultOutboundReplyPrompt) {
			t.Fatalf("expected reply prompt, got %q", got)
		}
	})

	t.Run("blank body uses placeholder", func(t *testing.T) {
		t.Parallel()
		payload, err := buildSoulCommVoiceTeXML("   ", "Polly.Joanna-Neural", "https://lab.lesser.host/webhooks/comm/voice/gather/comm-msg-1")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !strings.Contains(string(payload), `No message provided.`) {
			t.Fatalf("expected placeholder body, got %q", string(payload))
		}
		if !strings.Contains(string(payload), `voice="Polly.Joanna-Neural"`) {
			t.Fatalf("expected custom voice, got %q", string(payload))
		}
	})
}

func TestHandleCommVoiceTeXML_Disabled(t *testing.T) {
	t.Parallel()

	s := &Server{store: store.New(ttmocks.NewMockExtendedDB()), cfg: config.Config{SoulEnabled: false}}
	resp, err := s.handleCommVoiceTeXML(&apptheory.Context{Params: map[string]string{"messageId": "comm-msg-1"}})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.Status)
	}
}

func TestHandleCommVoiceTeXML_MissingMessageID(t *testing.T) {
	t.Parallel()

	s := &Server{store: store.New(ttmocks.NewMockExtendedDB()), cfg: config.Config{SoulEnabled: true}}
	_, err := s.handleCommVoiceTeXML(&apptheory.Context{})
	appErr := requireAppError(t, err)
	if appErr.Code != appErrCodeBadRequest || appErr.Message != "messageId is required" {
		t.Fatalf("unexpected error: %#v", appErr)
	}
}

func TestHandleCommVoiceTeXML_NotFound(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qVoice := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommVoiceInstruction")).Return(qVoice).Maybe()
	qVoice.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qVoice).Maybe()
	qVoice.On("First", mock.AnythingOfType("*models.SoulCommVoiceInstruction")).Return(theoryErrors.ErrItemNotFound).Once()

	s := &Server{store: store.New(db), cfg: config.Config{SoulEnabled: true}}
	_, err := s.handleCommVoiceTeXML(&apptheory.Context{Params: map[string]string{"messageId": "comm-msg-1"}})
	appErr := requireCommTheoryError(t, err)
	if appErr.Code != appErrCodeNotFound || appErr.StatusCode != http.StatusNotFound {
		t.Fatalf("unexpected error: %#v", appErr)
	}
}

func TestHandleCommVoiceTeXML_Success(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qVoice := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommVoiceInstruction")).Return(qVoice).Maybe()
	qVoice.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qVoice).Maybe()
	qVoice.On("First", mock.AnythingOfType("*models.SoulCommVoiceInstruction")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulCommVoiceInstruction](t, args, 0)
		*dest = models.SoulCommVoiceInstruction{
			MessageID: "comm-msg-1",
			AgentID:   "0xabc",
			From:      "+15550142",
			To:        "+15550143",
			Body:      "Voice <test>",
			Voice:     "Polly.Joanna-Neural",
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
	}).Once()

	s := &Server{store: store.New(db), cfg: config.Config{SoulEnabled: true, PublicBaseURL: "https://lab.lesser.host"}}
	resp, err := s.handleCommVoiceTeXML(&apptheory.Context{
		Params:  map[string]string{"messageId": "comm-msg-1"},
		Request: apptheory.Request{Headers: map[string][]string{"host": {"ignored.example"}}},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
	if got := resp.Headers["content-type"]; len(got) != 1 || got[0] != "application/xml; charset=utf-8" {
		t.Fatalf("unexpected headers: %#v", resp.Headers)
	}
	if !strings.Contains(string(resp.Body), `voice="Polly.Joanna-Neural"`) || !strings.Contains(string(resp.Body), `Voice &lt;test&gt;`) {
		t.Fatalf("unexpected xml body: %q", string(resp.Body))
	}
	if !strings.Contains(string(resp.Body), `/webhooks/comm/voice/gather/comm-msg-1`) {
		t.Fatalf("expected gather action in xml body: %q", string(resp.Body))
	}
}

func TestHandleCommVoiceGatherWebhook_CapturesSpeechReplyAndUpdatesStatus(t *testing.T) {
	t.Parallel()

	s, getQueued := newVoiceGatherCaptureServer(t)

	body, err := json.Marshal(map[string]any{
		"CallSid":      "call-1",
		"From":         "+15550142",
		"To":           "+15550143",
		"SpeechResult": commVoiceReplyBody,
		"Confidence":   "0.83",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp, callErr := s.handleCommVoiceGatherWebhook(&apptheory.Context{
		Params:  map[string]string{"messageId": "comm-msg-1"},
		Request: apptheory.Request{Body: body},
	})
	if callErr != nil {
		t.Fatalf("unexpected err: %v", callErr)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Status)
	}
	queued := getQueued()
	if queued == nil {
		t.Fatalf("expected queued reply notification")
	}
	if queued.Notification.Channel != commChannelVoice || queued.Notification.Body != commVoiceReplyBody {
		t.Fatalf("unexpected queued notification: %#v", queued.Notification)
	}
	if queued.Notification.From.Number != "+15550143" || queued.Notification.To == nil || queued.Notification.To.Number != "+15550142" {
		t.Fatalf("unexpected notification routing: %#v", queued.Notification)
	}
	if queued.Notification.InReplyTo == nil || *queued.Notification.InReplyTo != "comm-msg-1" {
		t.Fatalf("expected inReplyTo comm-msg-1, got %#v", queued.Notification.InReplyTo)
	}
}

func TestHandleCommVoiceGatherWebhook_EmptyGatherResultAcknowledges(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qVoice := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.SoulCommVoiceInstruction")).Return(qVoice).Maybe()
	qVoice.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qVoice).Maybe()
	qVoice.On("First", mock.AnythingOfType("*models.SoulCommVoiceInstruction")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.SoulCommVoiceInstruction](t, args, 0)
		*dest = voiceInstructionFixture()
	}).Once()

	s := &Server{
		store: store.New(db),
		cfg:   config.Config{SoulEnabled: true},
		enqueueCommMessage: func(_ context.Context, msg commworker.QueueMessage) error {
			t.Fatalf("enqueue should not be called for empty gather result")
			return nil
		},
	}

	body, err := json.Marshal(map[string]any{
		"CallSid": "call-1",
		"From":    "+15550142",
		"To":      "+15550143",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp, callErr := s.handleCommVoiceGatherWebhook(&apptheory.Context{
		Params:  map[string]string{"messageId": "comm-msg-1"},
		Request: apptheory.Request{Body: body},
	})
	if callErr != nil {
		t.Fatalf("unexpected err: %v", callErr)
	}
	if resp.Status != http.StatusOK || !strings.Contains(string(resp.Body), `"captured":false`) {
		t.Fatalf("unexpected response: %#v body=%s", resp, string(resp.Body))
	}
}

func TestMaybeHandleOutboundVoiceStatusWebhook(t *testing.T) {
	t.Parallel()

	t.Run("no message id falls through", func(t *testing.T) {
		t.Parallel()
		resp, handled, err := (&Server{}).maybeHandleOutboundVoiceStatusWebhook(&apptheory.Context{})
		if err != nil || handled || resp != nil {
			t.Fatalf("expected no-op, got resp=%#v handled=%v err=%v", resp, handled, err)
		}
	})

	t.Run("invalid payload", func(t *testing.T) {
		t.Parallel()
		db := ttmocks.NewMockExtendedDB()
		s := &Server{store: store.New(db)}
		_, handled, err := s.maybeHandleOutboundVoiceStatusWebhook(&apptheory.Context{
			Params:  map[string]string{"messageId": "comm-msg-1"},
			Request: apptheory.Request{Body: []byte("{")},
		})
		if !handled {
			t.Fatalf("expected handled=true")
		}
		appErr := requireAppError(t, err)
		if appErr.Code != appErrCodeBadRequest || appErr.Message != "invalid webhook payload" {
			t.Fatalf("unexpected error: %#v", appErr)
		}
	})

	t.Run("accepted status transitions to sent", func(t *testing.T) {
		t.Parallel()
		db := ttmocks.NewMockExtendedDB()
		qStatus := new(ttmocks.MockQuery)
		db.On("WithContext", mock.Anything).Return(db).Maybe()
		db.On("Model", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(qStatus).Maybe()
		qStatus.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(qStatus).Maybe()
		qStatus.On("IfExists").Return(qStatus).Maybe()
		qStatus.On("Update", mock.Anything).Return(nil).Once()
		qStatus.On("First", mock.AnythingOfType("*models.SoulCommMessageStatus")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.SoulCommMessageStatus](t, args, 0)
			*dest = models.SoulCommMessageStatus{
				MessageID: "comm-msg-1",
				AgentID:   "0xabc",
				To:        "+15550143",
				Status:    models.SoulCommMessageStatusAccepted,
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			}
		}).Once()

		body, err := json.Marshal(map[string]any{
			"data": map[string]any{
				"event_type": "call.answered",
				"payload": map[string]any{
					"call_control_id": "call-control-1",
				},
			},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		s := &Server{store: store.New(db)}
		resp, handled, callErr := s.maybeHandleOutboundVoiceStatusWebhook(&apptheory.Context{
			Params:  map[string]string{"messageId": "comm-msg-1"},
			Request: apptheory.Request{Body: body},
		})
		if callErr != nil {
			t.Fatalf("unexpected err: %v", callErr)
		}
		if !handled {
			t.Fatalf("expected handled=true")
		}
		if resp == nil || resp.Status != http.StatusOK {
			t.Fatalf("expected 200 response, got %#v", resp)
		}
	})
}

func TestMapTelnyxVoiceEventToSoulStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		eventType     string
		duration      int64
		currentStatus string
		wantStatus    string
		wantCode      string
		wantUpdate    bool
	}{
		{name: "answered becomes sent", eventType: "call.answered", wantStatus: models.SoulCommMessageStatusSent, wantUpdate: true},
		{name: "ended with duration stays sent", eventType: "call.ended", duration: 12, wantStatus: models.SoulCommMessageStatusSent, wantUpdate: true},
		{name: "hangup before delivery fails", eventType: "call.hangup", wantStatus: models.SoulCommMessageStatusFailed, wantCode: "call.hangup", wantUpdate: true},
		{name: "failure after sent ignored", eventType: "call.failed", currentStatus: models.SoulCommMessageStatusSent, wantUpdate: false},
		{name: "unknown event ignored", eventType: "call.ringing", wantUpdate: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotStatus, gotCode, _, gotUpdate := mapTelnyxVoiceEventToSoulStatus(tc.eventType, tc.duration, tc.currentStatus)
			if gotStatus != tc.wantStatus || gotCode != tc.wantCode || gotUpdate != tc.wantUpdate {
				t.Fatalf("expected status=%q code=%q update=%v, got status=%q code=%q update=%v", tc.wantStatus, tc.wantCode, tc.wantUpdate, gotStatus, gotCode, gotUpdate)
			}
		})
	}
}
