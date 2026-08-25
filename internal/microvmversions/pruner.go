// Package microvmversions prunes AWS::Lambda::MicrovmImage versions at deploy
// time (issue #1052). The service hard-caps an image at 50 versions; every
// content-changed deploy publishes a new version and rollback publishes yet
// another, so an image that is never pruned eventually hits the cap and the
// deploy fails with a 402 "maximum number of versions allowed" error.
//
// The pruner runs BEFORE the deploy publishes anything: it lists versions,
// deletes the oldest beyond the newest N, never touches the newest N and never
// touches the version the active controller references, and serializes the
// deletes with settle-waits because concurrent deletes race the image's
// UPDATING state machine (ConflictException — the live prune observed 11/45
// conflicts in a tight loop, all succeeding on retry with ~5s spacing).
//
// Failure policy:
//   - If pruning cannot bring the image back under the 50-version cap, Prune
//     returns an error so the deploy fails loudly BEFORE the 402 (fail-closed).
//   - If the image already has headroom, pruning down to keep-N is
//     opportunistic: an individual delete failure is reported as a warning and
//     does not block the deploy (fail-open), because the only precondition a
//     deploy truly needs is a version count under the cap.
package microvmversions

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MaxVersionsPerImage is the service hard cap on versions per MicroVM image.
// It is not present in Service Quotas and is not adjustable (issue #1052).
const MaxVersionsPerImage = 50

// DefaultKeepN is the default number of newest versions to keep. N=5 preserves
// ~5 deploys of rollback depth while leaving wide headroom under the cap.
const DefaultKeepN = 5

const (
	// settleAfterDelete is the settle-wait between serialized deletes. The
	// live prune needed ~5s spacing for the image's UPDATING state machine to
	// settle between operations.
	settleAfterDelete = 5 * time.Second

	// conflictRetryBase/conflictRetryMax bound the exponential backoff applied
	// when a delete races the image's UPDATING state (ConflictException).
	conflictRetryBase = 5 * time.Second
	conflictRetryMax  = 30 * time.Second

	// maxConflictAttempts caps how many times a single delete is retried after
	// a ConflictException before it is treated as failed.
	maxConflictAttempts = 8
)

// Image describes one hosted-genesis microvm image as seen by the service.
type Image struct {
	Name                     string
	ARN                      string
	LatestActiveImageVersion string
}

// Version is one version of a microvm image. State carries the service enum
// value (PENDING / IN_PROGRESS / SUCCESSFUL / FAILED / DELETING / DELETED /
// DELETE_FAILED); Status carries the availability status (ACTIVE/INACTIVE).
type Version struct {
	ImageVersion string
	CreatedAt    time.Time
	State        string
	Status       string
}

// Result summarizes one pruning run.
type Result struct {
	ImageName      string
	ImageARN       string
	ActiveVersion  string
	Listed         int // versions listed before pruning
	TargetDeletes  int // versions selected for deletion
	Deleted        int // versions successfully deleted
	DeleteFailed   int // versions whose delete failed (retries exhausted or terminal error)
	Kept           int // versions not targeted for deletion (newest N + active + non-deletable states)
	RemainingCount int // versions reported by the final listing (excluding DELETED)
	Warnings       []string
}

// Client is the microvm image surface the pruner needs. SDKClient implements
// it against the aws-sdk-go-v2 lambdamicrovms service; tests stub it.
type Client interface {
	// ResolveImage resolves an image by exact name. Returns ErrImageNotFound
	// when the image does not exist (first deploy — nothing to prune).
	ResolveImage(ctx context.Context, name string) (Image, error)
	// ListVersions lists every version of the image.
	ListVersions(ctx context.Context, imageARN string) ([]Version, error)
	// DeleteVersion deletes one image version. The service documents the
	// operation as idempotent. Conflicts that race the image's UPDATING state
	// are reported as ErrConflict so the pruner can settle and retry.
	DeleteVersion(ctx context.Context, imageARN, imageVersion string) error
}

// Pruner prunes microvm image versions to keep-N at deploy time.
type Pruner struct {
	client              Client
	keepN               int
	maxVersions         int
	settleAfterDelete   time.Duration
	conflictRetryBase   time.Duration
	conflictRetryMax    time.Duration
	maxConflictAttempts int
	sleep               func(context.Context, time.Duration) error
}

// Option configures a Pruner.
type Option func(*Pruner)

// WithKeepN sets the number of newest versions to keep (must be >= 1).
func WithKeepN(n int) Option {
	return func(p *Pruner) {
		if n < 1 {
			n = 1
		}
		p.keepN = n
	}
}

// WithSleep replaces the sleep implementation (injectable for tests).
func WithSleep(s func(context.Context, time.Duration) error) Option {
	return func(p *Pruner) {
		if s != nil {
			p.sleep = s
		}
	}
}

// NewPruner returns a Pruner with production defaults over client.
func NewPruner(client Client, opts ...Option) *Pruner {
	p := &Pruner{
		client:              client,
		keepN:               DefaultKeepN,
		maxVersions:         MaxVersionsPerImage,
		settleAfterDelete:   settleAfterDelete,
		conflictRetryBase:   conflictRetryBase,
		conflictRetryMax:    conflictRetryMax,
		maxConflictAttempts: maxConflictAttempts,
		sleep:               sleep,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// ImageNameForStage returns the hosted-genesis microvm image name owned by the
// given deploy stage. The CDK stack is named lesser-host-<stage> and its
// AppTheoryMicrovmImage name is `<stack-name>_hosted_genesis`
// (cdk/lib/hosted-genesis-microvm.ts). Each deploy prunes only the image its
// own stage owns.
func ImageNameForStage(stage string) string {
	return fmt.Sprintf("lesser-host-%s_hosted_genesis", stage)
}

// Prune resolves imageName, selects the deletable versions (oldest beyond the
// newest keepN, never the active controller version, never unsettled states),
// deletes them serially with settle-waits, then confirms the image is back
// under the version cap.
//
// Fail-closed: if the image cannot be brought under the cap, or the cap cannot
// be verified (list failures), Prune returns an error so the deploy is aborted
// before it can hit the 402. Fail-open: individual delete failures while the
// image still has headroom are returned as warnings, not errors.
func (p *Pruner) Prune(ctx context.Context, imageName string) (Result, error) {
	res := Result{ImageName: imageName}

	image, err := p.client.ResolveImage(ctx, imageName)
	if errors.Is(err, ErrImageNotFound) {
		res.Warnings = append(res.Warnings,
			"image not found; nothing to prune (first deploy creates the image)")
		return res, nil
	}
	if err != nil {
		return res, fmt.Errorf("microvmversions: resolve image %q: %w", imageName, err)
	}
	res.ImageARN = image.ARN
	res.ActiveVersion = image.LatestActiveImageVersion

	versions, err := p.client.ListVersions(ctx, image.ARN)
	if err != nil {
		return res, fmt.Errorf("microvmversions: list versions for %q: %w", image.ARN, err)
	}
	res.Listed = len(versions)

	deletable := p.selectDeletable(versions, image.LatestActiveImageVersion)
	res.TargetDeletes = len(deletable)
	res.Kept = res.Listed - res.TargetDeletes

	for i, v := range deletable {
		if delErr := p.deleteWithRetry(ctx, image.ARN, v.ImageVersion); delErr != nil {
			res.DeleteFailed++
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"delete image version %s failed: %v (deploy continues: image still has headroom)",
				v.ImageVersion, delErr))
		} else {
			res.Deleted++
		}
		// Settle-wait between serialized deletes so the image's UPDATING state
		// machine does not race the next delete (skip after the last).
		if i < len(deletable)-1 {
			if sleepErr := p.sleep(ctx, p.settleAfterDelete); sleepErr != nil {
				return res, fmt.Errorf("microvmversions: settle wait: %w", sleepErr)
			}
		}
	}

	// Re-list for the authoritative remaining count. Failing to confirm
	// headroom is fail-closed: the deploy must not proceed into a 402.
	remaining, err := p.client.ListVersions(ctx, image.ARN)
	if err != nil {
		return res, fmt.Errorf("microvmversions: re-list versions for %q after pruning: %w", image.ARN, err)
	}
	res.RemainingCount = countRemaining(remaining)

	if res.RemainingCount >= p.maxVersions {
		return res, fmt.Errorf(
			"microvmversions: image %q still has %d versions after pruning (cap %d); "+
				"the deploy would fail with 402 — refusing to proceed "+
				"(deleted=%d, delete failures=%d)",
			imageName, res.RemainingCount, p.maxVersions, res.Deleted, res.DeleteFailed)
	}
	return res, nil
}

// selectDeletable returns the versions eligible for deletion, oldest first.
// The newest keepN versions (by CreatedAt, numeric version tie-break) are
// never deleted — they are the rollback depth. The version the active
// controller references is never deleted either, even when it is older than
// the newest keepN. Versions whose state is not a settled, deletable state
// (PENDING, IN_PROGRESS, DELETING, DELETED, or an unknown state) are never
// targeted: we do not delete builds that are in flight or already gone, and we
// do not delete states we do not understand.
func (p *Pruner) selectDeletable(versions []Version, activeVersion string) []Version {
	ordered := append([]Version(nil), versions...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].CreatedAt.Equal(ordered[j].CreatedAt) {
			return ordered[i].CreatedAt.After(ordered[j].CreatedAt)
		}
		ni, nj := versionNumber(ordered[i].ImageVersion), versionNumber(ordered[j].ImageVersion)
		if ni != nj {
			return ni > nj
		}
		return ordered[i].ImageVersion > ordered[j].ImageVersion
	})

	keep := make(map[string]bool, p.keepN+1)
	for i := 0; i < len(ordered) && i < p.keepN; i++ {
		keep[ordered[i].ImageVersion] = true
	}
	if activeVersion != "" {
		keep[activeVersion] = true
	}

	nonDeletableState := map[string]bool{
		"PENDING":     true,
		"IN_PROGRESS": true,
		"DELETING":    true,
		"DELETED":     true,
	}
	deletableState := map[string]bool{
		"SUCCESSFUL":    true,
		"FAILED":        true,
		"DELETE_FAILED": true,
	}

	var deletable []Version
	for i := len(ordered) - 1; i >= 0; i-- { // oldest first
		v := ordered[i]
		if keep[v.ImageVersion] {
			continue
		}
		if nonDeletableState[v.State] || !deletableState[v.State] {
			continue
		}
		deletable = append(deletable, v)
	}
	return deletable
}

// deleteWithRetry deletes one version, retrying ErrConflict with exponential
// settle backoff. A non-conflict error is terminal for that version. When
// retries are exhausted the last conflict error is returned (wrapped in
// ErrConflict) so the caller can decide fail-open vs fail-closed.
func (p *Pruner) deleteWithRetry(ctx context.Context, imageARN, version string) error {
	var lastErr error
	backoff := p.conflictRetryBase
	for attempt := 1; attempt <= p.maxConflictAttempts; attempt++ {
		deleteErr := p.client.DeleteVersion(ctx, imageARN, version)
		if deleteErr == nil {
			return nil
		}
		lastErr = deleteErr
		if !errors.Is(deleteErr, ErrConflict) {
			return deleteErr
		}
		if waitErr := p.sleep(ctx, backoff); waitErr != nil {
			return fmt.Errorf("microvmversions: conflict retry wait: %w", waitErr)
		}
		backoff *= 2
		if backoff > p.conflictRetryMax {
			backoff = p.conflictRetryMax
		}
	}
	return fmt.Errorf("%w: version %s still conflicting after %d attempts: %v", ErrConflict, version, p.maxConflictAttempts, lastErr)
}

// countRemaining counts versions that still occupy the cap. DELETED versions
// are excluded; DELETING versions are counted because they are not gone yet.
func countRemaining(versions []Version) int {
	n := 0
	for _, v := range versions {
		if v.State == "DELETED" {
			continue
		}
		n++
	}
	return n
}

// versionNumber extracts the numeric suffix of an image version ("v46" -> 46)
// for deterministic tie-breaking when CreatedAt timestamps collide. Versions
// that do not parse are treated as 0 (stable, documented behavior).
func versionNumber(v string) int {
	s := strings.TrimPrefix(strings.ToLower(v), "v")
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
