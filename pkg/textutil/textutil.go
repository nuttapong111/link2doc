// Package textutil holds small pure helpers shared by the use cases and adapters.
package textutil

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func SHA256(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// RenderHeader fills the front-matter template.
func RenderHeader(tpl, title, url, fetchedAt string) string {
	if tpl == "" {
		tpl = "---\ntitle: {title}\nsource_url: {url}\nfetched_at: {fetched_at}\n---"
	}
	r := strings.NewReplacer("{title}", title, "{url}", url, "{fetched_at}", fetchedAt)
	return r.Replace(tpl)
}

// CountWords approximates a word count. Thai has no spaces between words, so a run of
// Thai letters counts as one word per ~6 characters (a typical Thai word length).
func CountWords(md string) int {
	n := 0
	for _, f := range strings.Fields(md) {
		thai, other := 0, false
		for _, r := range f {
			if r >= 0x0E00 && r <= 0x0E7F {
				thai++
			} else if r > ' ' && !strings.ContainsRune("#*_`>|-[]()!.,:;", r) {
				other = true
			}
		}
		switch {
		case thai > 0:
			n += max(1, (thai+5)/6)
		case other:
			n++
		}
	}
	return n
}

var titleSuffix = regexp.MustCompile(`^(.{8,}?)\s+[|\-–—]\s+[^|\-–—]{2,}$`)

// TabTitle turns "Page name | Site name" into "Page name" (max 80 characters).
func TabTitle(t string) string {
	t = strings.TrimSpace(t)
	if m := titleSuffix.FindStringSubmatch(t); m != nil {
		t = m[1]
	}
	r := []rune(strings.TrimSpace(t))
	if len(r) > 80 {
		r = r[:80]
	}
	return string(r)
}

var (
	allPages = regexp.MustCompile(`(?i)ทั้งหมด|all`)
	rangeRe  = regexp.MustCompile(`^\s*(\d+)\s*(?:-\s*(\d+))?\s*$`)
)

// ParsePageRange turns "1-5, 8, 10-12" (or "" / "ทั้งหมด") into page numbers within 1..total.
func ParsePageRange(input string, total int) []int {
	s := strings.TrimSpace(input)
	if s == "" || allPages.MatchString(s) {
		out := make([]int, total)
		for i := range out {
			out[i] = i + 1
		}
		return out
	}
	set := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		m := rangeRe.FindStringSubmatch(part)
		if m == nil {
			continue
		}
		a, _ := strconv.Atoi(m[1])
		b := a
		if m[2] != "" {
			b, _ = strconv.Atoi(m[2])
		}
		if a > b {
			a, b = b, a
		}
		for p := max(1, a); p <= min(total, b); p++ {
			set[p] = true
		}
	}
	out := make([]int, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}
