package memory

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// rawTokenRe matches identifier/path-shaped runs: letters, digits, and the
// separators that appear inside code identifiers, paths, and flags.
var rawTokenRe = regexp.MustCompile(`[A-Za-z0-9_./\\:@-]+`)

// stopwords excluded from normalized token sets and FTS queries. Small on
// purpose: code identifiers are the high-signal tokens and rarely collide
// with these.
var stopwords = map[string]struct{}{
	"the": {}, "a": {}, "an": {}, "and": {}, "or": {}, "to": {}, "of": {},
	"in": {}, "is": {}, "are": {}, "for": {}, "on": {}, "with": {},
	"this": {}, "that": {}, "it": {}, "be": {}, "as": {}, "at": {},
	"by": {}, "we": {}, "i": {}, "you": {}, "was": {}, "were": {},
	"do": {}, "does": {}, "did": {}, "not": {}, "no": {}, "yes": {},
	"can": {}, "should": {}, "would": {}, "could": {}, "have": {},
	"has": {}, "had": {}, "will": {}, "its": {}, "our": {}, "your": {},
	"from": {}, "into": {}, "than": {}, "then": {}, "them": {},
	"there": {}, "their": {}, "what": {}, "when": {}, "where": {},
	"which": {}, "how": {}, "why": {}, "all": {}, "any": {}, "some": {},
	"please": {}, "make": {}, "made": {}, "need": {}, "needs": {},
	"use": {}, "used": {}, "using": {}, "also": {}, "just": {},
	"now": {}, "new": {}, "get": {}, "got": {}, "let": {}, "lets": {},
	"about": {}, "after": {}, "before": {}, "only": {}, "same": {},
	"other": {}, "more": {}, "most": {}, "each": {}, "here": {},
	"want": {}, "wants": {}, "like": {}, "must": {}, "never": {},
	"always": {}, "still": {}, "very": {}, "over": {}, "under": {},
	"out": {}, "up": {}, "down": {}, "so": {}, "if": {}, "but": {},
	"because": {}, "these": {}, "those": {}, "been": {}, "being": {},
	"my": {}, "me": {}, "us": {}, "his": {}, "her": {}, "they": {},
	"he": {}, "she": {},
}

// splitCamel splits a single identifier run at camelCase boundaries:
// "moveWordLeft" -> ["move", "word", "left"], "LSPManager" -> ["lsp",
// "manager"]. Returns lowercase parts.
func splitCamel(token string) []string {
	if token == "" {
		return nil
	}
	runes := []rune(token)
	var parts []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		boundary := false
		if unicode.IsLower(prev) && unicode.IsUpper(cur) {
			boundary = true
		} else if i+1 < len(runes) &&
			unicode.IsUpper(prev) && unicode.IsUpper(cur) && unicode.IsLower(runes[i+1]) {
			// "LSPManager": boundary between "LSP" and "Manager".
			boundary = true
		} else if (unicode.IsLetter(prev) && unicode.IsDigit(cur)) ||
			(unicode.IsDigit(prev) && unicode.IsLetter(cur)) {
			boundary = true
		}
		if boundary {
			parts = append(parts, strings.ToLower(string(runes[start:i])))
			start = i
		}
	}
	parts = append(parts, strings.ToLower(string(runes[start:])))
	return parts
}

// tokenSet expands text into a deduplicated set of lowercase search tokens:
// each raw identifier run plus its separator-split and camel-split parts.
func tokenSet(text string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, raw := range rawTokenRe.FindAllString(text, -1) {
		// Trim edge punctuation so "mode." and "mode" normalize identically
		// (inner separators stay: paths remain whole tokens).
		lower := strings.ToLower(strings.Trim(raw, "._/\\:@-"))
		addToken(set, lower)
		for _, seg := range strings.FieldsFunc(raw, func(r rune) bool {
			switch r {
			case '_', '.', '/', '\\', ':', '@', '-':
				return true
			}
			return false
		}) {
			addToken(set, strings.ToLower(seg))
			for _, part := range splitCamel(seg) {
				addToken(set, part)
			}
		}
	}
	return set
}

func addToken(set map[string]struct{}, token string) {
	if len(token) < 2 {
		return
	}
	if _, stop := stopwords[token]; stop {
		return
	}
	set[token] = struct{}{}
}

// NormalizeText produces the sorted, space-joined token set for the FTS
// shadow column. It makes camelCase/snake_case/path identifiers searchable
// by their parts (the default unicode61 tokenizer would not split them).
func NormalizeText(parts ...string) string {
	set := make(map[string]struct{})
	for _, part := range parts {
		for token := range tokenSet(part) {
			set[token] = struct{}{}
		}
	}
	if len(set) == 0 {
		return ""
	}
	tokens := make([]string, 0, len(set))
	for token := range set {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return strings.Join(tokens, " ")
}

// maxQueryTokens bounds FTS query size; more terms add noise, not recall.
const maxQueryTokens = 24

// BuildMatchQuery converts free text (plus extra context strings) into a
// safe FTS5 OR-query over normalized tokens. Returns "" when nothing
// searchable remains; callers must treat that as "no query", not match-all.
func BuildMatchQuery(text string, extra ...string) string {
	set := tokenSet(text)
	for _, e := range extra {
		for token := range tokenSet(e) {
			set[token] = struct{}{}
		}
	}
	if len(set) == 0 {
		return ""
	}
	tokens := make([]string, 0, len(set))
	for token := range set {
		tokens = append(tokens, token)
	}
	// Longest first: identifiers beat generic words when truncating.
	sort.Slice(tokens, func(i, j int) bool {
		if len(tokens[i]) != len(tokens[j]) {
			return len(tokens[i]) > len(tokens[j])
		}
		return tokens[i] < tokens[j]
	})
	if len(tokens) > maxQueryTokens {
		tokens = tokens[:maxQueryTokens]
	}
	quoted := make([]string, len(tokens))
	for i, token := range tokens {
		quoted[i] = `"` + strings.ReplaceAll(token, `"`, `""`) + `"`
	}
	return strings.Join(quoted, " OR ")
}

// entityRe finds entity-shaped tokens: contains a path separator, an
// underscore, a dot inside the token, or a camelCase hump.
var entityCamelRe = regexp.MustCompile(`\p{Ll}\p{Lu}`)

// maxEntities bounds mechanical entity extraction per record.
const maxEntities = 24

// ExtractEntities mechanically pulls code-shaped entities (paths, symbols,
// commands, flags) out of text, preserving original casing for display.
func ExtractEntities(parts ...string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, text := range parts {
		for _, raw := range rawTokenRe.FindAllString(text, -1) {
			if len(out) >= maxEntities {
				return out
			}
			if len(raw) < 4 || len(raw) > 128 {
				continue
			}
			trimmed := strings.Trim(raw, ".-:@")
			if len(trimmed) < 4 {
				continue
			}
			if !isEntityShaped(trimmed) {
				continue
			}
			key := strings.ToLower(trimmed)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, trimmed)
		}
	}
	return out
}

func isEntityShaped(token string) bool {
	if strings.ContainsAny(token, "/\\") {
		return true
	}
	if strings.Contains(token, "_") {
		return true
	}
	inner := token
	if len(inner) > 2 {
		inner = token[1 : len(token)-1]
	}
	if strings.Contains(inner, ".") {
		return true
	}
	return entityCamelRe.MatchString(token)
}

// EstimateTokens is a cheap LLM-token estimate (~4 bytes/token) used for
// recall budget packing. It intentionally matches the coarse estimators
// used elsewhere; exactness is not required, headroom is.
func EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return len(s)/4 + 1
}

// PathOverlap scores how strongly a record's scope (entities + scope paths)
// overlaps the query's context paths. Returns 0..1.
func PathOverlap(recordTokens map[string]struct{}, queryPaths []string) float64 {
	if len(queryPaths) == 0 || len(recordTokens) == 0 {
		return 0
	}
	hits := 0
	for _, path := range queryPaths {
		matched := false
		for token := range tokenSet(path) {
			if len(token) < 3 {
				continue
			}
			if _, ok := recordTokens[token]; ok {
				matched = true
				break
			}
		}
		if matched {
			hits++
		}
	}
	return float64(hits) / float64(len(queryPaths))
}

// TruncateBytes trims s to at most n bytes without splitting a rune.
func TruncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
