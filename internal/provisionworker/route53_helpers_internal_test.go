package provisionworker

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/route53"
)

type memZone struct {
	id          string
	name        string
	nameServers []string
}

type memRoute53 struct {
	mu    sync.Mutex
	zones map[string]memZone // id -> zone

	lastMethod string
	lastPath   string
	lastQuery  string
}

func (m *memRoute53) ensureZone(id string, name string, ns []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.zones == nil {
		m.zones = map[string]memZone{}
	}
	m.zones[id] = memZone{id: id, name: name, nameServers: append([]string(nil), ns...)}
}

func (m *memRoute53) findByName(name string) (memZone, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, z := range m.zones {
		if strings.EqualFold(strings.TrimSpace(z.name), strings.TrimSpace(name)) {
			return z, true
		}
	}
	return memZone{}, false
}

func (m *memRoute53) findByID(id string) (memZone, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	z, ok := m.zones[id]
	return z, ok
}

func (m *memRoute53) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/xml")

	m.mu.Lock()
	m.lastMethod = r.Method
	m.lastPath = r.URL.Path
	m.lastQuery = r.URL.RawQuery
	m.mu.Unlock()

	path := strings.TrimSuffix(r.URL.Path, "/")

	switch {
	case r.Method == http.MethodGet && (path == "/2013-04-01/hostedzonesbyname" || path == "/2013-04-01/hostedzone"):
		// ListHostedZonesByName.
		dns := strings.TrimSpace(r.URL.Query().Get("dnsname"))
		if z, ok := m.findByName(dns); ok {
			_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListHostedZonesByNameResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <HostedZones>
    <HostedZone>
      <Id>/hostedzone/%s</Id>
      <Name>%s</Name>
      <CallerReference>ref</CallerReference>
      <Config><PrivateZone>false</PrivateZone></Config>
      <ResourceRecordSetCount>1</ResourceRecordSetCount>
    </HostedZone>
  </HostedZones>
  <IsTruncated>false</IsTruncated>
  <MaxItems>10</MaxItems>
</ListHostedZonesByNameResponse>`, z.id, z.name)
			return
		}
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListHostedZonesByNameResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <HostedZones></HostedZones>
  <IsTruncated>false</IsTruncated>
  <MaxItems>10</MaxItems>
</ListHostedZonesByNameResponse>`)
		return

	case r.Method == http.MethodPost && path == "/2013-04-01/hostedzone":
		// CreateHostedZone.
		name := "example.com."
		id := "ZCREATED"
		ns := []string{"ns-b", "ns-a", "ns-a"}
		m.ensureZone(id, name, ns)

		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<CreateHostedZoneResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <HostedZone>
    <Id>/hostedzone/%s</Id>
    <Name>%s</Name>
    <CallerReference>ref</CallerReference>
  </HostedZone>
  <DelegationSet>
    <NameServers>
      <NameServer>%s</NameServer>
      <NameServer>%s</NameServer>
      <NameServer>%s</NameServer>
    </NameServers>
  </DelegationSet>
</CreateHostedZoneResponse>`, id, name, ns[0], ns[1], ns[2])
		return

	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/2013-04-01/hostedzone/"):
		// GetHostedZone.
		id := strings.TrimPrefix(r.URL.Path, "/2013-04-01/hostedzone/")
		if z, ok := m.findByID(id); ok {
			_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<GetHostedZoneResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <HostedZone>
    <Id>/hostedzone/%s</Id>
    <Name>%s</Name>
    <CallerReference>ref</CallerReference>
  </HostedZone>
  <DelegationSet>
    <NameServers>
      <NameServer>%s</NameServer>
      <NameServer>%s</NameServer>
    </NameServers>
  </DelegationSet>
</GetHostedZoneResponse>`, z.id, z.name, z.nameServers[0], z.nameServers[1])
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<ErrorResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/">
  <Error>
    <Type>Sender</Type>
    <Code>NoSuchHostedZone</Code>
    <Message>Not found</Message>
  </Error>
</ErrorResponse>`)
		return

	default:
		http.NotFound(w, r)
		return
	}
}

func newRoute53ClientForTest(t *testing.T, baseURL string) *route53.Client {
	t.Helper()

	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
	}
	return route53.NewFromConfig(cfg, func(o *route53.Options) {
		o.BaseEndpoint = aws.String(baseURL)
		o.HTTPClient = http.DefaultClient
	})
}

func TestRoute53Helpers_FindCreateGetAndEnsureHostedZoneAndNameServers(t *testing.T) {
	t.Parallel()

	mem := &memRoute53{}
	mem.ensureZone("ZEXISTING", "example.com.", []string{"ns-1", "ns-2"})
	ts := httptest.NewServer(http.HandlerFunc(mem.handler))
	t.Cleanup(ts.Close)

	client := newRoute53ClientForTest(t, ts.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// findHostedZoneIDByName: success.
	if got, err := findHostedZoneIDByName(ctx, client, "example.com."); err != nil || got != "ZEXISTING" {
		t.Fatalf("findHostedZoneIDByName: got=%q err=%v (last=%s %s?%s)", got, err, mem.lastMethod, mem.lastPath, mem.lastQuery)
	}

	// findHostedZoneIDByName: no match.
	if got, err := findHostedZoneIDByName(ctx, client, "missing.example."); err != nil || got != "" {
		t.Fatalf("expected empty for missing, got=%q err=%v", got, err)
	}

	// createHostedZone: normalizes id + name servers.
	zoneID, ns, err := createHostedZone(ctx, client, "example.com.", "job1")
	if err != nil {
		t.Fatalf("createHostedZone: %v", err)
	}
	if zoneID == "" || len(ns) != 2 || ns[0] != "ns-a" || ns[1] != "ns-b" {
		t.Fatalf("unexpected create output: zoneID=%q ns=%#v", zoneID, ns)
	}

	// getHostedZoneNameServers: validates.
	if _, err := getHostedZoneNameServers(ctx, client, " "); err == nil {
		t.Fatalf("expected zone id required error")
	}

	// ensureHostedZoneAndNameServers: short-circuit when existing inputs present.
	gotID, gotNS, err := ensureHostedZoneAndNameServers(ctx, nil, "example.com.", "/hostedzone/Z1", []string{" b ", "a"}, "job")
	if err != nil || gotID != "Z1" || len(gotNS) != 2 || gotNS[0] != "a" || gotNS[1] != "b" {
		t.Fatalf("unexpected short-circuit: id=%q ns=%#v err=%v", gotID, gotNS, err)
	}

	// ensureHostedZoneAndNameServers: list -> get.
	mem.ensureZone("ZLIST", "list.example.", []string{"ns-x", "ns-y"})
	gotID, gotNS, err = ensureHostedZoneAndNameServers(ctx, client, "list.example.", "", nil, "job")
	if err != nil || gotID != "ZLIST" || len(gotNS) != 2 {
		t.Fatalf("unexpected list->get: id=%q ns=%#v err=%v", gotID, gotNS, err)
	}
}
