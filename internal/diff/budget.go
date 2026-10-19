package diff

import (
	"fmt"
	"strings"
)

// EstimateTokens approximates the token count of s without a tokenizer:
// ~4 bytes per token for ASCII (code, English) and ~1 token per rune for
// everything else (Thai, CJK), which errs on the high side. Good enough to
// decide whether a diff fits; never used for billing.
func EstimateTokens(s string) int {
	ascii, other := 0, 0
	for _, r := range s {
		if r < 128 {
			ascii++
		} else {
			other++
		}
	}
	return (ascii+3)/4 + other
}

// Truncate cuts s to roughly maxTokens at a line boundary and appends a
// marker saying how many lines were dropped.
func Truncate(s string, maxTokens int) (string, bool) {
	if EstimateTokens(s) <= maxTokens {
		return s, false
	}
	lines := strings.SplitAfter(s, "\n")
	var b strings.Builder
	used := 0
	for i, l := range lines {
		t := EstimateTokens(l)
		if used+t > maxTokens {
			fmt.Fprintf(&b, "... [truncated: %d more lines]\n", len(lines)-i)
			return b.String(), true
		}
		b.WriteString(l)
		used += t
	}
	return b.String(), false
}

// Chunks groups whole files into texts of at most maxTokens each, for the
// map step of map-reduce. A file larger than maxTokens on its own becomes a
// single truncated chunk.
func Chunks(files []File, maxTokens int) []string {
	var (
		out  []string
		cur  strings.Builder
		used int
	)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
			used = 0
		}
	}
	for i := range files {
		text := files[i].String()
		t := EstimateTokens(text)
		if t > maxTokens {
			flush()
			tr, _ := Truncate(text, maxTokens)
			out = append(out, tr)
			continue
		}
		if used+t > maxTokens {
			flush()
		}
		cur.WriteString(text)
		used += t
	}
	flush()
	return out
}

// Stat is a compact one-line-per-file summary ("M  12+ 3-  path").
func Stat(files []File) string {
	var b strings.Builder
	for i := range files {
		f := &files[i]
		code := map[Status]string{Added: "A", Deleted: "D", Renamed: "R", Modified: "M"}[f.Status]
		name := f.Path()
		if f.Status == Renamed && f.OldPath != f.NewPath {
			name = f.OldPath + " → " + f.NewPath
		}
		fmt.Fprintf(&b, "%s %4d+ %4d-  %s\n", code, f.Added, f.Deleted, name)
	}
	return b.String()
}
