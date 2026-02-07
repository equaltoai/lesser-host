package controlplane

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

func TestRoute53Helpers(t *testing.T) {
	t.Parallel()

	if got := normalizeRoute53ZoneName(" Example.COM. "); got != "example.com" {
		t.Fatalf("unexpected normalizeRoute53ZoneName: %q", got)
	}
	if !domainInZone("a.example.com", "example.com.") {
		t.Fatalf("expected domain in zone")
	}
	if domainInZone("example.com", "") {
		t.Fatalf("expected false for empty zone")
	}

	if got := ensureRoute53FQDN("a"); got != "a." {
		t.Fatalf("expected FQDN, got %q", got)
	}
	if got := ensureRoute53FQDN("a."); got != "a." {
		t.Fatalf("expected already fqdn, got %q", got)
	}

	if got := quoteTXTValue(` a"b `); got != `"a\"b"` {
		t.Fatalf("unexpected quoteTXTValue: %q", got)
	}
}

func TestPickBestHostedZoneID_PrefersLongestPublicMatch(t *testing.T) {
	t.Parallel()

	zones := []r53types.HostedZone{
		{Id: aws.String("Z1"), Name: aws.String("example.com.")},
		{Id: aws.String("Z2"), Name: aws.String("sub.example.com."), Config: &r53types.HostedZoneConfig{PrivateZone: true}},
		{Id: aws.String("Z3"), Name: aws.String("sub.example.com.")},
	}

	bestID, bestLen := pickBestHostedZoneID("x.sub.example.com", zones, "", -1)
	if bestID != "Z3" {
		t.Fatalf("expected best zone Z3, got %q", bestID)
	}
	if bestLen <= 0 {
		t.Fatalf("expected bestLen > 0, got %d", bestLen)
	}
}
