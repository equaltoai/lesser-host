package soulsearch

import (
	"sort"
	"strings"
	"unicode"
)

const maxBoundaryKeywords = 50

var boundaryStopwords = map[string]struct{}{
	"a":        {},
	"an":       {},
	"and":      {},
	"are":      {},
	"as":       {},
	"at":       {},
	"be":       {},
	"but":      {},
	"by":       {},
	"can":      {},
	"cannot":   {},
	"cant":     {},
	"could":    {},
	"did":      {},
	"do":       {},
	"does":     {},
	"dont":     {},
	"for":      {},
	"from":     {},
	"has":      {},
	"have":     {},
	"i":        {},
	"if":       {},
	"in":       {},
	"is":       {},
	"it":       {},
	"may":      {},
	"me":       {},
	"must":     {},
	"no":       {},
	"not":      {},
	"of":       {},
	"on":       {},
	"or":       {},
	"our":      {},
	"should":   {},
	"that":     {},
	"the":      {},
	"their":    {},
	"then":     {},
	"these":    {},
	"they":     {},
	"this":     {},
	"to":       {},
	"was":      {},
	"we":       {},
	"were":     {},
	"will":     {},
	"with":     {},
	"without":  {},
	"wont":     {},
	"would":    {},
	"you":      {},
	"your":     {},
	"yours":    {},
	"yourself": {},
}

func NormalizeBoundaryKeyword(raw string) (string, bool) {
	if kw, ok := normalizeSimpleKeyword(raw); ok {
		return kw, true
	}
	toks := tokenize(raw)
	if len(toks) != 1 {
		return "", false
	}
	return toks[0], true
}

func ExtractBoundaryKeywords(category, statement, rationale string) []string {
	set := map[string]struct{}{}
	add := func(tok string) {
		if strings.TrimSpace(tok) == "" {
			return
		}
		set[tok] = struct{}{}
	}

	if kw, ok := normalizeSimpleKeyword(category); ok {
		add(kw)
	}
	for _, tok := range tokenize(category) {
		add(tok)
	}
	for _, tok := range tokenize(statement) {
		add(tok)
	}
	for _, tok := range tokenize(rationale) {
		add(tok)
	}

	out := make([]string, 0, len(set))
	for tok := range set {
		out = append(out, tok)
	}
	sort.Strings(out)
	if len(out) > maxBoundaryKeywords {
		out = out[:maxBoundaryKeywords]
	}
	return out
}

func normalizeSimpleKeyword(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	raw = strings.ToLower(raw)
	if len(raw) < 2 {
		return "", false
	}
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			continue
		}
		return "", false
	}
	return raw, true
}

func tokenize(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	raw = strings.ToLower(raw)

	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}

	fields := strings.Fields(b.String())
	if len(fields) == 0 {
		return nil
	}

	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < 2 {
			continue
		}
		if _, ok := boundaryStopwords[f]; ok {
			continue
		}
		out = append(out, f)
	}
	return out
}
