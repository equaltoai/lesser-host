package trust

import (
	"testing"

	"github.com/stretchr/testify/mock"
	core "github.com/theory-cloud/tabletheory/v3/pkg/core"
	ttmocks "github.com/theory-cloud/tabletheory/v3/pkg/mocks"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func TestNormalizeInstanceAttestationSubject_BlankOrPartial(t *testing.T) {
	t.Parallel()

	s := &Server{}
	subject, err := s.normalizeInstanceAttestationSubject(t.Context(), " "+testBudgetInstanceSlug+" ", " ", "", "")
	if err != nil {
		t.Fatalf("unexpected blank subject error: %v", err)
	}
	if subject.InstanceSlug != testBudgetInstanceSlug || subject.ActorURI != "" || subject.ObjectURI != "" || subject.ContentHash != "" {
		t.Fatalf("unexpected blank subject: %#v", subject)
	}

	_, err = s.normalizeInstanceAttestationSubject(t.Context(), testBudgetInstanceSlug, "https://inst.example/@alice", "", "hash")
	if err == nil || err.Code != appErrCodeBadRequest {
		t.Fatalf("expected bad_request for partial subject, got %T: %v", err, err)
	}
}

func TestNormalizeInstanceAttestationSubject_RequiresOwnedHTTPHosts(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	for _, q := range []*ttmocks.MockQuery{qInst, qDomain} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
	}

	s := NewServer(config.Config{Stage: "lab"}, store.New(db))

	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: testBudgetInstanceSlug, HostedBaseDomain: "inst.example"}
	}).Once()
	subject, err := s.normalizeInstanceAttestationSubject(
		t.Context(),
		testBudgetInstanceSlug,
		"https://inst.example/@alice",
		"https://inst.example/posts/1",
		"sha256:abc",
	)
	if err != nil {
		t.Fatalf("unexpected owned subject error: %v", err)
	}
	if subject.ActorURI != "https://inst.example/@alice" || subject.ContentHash != "sha256:abc" {
		t.Fatalf("unexpected subject: %#v", subject)
	}

	qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: testBudgetInstanceSlug, HostedBaseDomain: "inst.example"}
	}).Once()
	qDomain.On("Limit", mock.Anything).Return(qDomain).Once()
	qDomain.On("AllPaginated", mock.AnythingOfType("*[]*models.Domain")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = nil
	}).Once()
	_, err = s.normalizeInstanceAttestationSubject(
		t.Context(),
		testBudgetInstanceSlug,
		"https://other.example/@alice",
		"https://inst.example/posts/1",
		"sha256:abc",
	)
	if err == nil || err.Code != appErrCodeBadRequest {
		t.Fatalf("expected bad_request for unowned subject, got %T: %v", err, err)
	}

	_, err = s.normalizeInstanceAttestationSubject(t.Context(), testBudgetInstanceSlug, "at://did:example:alice/post/1", "https://inst.example/posts/1", "hash")
	if err == nil || err.Code != appErrCodeBadRequest {
		t.Fatalf("expected bad_request for non-http actor URI, got %T: %v", err, err)
	}
}

func TestAttestationSubjectHostHelpers(t *testing.T) {
	t.Parallel()

	host, err := attestationSubjectURIHost(" https://Example.COM:443/users/alice ")
	if err != nil || host != "example.com" {
		t.Fatalf("unexpected host parse: host=%q err=%v", host, err)
	}
	for _, raw := range []string{"", "at://example.com/users/alice", "https:///missing-host"} {
		if _, err := attestationSubjectURIHost(raw); err == nil {
			t.Fatalf("expected parse error for %q", raw)
		}
	}

	owned := map[string]struct{}{}
	addOwnedAttestationDomain(nil, "example.com")
	addOwnedAttestationDomain(owned, " EXAMPLE.com ")
	addOwnedAttestationDomain(owned, "not a host")
	if !attestationHostsInSet(owned, "example.com", "EXAMPLE.COM") {
		t.Fatalf("expected exact owned host match")
	}
	if attestationHostsInSet(owned, "sub.example.com", "example.com") {
		t.Fatalf("expected exact matching, not suffix matching")
	}
	if attestationHostsInSet(nil, "example.com", "example.com") {
		t.Fatalf("expected empty owned map to fail")
	}
}

func TestAttestationDomainActive(t *testing.T) {
	t.Parallel()

	if !attestationDomainActive(models.DomainStatusVerified) || !attestationDomainActive(models.DomainStatusActive) {
		t.Fatalf("expected verified and active domains to be accepted")
	}
	if attestationDomainActive("pending") {
		t.Fatalf("expected pending domain to be rejected")
	}
}

// TestLoadAttestationSubjectDomainsBoundedWalk verifies the subject-domain
// read (issue #1061 part B) issues no Scan, applies the bounded page limit on
// every outgoing query, and resumes across pages via the opaque cursor.
func TestLoadAttestationSubjectDomainsBoundedWalk(t *testing.T) {
	t.Parallel()

	db := ttmocks.NewMockExtendedDB()
	qDomain := new(ttmocks.MockQuery)
	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	for _, q := range []*ttmocks.MockQuery{qDomain} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
	}

	appliedLimits := []int{}
	qDomain.On("Limit", mock.Anything).Return(qDomain).Times(2).Run(func(args mock.Arguments) {
		appliedLimits = append(appliedLimits, testutil.RequireMockArg[int](t, args, 0))
	})
	qDomain.On("Cursor", "after-page-1").Return(qDomain).Once()
	qDomain.On("AllPaginated", mock.AnythingOfType("*[]*models.Domain")).Return(&core.PaginatedResult{HasMore: true, NextCursor: "after-page-1"}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "one.example", InstanceSlug: "inst"}}
	}).Once()
	qDomain.On("AllPaginated", mock.AnythingOfType("*[]*models.Domain")).Return(&core.PaginatedResult{}, nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*[]*models.Domain](t, args, 0)
		*dest = []*models.Domain{{Domain: "two.example", InstanceSlug: "inst"}, {Domain: "three.example", InstanceSlug: "inst"}}
	}).Once()

	s := NewServer(config.Config{Stage: "lab"}, store.New(db))
	domains, appErr := s.loadAttestationSubjectDomains(t.Context(), "inst")
	if appErr != nil {
		t.Fatalf("unexpected appErr: %v", appErr)
	}
	if len(domains) != 3 {
		t.Fatalf("expected 3 domains across pages, got %d", len(domains))
	}
	if len(appliedLimits) != 2 || appliedLimits[0] != trustPartitionWalkPageSize || appliedLimits[1] != trustPartitionWalkPageSize {
		t.Fatalf("expected every page bounded to %d, got limits %v", trustPartitionWalkPageSize, appliedLimits)
	}
	qDomain.AssertNotCalled(t, "Scan", mock.Anything)
}
