// Package microvmversions prunes AWS::Lambda::MicrovmImage versions at deploy
// time (issue #1052). The service hard-caps an image at 50 versions; every
// content-changed deploy publishes a new version and rollback publishes yet
// another, so an image that is never pruned eventually hits the cap and the
// deploy fails with a 402 "maximum number of versions allowed" error.
//
// The pruner runs BEFORE the deploy publishes anything: it lists versions,
// deletes the oldest beyond the newest N LIVE versions, never touches the
// newest N live versions and never touches the version the active controller
// references, and serializes the deletes with settle-waits because concurrent
// deletes race the image's UPDATING state machine (ConflictException — the
// live prune observed 11/45 conflicts in a tight loop, all succeeding on retry
// with ~5s spacing). The active-version pin is re-checked before EACH delete:
// a version that becomes active after selection (a racing deploy or a rollback
// to an old version) is skipped, never deleted. The keep set is computed over
// live versions only: DELETED ghosts are excluded entirely and DELETING
// versions are protected but do not consume a keep slot, so out-of-band
// deletions of recent versions can never push older SUCCESSFUL rollback
// versions into the delete set.
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

	// settleBeforeReList is the settle-wait before the post-prune confirmation
	// re-list. Deletes reap asynchronously; without the wait a still-reaping
	// DELETING version could be misread as a stuck one. The cap check after the
	// re-list still fails closed on genuinely stuck deletions.
	settleBeforeReList = 10 * time.Second

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
	SkippedActive  int // versions selected for deletion but skipped because they became the active controller version mid-loop (per-delete TOCTOU guard)
	Kept           int // versions not targeted for deletion (newest N + active + non-deletable states); versions skipped as newly-active are reported in SkippedActive
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
	imageRequired       bool
	settleAfterDelete   time.Duration
	settleBeforeReList  time.Duration
	conflictRetryBase   time.Duration
	conflictRetryMax    time.Duration
	maxConflictAttempts int
	sleep               func(context.Context, time.Duration) error
}

// Option configures a Pruner.
type Option func(*Pruner)

// WithKeepN sets the number of newest LIVE versions to keep. Values below 1
// clamp to 1 (a defensive floor for direct API use; the CLI validates before
// constructing the pruner).
func WithKeepN(n int) Option {
	return func(p *Pruner) {
		if n < 1 {
			n = 1
		}
		p.keepN = n
	}
}

// WithImageRequired marks the image as required: ResolveImage returning
// ErrImageNotFound then becomes a hard, fail-closed error instead of a
// first-deploy no-op. The deploy wrapper sets this when the synthesized
// template declares the AWS::Lambda::MicrovmImage resource, so a name drift or
// an out-of-band deletion of the whole image cannot silently skip pruning.
func WithImageRequired(required bool) Option {
	return func(p *Pruner) {
		p.imageRequired = required
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
		settleBeforeReList:  settleBeforeReList,
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
// newest keepN live versions, never the active controller version, never
// unsettled states), deletes them serially with settle-waits while re-checking
// the active-version pin before each delete (a version that becomes active
// mid-loop is skipped, never deleted), then confirms the image is back under
// the version cap.
//
// Fail-closed: if the image cannot be brought under the cap, the cap cannot be
// verified (list failures), or the image is marked required but cannot be
// resolved, Prune returns an error so the deploy is aborted before it can hit
// the 402. Fail-open: individual delete failures while the image still has
// headroom are returned as warnings, not errors.
func (p *Pruner) Prune(ctx context.Context, imageName string) (Result, error) {
	res := Result{ImageName: imageName}

	image, found, err := p.resolveRequiredOrNoop(ctx, imageName)
	if err != nil {
		return res, err
	}
	if !found {
		res.Warnings = append(res.Warnings,
			"image not found; nothing to prune (first deploy creates the image)")
		return res, nil
	}
	res.ImageARN = image.ARN

	versions, err := p.client.ListVersions(ctx, image.ARN)
	if err != nil {
		return res, fmt.Errorf("microvmversions: list versions for %q: %w", image.ARN, err)
	}
	res.Listed = len(versions)

	// Re-resolve the image so the active-version pin is as fresh as possible
	// before anything outside the newest N is deleted: the controller may have
	// advanced LatestActiveImageVersion between the initial resolve and now.
	activeVersion, warns, err := p.refreshActiveVersion(ctx, imageName, image.LatestActiveImageVersion)
	if err != nil {
		return res, err
	}
	res.Warnings = append(res.Warnings, warns...)
	if activeVersion != "" {
		image.LatestActiveImageVersion = activeVersion
	}
	res.ActiveVersion = image.LatestActiveImageVersion

	deletable := p.selectDeletable(versions, image.LatestActiveImageVersion)
	res.TargetDeletes = len(deletable)
	res.Kept = res.Listed - res.TargetDeletes

	deleted, failed, skippedActive, warnings, err := p.deleteAll(ctx, imageName, image.ARN, deletable)
	res.Deleted = deleted
	res.DeleteFailed = failed
	res.SkippedActive = skippedActive
	res.Warnings = append(res.Warnings, warnings...)
	if err != nil {
		return res, err
	}

	// Settle-wait before the confirmation re-list so in-flight deletions
	// finish reaping: without it a still-reaping DELETING version could be
	// misread as a stuck one. The cap check below still fails closed on
	// genuinely stuck deletions.
	if len(deletable) > 0 {
		if sleepErr := p.sleep(ctx, p.settleBeforeReList); sleepErr != nil {
			return res, fmt.Errorf("microvmversions: settle wait before re-list: %w", sleepErr)
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

// resolveRequiredOrNoop resolves imageName under the required-image policy:
// when the image is marked required, ErrImageNotFound is a hard fail-closed
// error (name drift or out-of-band whole-image deletion — not a first deploy);
// otherwise it is a first-deploy no-op reported as found=false.
func (p *Pruner) resolveRequiredOrNoop(ctx context.Context, imageName string) (Image, bool, error) {
	image, err := p.client.ResolveImage(ctx, imageName)
	if errors.Is(err, ErrImageNotFound) {
		if p.imageRequired {
			return Image{}, false, fmt.Errorf(
				"microvmversions: image %q not found but the deploy template declares the AWS::Lambda::MicrovmImage resource; "+
					"refusing to treat this as a first deploy (resolution failure: %v); "+
					"check for image-name drift or an out-of-band deletion of the whole image",
				imageName, err)
		}
		return Image{}, false, nil
	}
	if err != nil {
		return Image{}, false, fmt.Errorf("microvmversions: resolve image %q: %w", imageName, err)
	}
	return image, true, nil
}

// refreshActiveVersion re-resolves the image for a fresh active-version pin
// before anything outside the newest N is deleted. A re-resolve failure is
// fail-closed (we must not delete without a fresh pin). An empty active
// version is warned about: if the initial resolve supplied a pin, that pin is
// retained (stale but still protective — the selection keep set still shields
// it) and the warning says so; if there was never a pin, only versions outside
// the newest N are at risk.
func (p *Pruner) refreshActiveVersion(ctx context.Context, imageName, previous string) (string, []string, error) {
	current, err := p.client.ResolveImage(ctx, imageName)
	if err != nil {
		return "", nil, fmt.Errorf("microvmversions: re-resolve image %q before pruning: %w", imageName, err)
	}
	if current.LatestActiveImageVersion == "" {
		if previous != "" {
			return "", []string{fmt.Sprintf(
				"image %q resolved but LatestActiveImageVersion is empty; retaining the previously resolved pin %q (stale but still protective — only versions outside the newest %d may be deleted)",
				imageName, previous, p.keepN)}, nil
		}
		return "", []string{fmt.Sprintf(
			"image %q resolved but LatestActiveImageVersion is empty; no active-version pin available (only versions outside the newest %d may be deleted)",
			imageName, p.keepN)}, nil
	}
	return current.LatestActiveImageVersion, nil, nil
}

// deleteAll deletes the selected versions serially with settle-waits so the
// image's UPDATING state machine does not race the next delete. Before each
// delete the image is re-resolved (TOCTOU guard): a target that has become the
// active controller version since selection — a second theory-app-up on the
// same stage, or a rollback publishing an old version — is skipped loudly,
// never deleted. A re-resolve failure is the only hard error: we must not
// delete without a fresh active pin. Delete failures are fail-open (warnings):
// the image may still have headroom, and the deploy's only true precondition
// is a version count under the cap. A settle-wait failure is also a hard error
// (we cannot confirm serialization).
func (p *Pruner) deleteAll(ctx context.Context, imageName, imageARN string, deletable []Version) (deleted, failed, skippedActive int, warnings []string, err error) {
	firstDelete := true
	for _, v := range deletable {
		active, err := p.currentActiveVersion(ctx, imageName)
		if err != nil {
			return deleted, failed, skippedActive, warnings, fmt.Errorf(
				"microvmversions: before deleting version %s: %w", v.ImageVersion, err)
		}
		if active == v.ImageVersion {
			skippedActive++
			warnings = append(warnings, fmt.Sprintf(
				"image version %s became the active controller version during pruning; skipped (the active version is never deleted)",
				v.ImageVersion))
			continue
		}
		// Settle-wait before each delete except the first so consecutive deletes
		// stay >= settleAfterDelete apart even when a skipped target sits between
		// them (the skip itself needs no settle — no delete happened).
		if !firstDelete {
			if sleepErr := p.sleep(ctx, p.settleAfterDelete); sleepErr != nil {
				return deleted, failed, skippedActive, warnings, fmt.Errorf("microvmversions: settle wait: %w", sleepErr)
			}
		}
		firstDelete = false
		if delErr := p.deleteWithRetry(ctx, imageARN, v.ImageVersion); delErr != nil {
			failed++
			warnings = append(warnings, fmt.Sprintf(
				"delete image version %s failed: %v (deploy continues: image still has headroom)",
				v.ImageVersion, delErr))
		} else {
			deleted++
		}
	}
	return deleted, failed, skippedActive, warnings, nil
}

// currentActiveVersion re-resolves the image for the freshest active-version
// pin. A resolution failure is fail-closed: the caller must not delete without
// a fresh pin. An empty pin means the check cannot detect a mid-loop
// activation (the newest-N selection bound remains the only protection).
func (p *Pruner) currentActiveVersion(ctx context.Context, imageName string) (string, error) {
	current, err := p.client.ResolveImage(ctx, imageName)
	if err != nil {
		return "", fmt.Errorf("microvmversions: re-resolve image %q: %w", imageName, err)
	}
	return current.LatestActiveImageVersion, nil
}

// selectDeletable returns the versions eligible for deletion, oldest first.
// The newest keepN LIVE versions (by CreatedAt, numeric version tie-break) are
// never deleted — they are the rollback depth. The version the active
// controller references is never deleted either, even when it is older than
// the newest keepN. Versions whose state is not a settled, deletable state
// (PENDING, IN_PROGRESS, DELETING, DELETED, or an unknown state) are never
// targeted: we do not delete builds that are in flight or already gone, and we
// do not delete states we do not understand.
//
// The keep set is computed over live versions only. DELETED versions are
// excluded entirely (they are already gone — they neither consume a keep slot
// nor count toward the cap). DELETING versions are protected but non-consuming:
// a deletion in flight must not eat a keep slot, or ghosts in the newest
// positions would push older SUCCESSFUL rollback versions into the delete set
// (out-of-band manual pruning of recent versions — the 2026-08-25 incident —
// followed by a deploy inside the reap window).
func (p *Pruner) selectDeletable(versions []Version, activeVersion string) []Version {
	ordered := make([]Version, 0, len(versions))
	for _, v := range versions {
		if v.State == "DELETED" {
			continue
		}
		ordered = append(ordered, v)
	}
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
	kept := 0
	for i := 0; i < len(ordered) && kept < p.keepN; i++ {
		if ordered[i].State == "DELETING" {
			continue // protected, but does not consume a keep slot
		}
		keep[ordered[i].ImageVersion] = true
		kept++
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
