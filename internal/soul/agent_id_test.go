package soul

import (
	"testing"

	"github.com/equaltoai/lesser-host/internal/domains"
)

func TestNormalizeLocalAgentID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw  string
		want string
		ok   bool
	}{
		{raw: "agent-alice", want: "agent-alice", ok: true},
		{raw: "  @Agent-Bob  ", want: "agent-bob", ok: true},
		{raw: "soul_researcher", want: "soul_researcher", ok: true},

		{raw: "ab", ok: false},
		{raw: "alice@example.com", ok: false},
		{raw: "agent/alice", ok: false},
		{raw: "agent:alice", ok: false},
		{raw: "a-", ok: false},
		{raw: "-a", ok: false},
		{raw: "a.", ok: false},
		{raw: ".a", ok: false},
		{raw: "a_", ok: false},
		{raw: "_a", ok: false},
	}

	for _, c := range cases {
		got, err := NormalizeLocalAgentID(c.raw)
		if c.ok && err != nil {
			t.Fatalf("NormalizeLocalAgentID(%q) unexpected err: %v", c.raw, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("NormalizeLocalAgentID(%q) expected err", c.raw)
		}
		if c.ok && got != c.want {
			t.Fatalf("NormalizeLocalAgentID(%q) got=%q want=%q", c.raw, got, c.want)
		}
	}
}

func TestDeriveAgentIDHex_ConformanceVectors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		rawDomain string
		rawLocal  string
		want      string
	}{
		{
			rawDomain: " Example.Lesser.Social. ",
			rawLocal:  "agent-alice",
			want:      "0x8db124b1d48e366002db4e61cc1501eeb8561e1ef06fd6f9abf9f984501d13ab",
		},
		{
			rawDomain: "münich.example",
			rawLocal:  "agent-alice",
			want:      "0xf0b2c505271215e7bbbac618dc24f69de8aff1207d880d9c10b0779e7ce1b5e3",
		},
		{
			rawDomain: "例え.テスト",
			rawLocal:  "agent-alice",
			want:      "0x4744283784f8b135533d6b699c52ad842588b0c418c21a4a7c778df201572565",
		},
		{
			rawDomain: "dev.EXAMPLE.com",
			rawLocal:  "  @Agent-Bob  ",
			want:      "0xf5e2da2896de9116a9463270defab5abd70be7be4722f57fd841079ded2c6cf6",
		},
		{
			rawDomain: "stage.Dev.Example.Com.",
			rawLocal:  "soul_researcher",
			want:      "0x803682e2e7629f07fe1c65670bf29bf19691a339e0252001a48737cfe22dd9f5",
		},
	}

	for _, c := range cases {
		d, err := domains.NormalizeDomain(c.rawDomain)
		if err != nil {
			t.Fatalf("NormalizeDomain(%q) err: %v", c.rawDomain, err)
		}
		local, err := NormalizeLocalAgentID(c.rawLocal)
		if err != nil {
			t.Fatalf("NormalizeLocalAgentID(%q) err: %v", c.rawLocal, err)
		}

		got, err := DeriveAgentIDHex(d, local)
		if err != nil {
			t.Fatalf("DeriveAgentIDHex(%q,%q) err: %v", d, local, err)
		}
		if got != c.want {
			t.Fatalf("DeriveAgentIDHex(%q,%q) got=%q want=%q", d, local, got, c.want)
		}
	}
}

