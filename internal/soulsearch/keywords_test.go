package soulsearch

import "testing"

func TestNormalizeBoundaryKeyword(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw string
		ok  bool
		kw  string
	}{
		{raw: "refusal", ok: true, kw: "refusal"},
		{raw: " Scope_Limit ", ok: true, kw: "scope_limit"},
		{raw: "a", ok: false},
		{raw: "two words", ok: false},
		{raw: "!!!", ok: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()

			got, ok := NormalizeBoundaryKeyword(tc.raw)
			if ok != tc.ok || got != tc.kw {
				t.Fatalf("got (%q,%v), want (%q,%v)", got, ok, tc.kw, tc.ok)
			}
		})
	}
}

func TestExtractBoundaryKeywords(t *testing.T) {
	t.Parallel()

	got := ExtractBoundaryKeywords(
		"Scope_Limit",
		"I will not share credentials or secrets, and I refuse unsafe requests.",
		"Protect users from credential theft.",
	)
	if len(got) == 0 {
		t.Fatalf("expected keywords")
	}
	if got[0] != "credential" && got[0] != "credentials" {
		// Sort order is deterministic but token stemming is not applied.
		found := false
		for _, kw := range got {
			if kw == "credentials" || kw == "refuse" || kw == "unsafe" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("unexpected keywords: %#v", got)
		}
	}

	long := ""
	for i := 0; i < 80; i++ {
		long += "token" + string(rune('a'+(i%26))) + " "
	}
	many := ExtractBoundaryKeywords("", long, "")
	if len(many) > maxBoundaryKeywords {
		t.Fatalf("expected keyword clamp, got %d", len(many))
	}
}

func TestTokenizeAndNormalizeHelpers(t *testing.T) {
	t.Parallel()

	if kw, ok := normalizeSimpleKeyword("A"); ok || kw != "" {
		t.Fatalf("expected short keyword rejection")
	}
	if kw, ok := normalizeSimpleKeyword("two words"); ok || kw != "" {
		t.Fatalf("expected spaced keyword rejection")
	}
	if kw, ok := normalizeSimpleKeyword("User_ID1"); !ok || kw != "user_id1" {
		t.Fatalf("unexpected normalized keyword: %q %v", kw, ok)
	}

	toks := tokenize("I will not dox users; keep audits on-call.")
	if len(toks) == 0 {
		t.Fatalf("expected tokens")
	}
	for _, tok := range toks {
		if tok == "i" || tok == "will" {
			t.Fatalf("expected stopwords removed: %#v", toks)
		}
	}
	if toks := tokenize("   "); toks != nil {
		t.Fatalf("expected nil tokens for empty input")
	}
}
