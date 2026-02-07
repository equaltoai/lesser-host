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

	"github.com/stretchr/testify/mock"

	"github.com/equaltoai/lesser-host/internal/config"
	"github.com/equaltoai/lesser-host/internal/store"
	"github.com/equaltoai/lesser-host/internal/store/models"
	"github.com/equaltoai/lesser-host/internal/testutil"
)

func ensureTrailingDot(fqdn string) string {
	if !strings.HasSuffix(fqdn, ".") {
		return fqdn + "."
	}
	return fqdn
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}

func unpackDNSMessage(b []byte) (dnsmessage.Message, bool) {
	var req dnsmessage.Message
	if err := req.Unpack(b); err != nil {
		return dnsmessage.Message{}, false
	}
	return req, true
}

func buildTXTResponse(req dnsmessage.Message, fqdn string, txt string) dnsmessage.Message {
	resp := dnsmessage.Message{
		Header: dnsmessage.Header{
			ID:                 req.ID,
			Response:           true,
			Authoritative:      true,
			RecursionAvailable: true,
		},
		Questions: req.Questions,
	}

	if len(req.Questions) == 0 {
		return resp
	}

	q := req.Questions[0]
	if q.Type != dnsmessage.TypeTXT || !strings.EqualFold(q.Name.String(), fqdn) {
		return resp
	}

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
	return resp
}

func serveDNSTXT(pc net.PacketConn, done <-chan struct{}, fqdn string, txt string) {
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
			if isTimeout(err) {
				continue
			}
			return
		}

		req, ok := unpackDNSMessage(buf[:n])
		if !ok {
			continue
		}

		resp := buildTXTResponse(req, fqdn, txt)
		packed, err := resp.Pack()
		if err != nil {
			continue
		}
		_, _ = pc.WriteTo(packed, addr)
	}
}

func withDNSTXTResolver(t *testing.T, fqdn string, txt string, fn func()) {
	t.Helper()

	fqdn = ensureTrailingDot(fqdn)

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })

	done := make(chan struct{})
	t.Cleanup(func() { close(done) })

	go func() {
		serveDNSTXT(pc, done, fqdn, txt)
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
	tdb := newDomainsTestDB()
	s := &Server{
		cfg:   config.Config{TipEnabled: false},
		store: store.New(tdb.db),
	}

	tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
		*dest = models.Instance{Slug: "demo", Owner: "alice"}
	}).Once()

	tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
		dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
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
	tdb.qVReq.On("CreateOrUpdate").Return(nil).Once()

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
		tdb := newDomainsTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
			*dest = models.Instance{Slug: "demo", Owner: "alice"}
		}).Once()
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
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
		tdb := newDomainsTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
			*dest = models.Instance{Slug: "demo", Owner: "alice"}
		}).Once()
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
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
		tdb := newDomainsTestDB()
		s := &Server{store: store.New(tdb.db)}

		tdb.qInst.On("First", mock.AnythingOfType("*models.Instance")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Instance](t, args, 0)
			*dest = models.Instance{Slug: "demo", Owner: "alice"}
		}).Once()
		tdb.qDomain.On("First", mock.AnythingOfType("*models.Domain")).Return(nil).Run(func(args mock.Arguments) {
			dest := testutil.RequireMockArg[*models.Domain](t, args, 0)
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
