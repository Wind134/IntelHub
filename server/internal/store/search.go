package store

import (
	"strings"
	"unicode"
)

// BuildCJKSearchTokens generates space-separated CJK unigrams + bigrams alongside normalized
// ASCII/alphanumeric words so SQLite FTS5 unicode61 matches Chinese substrings and tickers cleanly.
func BuildCJKSearchTokens(parts ...string) string {
	seen := make(map[string]struct{}, 64)
	var tokens []string
	add := func(tok string) {
		if tok == "" {
			return
		}
		if _, ok := seen[tok]; ok {
			return
		}
		seen[tok] = struct{}{}
		tokens = append(tokens, tok)
	}

	for _, part := range parts {
		runes := []rune(strings.ToLower(part))
		var asciiBuf strings.Builder
		var cjkRun []rune

		flushASCII := func() {
			if asciiBuf.Len() > 0 {
				add(asciiBuf.String())
				asciiBuf.Reset()
			}
		}
		flushCJK := func() {
			if len(cjkRun) == 0 {
				return
			}
			for i, r := range cjkRun {
				add(string(r))
				if i+1 < len(cjkRun) {
					add(string(cjkRun[i : i+2]))
				}
			}
			cjkRun = cjkRun[:0]
		}

		for _, r := range runes {
			switch {
			case isCJK(r):
				flushASCII()
				cjkRun = append(cjkRun, r)
			case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.':
				flushCJK()
				asciiBuf.WriteRune(r)
			default:
				flushASCII()
				flushCJK()
			}
		}
		flushASCII()
		flushCJK()
	}

	return strings.Join(tokens, " ")
}

// BuildFTSQuery converts a user query string (mixed Chinese/English/tickers) into a safe FTS5 MATCH query.
func BuildFTSQuery(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	fields := strings.Fields(raw)
	var clauses []string
	for _, field := range fields {
		upper := strings.ToUpper(field)
		if upper == "OR" || upper == "AND" || upper == "NOT" {
			clauses = append(clauses, upper)
			continue
		}
		runes := []rune(strings.ToLower(field))
		hasCJK := false
		for _, r := range runes {
			if isCJK(r) {
				hasCJK = true
				break
			}
		}
		if !hasCJK {
			clean := sanitizeFTSTerm(field)
			if clean != "" {
				clauses = append(clauses, `"`+clean+`"*`)
			}
			continue
		}

		// For CJK phrases, require all bigrams (or unigram if length 1) to match search_tokens.
		var sub []string
		var cjkRun []rune
		var asciiBuf strings.Builder
		flushASCII := func() {
			if asciiBuf.Len() > 0 {
				clean := sanitizeFTSTerm(asciiBuf.String())
				if clean != "" {
					sub = append(sub, `"`+clean+`"*`)
				}
				asciiBuf.Reset()
			}
		}
		flushCJK := func() {
			if len(cjkRun) == 0 {
				return
			}
			if len(cjkRun) == 1 {
				sub = append(sub, `"`+string(cjkRun[0])+`"`)
			} else {
				for i := 0; i < len(cjkRun)-1; i++ {
					sub = append(sub, `"`+string(cjkRun[i:i+2])+`"`)
				}
			}
			cjkRun = cjkRun[:0]
		}
		for _, r := range runes {
			if isCJK(r) {
				flushASCII()
				cjkRun = append(cjkRun, r)
			} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
				flushCJK()
				asciiBuf.WriteRune(r)
			} else {
				flushASCII()
				flushCJK()
			}
		}
		flushASCII()
		flushCJK()
		if len(sub) > 0 {
			clauses = append(clauses, "("+strings.Join(sub, " AND ")+")")
		}
	}
	if len(clauses) == 0 {
		return ""
	}
	// Strip leading/trailing operators if user typed them accidentally.
	for len(clauses) > 0 && (clauses[0] == "OR" || clauses[0] == "AND" || clauses[0] == "NOT") {
		clauses = clauses[1:]
	}
	for len(clauses) > 0 && (clauses[len(clauses)-1] == "OR" || clauses[len(clauses)-1] == "AND" || clauses[len(clauses)-1] == "NOT") {
		clauses = clauses[:len(clauses)-1]
	}
	if len(clauses) == 0 {
		return ""
	}
	// Explicitly join adjacent non-operator terms with AND (required by FTS5 when parenthesized groups are adjacent).
	var out []string
	for _, c := range clauses {
		isOp := c == "OR" || c == "AND" || c == "NOT"
		if len(out) > 0 {
			prev := out[len(out)-1]
			prevIsOp := prev == "OR" || prev == "AND" || prev == "NOT"
			if !isOp && !prevIsOp {
				out = append(out, "AND")
			}
		}
		out = append(out, c)
	}
	return strings.Join(out, " ")
}

func sanitizeFTSTerm(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0xF900 && r <= 0xFAFF)
}
