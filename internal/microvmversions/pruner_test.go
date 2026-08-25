package microvmversions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubClient is a scripted Client. Successful deletes remove the version from
// the stub's list, so the post-prune re-list reflects reality the way the
// service behaves after the live prune (only the kept versions remain listed).
// The stub records call order, tracks in-flight delete concurrency, and can
// inject conflicts or terminal errors per version.
type stubClient struct {
	found   bool
	image   Image
	mu      sync.Mutex
	version []Version

	listCalls int
	listErr   error // returned when listCalls >= listErrOn
	listErrOn int   // 0 = never

	failures map[string]*deleteBehavior

	deleteOrder []string
	inFlight    int32
	maxInFlight int32
}

type deleteBehavior struct {
	conflicts int // ErrConflict to return before success; -1 = always conflict
	terminal  error
}

func newStub(versions []Version) *stubClient {
	return &stubClient{
		found: true,
		image: Image{
			Name: "lesser-host-lab_hosted_genesis",
			ARN:  "arn:aws:lambda-microvms:us-east-1:123456789012:image/lesser-host-lab_hosted_genesis",
		},
		version:  append([]Version(nil), versions...),
		failures: map[string]*deleteBehavior{},
	}
}

func (s *stubClient) ResolveImage(_ context.Context, name string) (Image, error) {
	if !s.found {
		return Image{}, ErrImageNotFound
	}
	return s.image, nil
}

func (s *stubClient) ListVersions(_ context.Context, _ string) ([]Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listCalls++
	if s.listErr != nil && s.listCalls >= s.listErrOn {
		return nil, s.listErr
	}
	return append([]Version(nil), s.version...), nil
}

func (s *stubClient) DeleteVersion(_ context.Context, _, imageVersion string) error {
	cur := atomic.AddInt32(&s.inFlight, 1)
	if cur > atomic.LoadInt32(&s.maxInFlight) {
		atomic.StoreInt32(&s.maxInFlight, cur)
	}
	defer atomic.AddInt32(&s.inFlight, -1)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteOrder = append(s.deleteOrder, imageVersion)

	if behavior := s.failures[imageVersion]; behavior != nil {
		if behavior.terminal != nil {
			return behavior.terminal
		}
		if behavior.conflicts > 0 {
			behavior.conflicts--
			return fmt.Errorf("%w: injected conflict for %s", ErrConflict, imageVersion)
		}
		if behavior.conflicts < 0 {
			return fmt.Errorf("%w: persistent conflict for %s", ErrConflict, imageVersion)
		}
	}
	for i, v := range s.version {
		if v.ImageVersion == imageVersion {
			s.version = append(s.version[:i], s.version[i+1:]...)
			break
		}
	}
	return nil
}

// versions builds n SUCCESSFUL versions v1..vn with increasing CreatedAt
// (v1 oldest, vn newest).
func versions(n int) []Version {
	vs := make([]Version, 0, n)
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= n; i++ {
		vs = append(vs, Version{
			ImageVersion: fmt.Sprintf("v%d", i),
			CreatedAt:    base.Add(time.Duration(i) * time.Hour),
			State:        "SUCCESSFUL",
			Status:       "ACTIVE",
		})
	}
	return vs
}

// sleepRecorder records settle/backoff sleeps without actually waiting.
type sleepRecorder struct {
	mu     sync.Mutex
	sleeps []time.Duration
}

func (r *sleepRecorder) sleep(_ context.Context, d time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sleeps = append(r.sleeps, d)
	return nil
}

func (r *sleepRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sleeps)
}

func TestPruneKeepsNewestNDeletesOldestFirst(t *testing.T) {
	s := newStub(versions(10))
	rec := &sleepRecorder{}
	p := NewPruner(s, WithSleep(rec.sleep))

	res, err := p.Prune(context.Background(), s.image.Name)
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}
	if res.Deleted != 5 || res.DeleteFailed != 0 {
		t.Fatalf("expected 5 deletions, got deleted=%d failed=%d", res.Deleted, res.DeleteFailed)
	}
	wantOrder := []string{"v1", "v2", "v3", "v4", "v5"}
	if !equalStrings(s.deleteOrder, wantOrder) {
		t.Fatalf("delete order = %v, want %v", s.deleteOrder, wantOrder)
	}
	if res.RemainingCount != 5 {
		t.Fatalf("remaining = %d, want 5", res.RemainingCount)
	}
	if res.Kept != 5 {
		t.Fatalf("kept = %d, want 5", res.Kept)
	}
	if rec.count() != 4 { // settle-waits between the 5 serialized deletes
		t.Fatalf("settle sleeps = %d, want 4", rec.count())
	}
}

func TestPruneNeverDeletesActiveVersion(t *testing.T) {
	s := newStub(versions(10))
	s.image.LatestActiveImageVersion = "v2" // older than the newest 5
	p := NewPruner(s, WithSleep((&sleepRecorder{}).sleep))

	res, err := p.Prune(context.Background(), s.image.Name)
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}
	wantOrder := []string{"v1", "v3", "v4", "v5"}
	if !equalStrings(s.deleteOrder, wantOrder) {
		t.Fatalf("delete order = %v, want %v (active v2 must survive)", s.deleteOrder, wantOrder)
	}
	if res.RemainingCount != 6 { // v2 + newest 5 (v6..v10)
		t.Fatalf("remaining = %d, want 6", res.RemainingCount)
	}
}

func TestPruneSkipsUnsettledAndGoneStates(t *testing.T) {
	vs := []Version{
		{ImageVersion: "v1", CreatedAt: t1(1), State: "SUCCESSFUL"},
		{ImageVersion: "v2", CreatedAt: t1(2), State: "SUCCESSFUL"},
		{ImageVersion: "v3", CreatedAt: t1(3), State: "SUCCESSFUL"},
		{ImageVersion: "v4", CreatedAt: t1(4), State: "SUCCESSFUL"},
		{ImageVersion: "v5", CreatedAt: t1(5), State: "PENDING"},
		{ImageVersion: "v6", CreatedAt: t1(6), State: "IN_PROGRESS"},
		{ImageVersion: "v7", CreatedAt: t1(7), State: "DELETING"},
		{ImageVersion: "v8", CreatedAt: t1(8), State: "DELETED"},
	}
	s := newStub(vs)
	p := NewPruner(s, WithSleep((&sleepRecorder{}).sleep))

	res, err := p.Prune(context.Background(), s.image.Name)
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}
	wantOrder := []string{"v1", "v2", "v3"}
	if !equalStrings(s.deleteOrder, wantOrder) {
		t.Fatalf("delete order = %v, want %v", s.deleteOrder, wantOrder)
	}
	if res.RemainingCount != 4 { // v4 (newest-5) + v5..v7 (unsettled); v8 excluded (DELETED)
		t.Fatalf("remaining = %d, want 4", res.RemainingCount)
	}
}

func TestPruneConflictRetrySucceeds(t *testing.T) {
	s := newStub(versions(10))
	s.failures["v1"] = &deleteBehavior{conflicts: 2}
	s.failures["v2"] = &deleteBehavior{conflicts: 1}
	rec := &sleepRecorder{}
	p := NewPruner(s, WithSleep(rec.sleep))

	res, err := p.Prune(context.Background(), s.image.Name)
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}
	if res.Deleted != 5 || res.DeleteFailed != 0 {
		t.Fatalf("expected all 5 deletes to succeed, got deleted=%d failed=%d", res.Deleted, res.DeleteFailed)
	}
	wantOrder := []string{"v1", "v1", "v1", "v2", "v2", "v3", "v4", "v5"}
	if !equalStrings(s.deleteOrder, wantOrder) {
		t.Fatalf("delete order = %v, want %v", s.deleteOrder, wantOrder)
	}
	// sleeps: v1 backoff (5s), v1 backoff (10s), settle after v1, v2 backoff
	// (5s), settle after v2, settle after v3, settle after v4 = 7
	if rec.count() != 7 {
		t.Fatalf("sleep count = %d, want 7", rec.count())
	}
	sleeps := rec.sleeps
	if sleeps[0] != 5*time.Second || sleeps[1] != 10*time.Second {
		t.Fatalf("conflict backoff = %v, %v; want 5s, 10s", sleeps[0], sleeps[1])
	}
}

func TestPruneFailClosedWhenStillAtCap(t *testing.T) {
	s := newStub(versions(50))
	for i := 1; i <= 50; i++ {
		s.failures[fmt.Sprintf("v%d", i)] = &deleteBehavior{conflicts: -1} // persistent conflict
	}
	p := NewPruner(s, WithSleep((&sleepRecorder{}).sleep))

	res, err := p.Prune(context.Background(), s.image.Name)
	if err == nil {
		t.Fatal("expected fail-closed error at/over the 50-version cap, got nil")
	}
	if !strings.Contains(err.Error(), "refusing to proceed") {
		t.Fatalf("error should refuse the deploy: %v", err)
	}
	if res.DeleteFailed != 45 {
		t.Fatalf("delete failures = %d, want 45", res.DeleteFailed)
	}
	if res.Deleted != 0 {
		t.Fatalf("deleted = %d, want 0", res.Deleted)
	}
	if res.RemainingCount != 50 {
		t.Fatalf("remaining = %d, want 50", res.RemainingCount)
	}
}

func TestPruneFailOpenOnHeadroom(t *testing.T) {
	s := newStub(versions(30))
	s.failures["v1"] = &deleteBehavior{terminal: errors.New("permanent failure")}
	p := NewPruner(s, WithSleep((&sleepRecorder{}).sleep))

	res, err := p.Prune(context.Background(), s.image.Name)
	if err != nil {
		t.Fatalf("expected fail-open (no error) with headroom, got: %v", err)
	}
	if res.DeleteFailed != 1 || res.Deleted != 24 {
		t.Fatalf("deleted=%d failed=%d, want 24/1", res.Deleted, res.DeleteFailed)
	}
	if res.RemainingCount != 6 { // v1 (failed delete) + newest 5
		t.Fatalf("remaining = %d, want 6", res.RemainingCount)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "headroom") {
		t.Fatalf("expected one headroom warning, got %v", res.Warnings)
	}
}

func TestPruneUnderKeepNNoop(t *testing.T) {
	s := newStub(versions(3))
	p := NewPruner(s, WithSleep((&sleepRecorder{}).sleep))

	res, err := p.Prune(context.Background(), s.image.Name)
	if err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}
	if len(s.deleteOrder) != 0 {
		t.Fatalf("expected no deletes, got %v", s.deleteOrder)
	}
	if res.RemainingCount != 3 || res.Listed != 3 {
		t.Fatalf("listed=%d remaining=%d, want 3/3", res.Listed, res.RemainingCount)
	}
}

func TestPruneImageNotFoundNoop(t *testing.T) {
	s := newStub(nil)
	s.found = false
	p := NewPruner(s, WithSleep((&sleepRecorder{}).sleep))

	res, err := p.Prune(context.Background(), "lesser-host-lab_hosted_genesis")
	if err != nil {
		t.Fatalf("expected no-op on missing image, got: %v", err)
	}
	if len(s.deleteOrder) != 0 || res.Deleted != 0 {
		t.Fatalf("expected no deletes for a missing image")
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "not found") {
		t.Fatalf("expected image-not-found warning, got %v", res.Warnings)
	}
}

func TestPruneListFailureFailsClosed(t *testing.T) {
	s := newStub(versions(10))
	s.listErr = errors.New("list down")
	s.listErrOn = 1
	p := NewPruner(s, WithSleep((&sleepRecorder{}).sleep))

	_, err := p.Prune(context.Background(), s.image.Name)
	if err == nil {
		t.Fatal("expected fail-closed on list failure, got nil")
	}
}

func TestPruneReListFailureFailsClosed(t *testing.T) {
	s := newStub(versions(10))
	s.listErr = errors.New("re-list down")
	s.listErrOn = 2 // second list call (post-prune confirmation) fails
	p := NewPruner(s, WithSleep((&sleepRecorder{}).sleep))

	res, err := p.Prune(context.Background(), s.image.Name)
	if err == nil {
		t.Fatal("expected fail-closed when headroom cannot be confirmed, got nil")
	}
	if res.Deleted != 5 {
		t.Fatalf("deletes should have happened before the re-list failure, got %d", res.Deleted)
	}
}

func TestPruneStuckStatesAtCapFailClosed(t *testing.T) {
	vs := make([]Version, 0, 50)
	for i := 1; i <= 45; i++ {
		vs = append(vs, Version{ImageVersion: fmt.Sprintf("v%d", i), CreatedAt: t1(i), State: "IN_PROGRESS"})
	}
	for i := 46; i <= 50; i++ {
		vs = append(vs, Version{ImageVersion: fmt.Sprintf("v%d", i), CreatedAt: t1(i), State: "SUCCESSFUL"})
	}
	s := newStub(vs)
	p := NewPruner(s, WithSleep((&sleepRecorder{}).sleep))

	res, err := p.Prune(context.Background(), s.image.Name)
	if err == nil {
		t.Fatal("expected fail-closed: 50 versions with nothing deletable, got nil")
	}
	if res.TargetDeletes != 0 {
		t.Fatalf("target deletes = %d, want 0 (stuck states are not deletable)", res.TargetDeletes)
	}
	if res.RemainingCount != 50 {
		t.Fatalf("remaining = %d, want 50", res.RemainingCount)
	}
}

func TestSelectDeletableNumericTieBreak(t *testing.T) {
	// All versions share one CreatedAt; the numeric version tie-break must
	// pick v46..v50 as the newest 5 even though "v9" sorts before "v46"
	// lexicographically.
	vs := []Version{
		{ImageVersion: "v50", CreatedAt: t1(1), State: "SUCCESSFUL"},
		{ImageVersion: "v49", CreatedAt: t1(1), State: "SUCCESSFUL"},
		{ImageVersion: "v48", CreatedAt: t1(1), State: "SUCCESSFUL"},
		{ImageVersion: "v47", CreatedAt: t1(1), State: "SUCCESSFUL"},
		{ImageVersion: "v46", CreatedAt: t1(1), State: "SUCCESSFUL"},
		{ImageVersion: "v9", CreatedAt: t1(1), State: "SUCCESSFUL"},
	}
	p := NewPruner(&stubClient{})
	deletable := p.selectDeletable(vs, "")
	if len(deletable) != 1 || deletable[0].ImageVersion != "v9" {
		t.Fatalf("deletable = %v, want [v9]", deletable)
	}
}

func TestPruneDeleteSerialized(t *testing.T) {
	s := newStub(versions(30))
	p := NewPruner(s, WithSleep((&sleepRecorder{}).sleep))
	if _, err := p.Prune(context.Background(), s.image.Name); err != nil {
		t.Fatalf("Prune returned error: %v", err)
	}
	if max := atomic.LoadInt32(&s.maxInFlight); max != 1 {
		t.Fatalf("max concurrent deletes = %d, want 1 (serialized)", max)
	}
}

func TestVersionNumber(t *testing.T) {
	cases := map[string]int{
		"v46": 46,
		"V1":  1,
		"10":  10,
		"v":   0,
		"abc": 0,
		"":    0,
	}
	for in, want := range cases {
		if got := versionNumber(in); got != want {
			t.Errorf("versionNumber(%q) = %d, want %d", in, got, want)
		}
	}
}

func t1(hour int) time.Time {
	return time.Date(2026, 8, 1, hour, 0, 0, 0, time.UTC)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
