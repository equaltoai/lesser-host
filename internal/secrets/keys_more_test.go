package secrets

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

const migaduTestUsername = "aron@equal-to.ai"

func TestParseTelnyxCredentials(t *testing.T) {
	t.Parallel()

	t.Run("plain key", func(t *testing.T) {
		t.Parallel()

		got, err := parseTelnyxCredentials("  telnyx-key  ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.APIKey != "telnyx-key" {
			t.Fatalf("unexpected api key: %q", got.APIKey)
		}
	})

	t.Run("json payload", func(t *testing.T) {
		t.Parallel()

		got, err := parseTelnyxCredentials(`{"api_key":" key ","messaging_profile_id":" mp ","connection_id":" conn "}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.APIKey != "key" || got.MessagingProfileID != "mp" || got.ConnectionID != "conn" {
			t.Fatalf("unexpected creds: %#v", got)
		}
	})

	t.Run("errors", func(t *testing.T) {
		t.Parallel()

		if _, err := parseTelnyxCredentials(" "); err == nil {
			t.Fatalf("expected empty credentials error")
		}
		if _, err := parseTelnyxCredentials(`{"api_key":1}`); err == nil {
			t.Fatalf("expected missing api_key error")
		}
		if _, ok := parseAPIKeyValueFromJSON(`{"token":""}`); ok {
			t.Fatalf("expected false when json has no non-empty key")
		}
		if _, ok := parseAPIKeyValueFromJSON(`{"bad"`); ok {
			t.Fatalf("expected false for invalid json")
		}
	})
}

func TestParseMigaduCredentials(t *testing.T) {
	t.Run("plain key uses env username", func(t *testing.T) {
		t.Setenv("MIGADU_USERNAME", "  "+migaduTestUsername+" ")
		got, err := parseMigaduCredentials("  migadu-key  ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Username != migaduTestUsername || got.APIToken != "migadu-key" {
			t.Fatalf("unexpected creds: %#v", got)
		}
	})

	t.Run("json payload", func(t *testing.T) {
		t.Parallel()

		got, err := parseMigaduCredentials(`{"username":" ` + migaduTestUsername + ` ","token":" key "}`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Username != migaduTestUsername || got.APIToken != "key" {
			t.Fatalf("unexpected creds: %#v", got)
		}
	})

	t.Run("errors", func(t *testing.T) {
		t.Parallel()

		if _, err := parseMigaduCredentials(" "); err == nil {
			t.Fatalf("expected empty credentials error")
		}
		if _, err := parseMigaduCredentials(`{"username":"a@example.com"}`); err == nil {
			t.Fatalf("expected missing api key error")
		}
	})
}

func TestMigaduAndTelnyxLoaders(t *testing.T) {
	t.Parallel()

	clearParamCache()

	client := stubSSM{
		getParameter: func(_ context.Context, params *ssm.GetParameterInput) (*ssm.GetParameterOutput, error) {
			switch aws.ToString(params.Name) {
			case MigaduAPITokenSSMParameterName:
				return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String(`{"username":" ` + migaduTestUsername + ` ","token":" migadu-token "}`)}}, nil
			case TelnyxAPITokenSSMParameterName:
				return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: aws.String(`{"apiKey":" telnyx-token ","messagingProfileId":" mp-1 "}`)}}, nil
			default:
				t.Fatalf("unexpected parameter %q", aws.ToString(params.Name))
				return nil, nil
			}
		},
	}

	got, err := MigaduAPIToken(context.Background(), client)
	if err != nil || got != "migadu-token" {
		t.Fatalf("MigaduAPIToken: got=%q err=%v", got, err)
	}
	migaduCreds, err := MigaduCreds(context.Background(), client)
	if err != nil {
		t.Fatalf("MigaduCreds: %v", err)
	}
	if migaduCreds.Username != migaduTestUsername || migaduCreds.APIToken != "migadu-token" {
		t.Fatalf("unexpected migadu creds: %#v", migaduCreds)
	}

	creds, err := TelnyxCreds(context.Background(), client)
	if err != nil {
		t.Fatalf("TelnyxCreds: %v", err)
	}
	if creds.APIKey != "telnyx-token" || creds.MessagingProfileID != "mp-1" {
		t.Fatalf("unexpected creds: %#v", creds)
	}
}

func TestLoadFirstSSMParameterCached_ReturnsLastError(t *testing.T) {
	t.Parallel()

	clearParamCache()

	client := stubSSM{
		getParameter: func(_ context.Context, params *ssm.GetParameterInput) (*ssm.GetParameterOutput, error) {
			return nil, &ssmtypes.ParameterNotFound{}
		},
	}

	if _, err := loadFirstSSMParameterCached(context.Background(), client, []string{"/a", "/b"}, time.Minute); err == nil {
		t.Fatalf("expected error")
	}
}
