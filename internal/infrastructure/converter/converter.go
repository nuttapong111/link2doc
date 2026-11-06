// Package converter turns an HTML page into clean Markdown: decode the charset
// (including Thai TIS-620), apply site rules, pick the main article, convert.
package converter

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/strikethrough"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"github.com/PuerkitoBio/goquery"
	readability "github.com/go-shiori/go-readability"
	"github.com/nuttapong111/link2doc/internal/domain"
	"golang.org/x/net/html/charset"
)

type Converter struct{}

func New() *Converter { return &Converter{} }

var metaCharset = regexp.MustCompile(`(?i)<meta[^>]+charset=["']?([\w-]+)`)

// Decode converts the body to UTF-8 using the header charset, then <meta charset>.
func (Converter) Decode(body []byte, contentType string) string {
	cs := ""
	if m := regexp.MustCompile(`(?i)charset=([\w-]+)`).FindStringSubmatch(contentType); m != nil {
		cs = m[1]
	} else if m := metaCharset.FindSubmatch(body[:min(len(body), 4096)]); m != nil {
		cs = string(m[1])
	}
	cs = strings.ToLower(cs)
	if cs == "tis-620" || cs == "tis620" || cs == "iso-8859-11" {
		cs = "windows-874"
	}
	if cs == "" || cs == "utf-8" || cs == "utf8" {
		return string(body)
	}
	enc, _ := charset.Lookup(cs)
	if enc == nil {
		return string(body)
	}
	out, err := enc.NewDecoder().Bytes(body)
	if err != nil {
		return string(body)
	}
	return string(out)
}

func splitSel(s *string) []string {
	var out []string
	for _, p := range strings.Split(domain.Str(s), ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func absolutize(doc *goquery.Document, base *url.URL) {
	for sel, attr := range map[string]string{"a[href]": "href", "img[src]": "src"} {
		doc.Find(sel).Each(func(_ int, s *goquery.Selection) {
			if v, ok := s.Attr(attr); ok {
				if u, err := base.Parse(v); err == nil {
					s.SetAttr(attr, u.String())
				}
			}
		})
	}
}

func applyRule(doc *goquery.Document, rule *domain.SiteRule) {
	if rule == nil {
		return
	}
	for _, sel := range splitSel(rule.ExcludeSel) {
		doc.Find(sel).Remove()
	}
	var keep []string
	for _, sel := range splitSel(rule.IncludeSel) {
		doc.Find(sel).Each(func(_ int, s *goquery.Selection) {
			if h, err := goquery.OuterHtml(s); err == nil {
				keep = append(keep, h)
			}
		})
	}
	if len(keep) > 0 {
		doc.Find("body").SetHtml("<main>" + strings.Join(keep, "") + "</main>")
	}
}

var (
	multiNL     = regexp.MustCompile(`\n{3,}`)
	spaces      = regexp.MustCompile(`\s+`)
	mdSyntax    = regexp.MustCompile("[#*_`>|\\-\\[\\]()]")
	linkPair    = regexp.MustCompile(`\[(https?://[^\]]+)\]\((https?://[^)]+)\)`)
	nestedTable = regexp.MustCompile(`(?is)<table[^>]*>.*?<table`)
)

func (c Converter) ToMarkdown(html, pageURL string, o domain.ConvertOptions) (*domain.Converted, error) {
	base, err := url.Parse(pageURL)
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, err
	}
	absolutize(doc, base)
	applyRule(doc, o.Rule)
	doc.Find("script,style,noscript,iframe,form,button,svg").Remove()

	var warnings []string
	title, _ := doc.Find(`meta[property="og:title"]`).Attr("content")
	if title == "" {
		title = doc.Find("title").First().Text()
	}

	contentHTML := ""
	if o.MainOnly && (o.Rule == nil || len(splitSel(o.Rule.IncludeSel)) == 0) {
		full, _ := doc.Html()
		art, err := readability.FromReader(strings.NewReader(full), base)
		if err == nil && len(strings.TrimSpace(art.TextContent)) > 200 {
			contentHTML = art.Content
			if art.Title != "" {
				title = art.Title
			}
		} else {
			warnings = append(warnings, "ตัดเนื้อหาหลักไม่ได้ ใช้ทั้งหน้าแทน")
		}
	}
	if contentHTML == "" {
		for _, sel := range []string{"main", "article", "body"} {
			if s := doc.Find(sel).First(); s.Length() > 0 {
				contentHTML, _ = s.Html()
				break
			}
		}
	}

	// Strip images / links before converting, when the user asked for that.
	cdoc, _ := goquery.NewDocumentFromReader(strings.NewReader(contentHTML))
	if !o.KeepImages {
		cdoc.Find("img,picture,figure > figcaption:empty").Remove()
	}
	if !o.KeepLinks {
		cdoc.Find("a").Each(func(_ int, s *goquery.Selection) { s.ReplaceWithSelection(s.Contents()) })
	}
	cleanHTML, _ := cdoc.Find("body").Html()

	md, err := newMD().ConvertString(cleanHTML, htmltomd.WithDomain(base.Scheme+"://"+base.Host))
	if err != nil {
		return nil, fmt.Errorf("แปลง Markdown ไม่สำเร็จ: %w", err)
	}
	md = strings.TrimSpace(multiNL.ReplaceAllString(md, "\n\n"))
	title = strings.TrimSpace(spaces.ReplaceAllString(title, " "))

	if nestedTable.MatchString(contentHTML) {
		warnings = append(warnings, "มีตารางซ้อนตาราง อาจแตก")
	}
	if n := len([]rune(title)); n > 0 && n < 6 {
		warnings = append(warnings, "ชื่อเรื่องอาจถูกตัด")
	}
	mismatch := 0
	for _, m := range linkPair.FindAllStringSubmatch(md, -1) {
		a, e1 := url.Parse(m[1])
		b, e2 := url.Parse(m[2])
		if e1 == nil && e2 == nil && a.Hostname() != b.Hostname() {
			mismatch++
		}
	}
	if mismatch > 0 {
		warnings = append(warnings, fmt.Sprintf("ลิงก์ %d จุดที่ข้อความกับ URL ไม่ตรงกัน", mismatch))
	}
	textLen := len([]rune(strings.TrimSpace(mdSyntax.ReplaceAllString(md, ""))))
	return &domain.Converted{Title: title, Markdown: md, TextLength: textLen, Warnings: warnings}, nil
}

func newMD() *htmltomd.Converter {
	return htmltomd.NewConverter(htmltomd.WithPlugins(
		base.NewBasePlugin(),
		commonmark.NewCommonmarkPlugin(commonmark.WithBulletListMarker("-"), commonmark.WithEmDelimiter("*"), commonmark.WithListEndComment(false)),
		table.NewTablePlugin(),
		strikethrough.NewStrikethroughPlugin(),
	))
}
