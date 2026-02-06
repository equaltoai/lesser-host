package controlplane

import (
	"testing"

	apptheory "github.com/theory-cloud/apptheory/runtime"
)

func TestBuildInstanceConfigUpdate(t *testing.T) {
	t.Parallel()

	_, _, err := buildInstanceConfigUpdate("slug", updateInstanceConfigRequest{})
	if err == nil {
		t.Fatalf("expected error for empty request")
	}

	rp := "nope"
	_, _, err = buildInstanceConfigUpdate("slug", updateInstanceConfigRequest{RenderPolicy: &rp})
	if err == nil {
		t.Fatalf("expected error for invalid render policy")
	}
	if appErr, ok := err.(*apptheory.AppError); !ok || appErr.Code != "app.bad_request" {
		t.Fatalf("expected bad_request app error, got %#v", err)
	}

	op := "allow"
	mt := "virality"
	ms := "openai:test"
	bm := "worker"
	maxItems := int64(2)
	maxBytes := int64(100)
	mult := int64(11000)
	inflight := int64(10)
	enabled := true

	update, fields, err := buildInstanceConfigUpdate("slug", updateInstanceConfigRequest{
		OveragePolicy:          &op,
		ModerationTrigger:      &mt,
		AIModelSet:             &ms,
		AIBatchingMode:         &bm,
		AIBatchMaxItems:        &maxItems,
		AIBatchMaxTotalBytes:   &maxBytes,
		AIPricingMultiplierBps: &mult,
		AIMaxInflightJobs:      &inflight,
		AIEnabled:              &enabled,
	})
	if err != nil {
		t.Fatalf("buildInstanceConfigUpdate: %v", err)
	}
	if update == nil || update.Slug != "slug" {
		t.Fatalf("unexpected update: %#v", update)
	}
	if len(fields) == 0 {
		t.Fatalf("expected fields list")
	}
	if update.OveragePolicy != "allow" || update.ModerationTrigger != "virality" {
		t.Fatalf("unexpected policy fields: %#v", update)
	}
	if update.AIModelSet != "openai:test" || update.AIBatchingMode != "worker" {
		t.Fatalf("unexpected ai fields: %#v", update)
	}
	if update.AIBatchMaxItems != 2 || update.AIBatchMaxTotalBytes != 100 {
		t.Fatalf("unexpected ai bounds: %#v", update)
	}
	if update.AIPricingMultiplierBps == nil || *update.AIPricingMultiplierBps != 11000 {
		t.Fatalf("unexpected multiplier: %#v", update.AIPricingMultiplierBps)
	}
	if update.AIMaxInflightJobs == nil || *update.AIMaxInflightJobs != 10 {
		t.Fatalf("unexpected inflight: %#v", update.AIMaxInflightJobs)
	}
}

