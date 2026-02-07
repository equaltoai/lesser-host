package controlplane

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"

	apptheory "github.com/theory-cloud/apptheory/runtime"
	ttmocks "github.com/theory-cloud/tabletheory/pkg/mocks"

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
)

type portalVerifyDomainTestDB struct {
	db       *ttmocks.MockExtendedDB
	qInst    *ttmocks.MockQuery
	qDomain  *ttmocks.MockQuery
	qAudit   *ttmocks.MockQuery
	qVanity  *ttmocks.MockQuery
}

func newPortalVerifyDomainTestDB() portalVerifyDomainTestDB {
	db := ttmocks.NewMockExtendedDB()
	qInst := new(ttmocks.MockQuery)
	qDomain := new(ttmocks.MockQuery)
	qAudit := new(ttmocks.MockQuery)
	qVanity := new(ttmocks.MockQuery)

	db.On("WithContext", mock.Anything).Return(db).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Instance")).Return(qInst).Maybe()
	db.On("Model", mock.AnythingOfType("*models.Domain")).Return(qDomain).Maybe()
	db.On("Model", mock.AnythingOfType("*models.AuditLogEntry")).Return(qAudit).Maybe()
	db.On("Model", mock.AnythingOfType("*models.VanityDomainRequest")).Return(qVanity).Maybe()

	for _, q := range []*ttmocks.MockQuery{qInst, qDomain, qAudit, qVanity} {
		q.On("Where", mock.Anything, mock.Anything, mock.Anything).Return(q).Maybe()
		q.On("Index", mock.Anything).Return(q).Maybe()
		q.On("Limit", mock.Anything).Return(q).Maybe()
		q.On("IfExists").Return(q).Maybe()
		q.On("IfNotExists").Return(q).Maybe()
		q.On("ConsistentRead").Return(q).Maybe()
		q.On("Create").Return(nil).Maybe()
		q.On("CreateOrUpdate").Return(nil).Maybe()
		q.On("Delete").Return(nil).Maybe()
		q.On("Update", mock.Anything).Return(nil).Maybe()
	}

	return portalVerifyDomainTestDB{
		db:      db,
		qInst:   qInst,
		qDomain: qDomain,
		qAudit:  qAudit,
		qVanity: qVanity,
	}
}

func withDNSTXTResolver(t *testing.T, fqdn string, txt string, fn func()) {
	t.Helper()

	if !strings.HasSuffix(fqdn, ".") {
		fqdn = fqdn + "."
	}

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })

	done := make(chan struct{})
	t.Cleanup(func() { close(done) })

	go func() {
		buf := make([]byte, 2048)
		for {
			select {
			case <-done:
				return
			default:
			}

			_ = pc.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				return
			}

			var req dnsmessage.Message
			if err := req.Unpack(buf[:n]); err != nil {
				continue
			}

			resp := dnsmessage.Message{
				Header: dnsmessage.Header{
					ID:                 req.Header.ID,
					Response:           true,
					Authoritative:      true,
					RecursionAvailable: true,
				},
				Questions: req.Questions,
			}

			if len(req.Questions) > 0 {
				q := req.Questions[0]
				if q.Type == dnsmessage.TypeTXT && strings.EqualFold(q.Name.String(), fqdn) {
					resp.Answers = []dnsmessage.Resource{
						{
							Header: dnsmessage.ResourceHeader{
								Name:  q.Name,
								Type:  dnsmessage.TypeTXT,
								Class: dnsmessage.ClassINET,
								TTL:   60,
							},
							Body: &dnsmessage.TXTResource{TXT: []string{txt}},
						},
					}
				}
			}

			packed, err := resp.Pack()
			if err != nil {
				continue
			}
			_, _ = pc.WriteTo(packed, addr)
		}
	}()

	old := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", pc.LocalAddr().String())
		},
	}
	t.Cleanup(func() { net.DefaultResolver = old })

	fn()
}

func TestHandlePortalVerifyInstanceDomain_VerifiesAndCreatesVanityRequest(t *testing.T) {
	tdb := newPortalVerifyDomainTestDB()
	s := &Server{
		cfg:   config.Config{TipEnabled: false},
		store: store.New(tdb.db),
	}

	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Instance)
		*dest = models.Instance{Slug: "demo", Owner: "alice"}
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := args.Get(0).(*models.Domain)
		*dest = models.Domain{
			Domain:             "example.com",
			DomainRaw:          "Example.COM",
			InstanceSlug:       "demo",
			Type:               models.DomainTypeVanity,
			Status:             models.DomainStatusPending,
			VerificationMethod: domainVerificationMethodDNSTXT,
			VerificationToken:  "tok",
		}
		_ = dest.UpdateKeys()
	}).Once()

	tdb.qDomain.On("Update", mock.Anything).Return(nil).Once()
	tdb.qAudit.On("Create").Return(nil).Once()
	tdb.qVanity.On("CreateOrUpdate").Return(nil).Once()

	ctx := &apptheory.Context{
		AuthIdentity: "alice",
		RequestID:    "rid",
		Params:       map[string]string{"slug": "demo", "domain": "example.com"},
	}

	txtName := domainVerificationRecordPrefix + "example.com"
	txtValue := domainVerificationValuePrefix + "tok"

	withDNSTXTResolver(t, txtName, txtValue, func() {
		resp, err := s.handlePortalVerifyInstanceDomain(ctx)
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("resp=%#v err=%v", resp, err)
		}

		var parsed verifyDomainResponse
		if err := json.Unmarshal(resp.Body, &parsed); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if strings.TrimSpace(parsed.Domain.Status) != models.DomainStatusVerified {
			t.Fatalf("expected verified, got %#v", parsed.Domain)
		}
	})
}

func TestHandlePortalVerifyInstanceDomain_EarlyReturnsAndErrors(t *testing.T) {
	t.Run("already_verified", func(t *testing.T) {
		tdb := newPortalVerifyDomainTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.Instance)
			*dest = models.Instance{Slug: "demo", Owner: "alice"}
		}).Once()
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.Domain)
			*dest = models.Domain{
				Domain:       "example.com",
				InstanceSlug: "demo",
				Type:         models.DomainTypeVanity,
				Status:       models.DomainStatusVerified,
			}
			_ = dest.UpdateKeys()
		}).Once()

		resp, err := s.handlePortalVerifyInstanceDomain(&apptheory.Context{
			AuthIdentity: "alice",
			Params:       map[string]string{"slug": "demo", "domain": "example.com"},
		})
		if err != nil || resp == nil || resp.Status != 200 {
			t.Fatalf("resp=%#v err=%v", resp, err)
		}
	})

	t.Run("not_eligible_without_token", func(t *testing.T) {
		tdb := newPortalVerifyDomainTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.Instance)
			*dest = models.Instance{Slug: "demo", Owner: "alice"}
		}).Once()
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.Domain)
			*dest = models.Domain{
				Domain:            "example.com",
				InstanceSlug:      "demo",
				Type:              models.DomainTypeVanity,
				Status:            models.DomainStatusPending,
				VerificationToken: "",
			}
			_ = dest.UpdateKeys()
		}).Once()

		_, err := s.handlePortalVerifyInstanceDomain(&apptheory.Context{
			AuthIdentity: "alice",
			Params:       map[string]string{"slug": "demo", "domain": "example.com"},
		})
		if err == nil {
			t.Fatalf("expected conflict error")
		}
	})

	t.Run("verification_record_not_found", func(t *testing.T) {
		tdb := newPortalVerifyDomainTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.Instance)
			*dest = models.Instance{Slug: "demo", Owner: "alice"}
		}).Once()
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := args.Get(0).(*models.Domain)
			*dest = models.Domain{
				Domain:            "example.com",
				InstanceSlug:      "demo",
				Type:              models.DomainTypeVanity,
				Status:            models.DomainStatusPending,
				VerificationToken: "tok",
			}
			_ = dest.UpdateKeys()
		}).Once()

		txtName := domainVerificationRecordPrefix + "example.com"
		withDNSTXTResolver(t, txtName, "wrong", func() {
			_, err := s.handlePortalVerifyInstanceDomain(&apptheory.Context{
				AuthIdentity: "alice",
				Params:       map[string]string{"slug": "demo", "domain": "example.com"},
			})
			if err == nil {
				t.Fatalf("expected verification error")
			}
		})
	})
}

