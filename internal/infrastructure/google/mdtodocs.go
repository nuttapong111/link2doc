package google

import (
	"bytes"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	docs "google.golang.org/api/docs/v1"
)

// Markdown -> Google Docs batchUpdate requests.
// The whole plain text is inserted with one insertText; paragraph styles, text
// styles and bullets are applied afterwards over remembered ranges.
// Google Docs indexes count UTF-16 code units, so every position here does too.

var md = goldmark.New(goldmark.WithExtensions(extension.GFM))

var frontMatter = regexp.MustCompile(`(?s)^---\n(.*?)\n---\n?`)

type span struct {
	start, end int64
	named      string
	style      *docs.TextStyle
	fields     string
	ordered    bool
}

type model struct {
	buf     strings.Builder
	pos     int64
	paras   []span
	styles  []span
	bullets []span
	src     []byte
}

func u16(s string) int64 { return int64(len(utf16.Encode([]rune(s)))) }

func (m *model) add(s string) {
	m.buf.WriteString(s)
	m.pos += u16(s)
}

func (m *model) style(start int64, st *docs.TextStyle, fields string) {
	if m.pos > start {
		m.styles = append(m.styles, span{start: start, end: m.pos, style: st, fields: fields})
	}
}

var mono = func() *docs.TextStyle {
	return &docs.TextStyle{WeightedFontFamily: &docs.WeightedFontFamily{FontFamily: "Roboto Mono"}}
}

func unescape(b []byte) string {
	b = util.UnescapePunctuations(b)
	b = util.ResolveNumericReferences(b)
	b = util.ResolveEntityNames(b)
	return string(b)
}

var tagRe = regexp.MustCompile(`<[^>]+>`)

func (m *model) inline(n ast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		start := m.pos
		switch t := c.(type) {
		case *ast.Text:
			m.add(unescape(t.Segment.Value(m.src)))
			if t.HardLineBreak() {
				m.add("\u000b") // vertical tab = soft line break inside a Docs paragraph
			} else if t.SoftLineBreak() {
				m.add(" ")
			}
		case *ast.String:
			m.add(string(t.Value))
		case *ast.CodeSpan:
			var b bytes.Buffer
			for g := t.FirstChild(); g != nil; g = g.NextSibling() {
				if tx, ok := g.(*ast.Text); ok {
					b.Write(tx.Segment.Value(m.src))
				}
			}
			m.add(b.String())
			m.style(start, mono(), "weightedFontFamily")
		case *ast.Emphasis:
			m.inline(t)
			if t.Level >= 2 {
				m.style(start, &docs.TextStyle{Bold: true}, "bold")
			} else {
				m.style(start, &docs.TextStyle{Italic: true}, "italic")
			}
		case *east.Strikethrough:
			m.inline(t)
			m.style(start, &docs.TextStyle{Strikethrough: true}, "strikethrough")
		case *ast.Link:
			m.inline(t)
			dest := string(t.Destination)
			if m.pos == start {
				m.add(dest)
			}
			if strings.HasPrefix(dest, "http") {
				m.style(start, &docs.TextStyle{Link: &docs.Link{Url: dest}}, "link")
			}
		case *ast.AutoLink:
			u := string(t.URL(m.src))
			m.add(string(t.Label(m.src)))
			if strings.HasPrefix(u, "http") {
				m.style(start, &docs.TextStyle{Link: &docs.Link{Url: u}}, "link")
			}
		case *ast.Image:
			alt := plain(t, m.src)
			if alt != "" {
				m.add("[รูป: " + alt + "]")
			} else {
				m.add("[รูป]")
			}
			if d := string(t.Destination); strings.HasPrefix(d, "http") {
				m.style(start, &docs.TextStyle{Link: &docs.Link{Url: d}}, "link")
			}
		case *ast.RawHTML:
			var b bytes.Buffer
			for i := 0; i < t.Segments.Len(); i++ {
				seg := t.Segments.At(i)
				b.Write(seg.Value(m.src))
			}
			if strings.HasPrefix(strings.ToLower(b.String()), "<br") {
				m.add("\u000b")
			} else {
				m.add(unescape([]byte(tagRe.ReplaceAllString(b.String(), ""))))
			}
		case *east.TaskCheckBox:
			if t.IsChecked {
				m.add("☑ ")
			} else {
				m.add("☐ ")
			}
		default:
			m.inline(c)
		}
	}
}

func plain(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch t := c.(type) {
			case *ast.Text:
				b.WriteString(unescape(t.Segment.Value(src)))
			case *ast.String:
				b.Write(t.Value)
			}
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

func (m *model) paragraph(n ast.Node, named string) {
	start := m.pos
	m.inline(n)
	m.add("\n")
	if named != "" {
		m.paras = append(m.paras, span{start: start, end: m.pos, named: named})
	}
}

func (m *model) plainPara(s string, st *docs.TextStyle, fields string) {
	start := m.pos
	m.add(s)
	if st != nil {
		m.style(start, st, fields)
	}
	m.add("\n")
}

func (m *model) list(l *ast.List) {
	for li := l.FirstChild(); li != nil; li = li.NextSibling() {
		start := m.pos
		var nested []*ast.List
		first := true
		for c := li.FirstChild(); c != nil; c = c.NextSibling() {
			if sub, ok := c.(*ast.List); ok {
				nested = append(nested, sub)
				continue
			}
			if !first {
				m.add(" ")
			}
			first = false
			m.inline(c)
		}
		m.add("\n")
		m.bullets = append(m.bullets, span{start: start, end: m.pos, ordered: l.IsOrdered()})
		for _, sub := range nested { // nesting is flattened to one level
			m.list(sub)
		}
	}
}

func lines(n ast.Node, src []byte) string {
	var b bytes.Buffer
	ls := n.Lines()
	for i := 0; i < ls.Len(); i++ {
		seg := ls.At(i)
		b.Write(seg.Value(src))
	}
	return b.String()
}

func (m *model) block(n ast.Node) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *ast.Heading:
			m.paragraph(t, "HEADING_"+string(rune('0'+min(t.Level, 6))))
		case *ast.Paragraph, *ast.TextBlock:
			m.paragraph(t, "")
		case *ast.List:
			m.list(t)
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			start := m.pos
			code := strings.TrimRight(lines(t, m.src), "\n")
			m.add(strings.ReplaceAll(code, "\n", "\u000b"))
			m.style(start, mono(), "weightedFontFamily")
			m.add("\n")
		case *ast.Blockquote:
			start := m.pos
			m.block(t)
			m.style(start, &docs.TextStyle{Italic: true}, "italic")
		case *east.Table:
			for row := t.FirstChild(); row != nil; row = row.NextSibling() {
				var cells []string
				for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
					cells = append(cells, strings.ReplaceAll(plain(cell, m.src), "|", "/"))
				}
				line := strings.Join(cells, "  |  ")
				if _, isHeader := row.(*east.TableHeader); isHeader {
					m.plainPara(line, &docs.TextStyle{Bold: true}, "bold")
				} else {
					m.plainPara(line, nil, "")
				}
			}
		case *ast.ThematicBreak:
			m.plainPara("────────────────────", nil, "")
		case *ast.HTMLBlock:
			if s := strings.TrimSpace(unescape([]byte(tagRe.ReplaceAllString(lines(t, m.src), "")))); s != "" {
				m.plainPara(s, nil, "")
			}
		default:
			if c.HasChildren() {
				m.block(c)
			}
		}
	}
}

func buildModel(markdown string) *model {
	m := &model{}
	if fm := frontMatter.FindStringSubmatch(markdown); fm != nil {
		start := m.pos
		m.add(strings.ReplaceAll(fm[1], "\n", "\u000b"))
		st := mono()
		st.FontSize = &docs.Dimension{Magnitude: 9, Unit: "PT"}
		st.ForegroundColor = &docs.OptionalColor{Color: &docs.Color{RgbColor: &docs.RgbColor{Red: 0.4, Green: 0.43, Blue: 0.42}}}
		m.style(start, st, "weightedFontFamily,fontSize,foregroundColor")
		m.add("\n")
		markdown = markdown[len(fm[0]):]
	}
	m.src = []byte(markdown)
	doc := md.Parser().Parse(text.NewReader(m.src))
	m.block(doc)
	if !strings.HasSuffix(m.buf.String(), "\n") {
		m.add("\n")
	}
	return m
}

// Requests holds the insert request and the styling requests (sent in chunks).
type Requests struct {
	Insert []*docs.Request
	Style  []*docs.Request
	Length int64
}

// MarkdownToRequests builds requests that insert markdown at index in tabId ("" = first tab).
func MarkdownToRequests(markdown string, index int64, tabID string, raw bool) Requests {
	loc := &docs.Location{Index: index, TabId: tabID}
	rng := func(s, e int64) *docs.Range {
		return &docs.Range{StartIndex: index + s, EndIndex: index + e, TabId: tabID}
	}
	if raw {
		t := markdown
		if !strings.HasSuffix(t, "\n") {
			t += "\n"
		}
		return Requests{Insert: []*docs.Request{{InsertText: &docs.InsertTextRequest{Location: loc, Text: t}}}, Length: u16(t)}
	}
	m := buildModel(markdown)
	text := m.buf.String()
	var style []*docs.Request
	// Reset to normal text first; inserted text inherits the style at the insertion point otherwise.
	style = append(style, &docs.Request{UpdateParagraphStyle: &docs.UpdateParagraphStyleRequest{
		Range: rng(0, m.pos), ParagraphStyle: &docs.ParagraphStyle{NamedStyleType: "NORMAL_TEXT"}, Fields: "namedStyleType"}})
	for _, p := range m.paras {
		style = append(style, &docs.Request{UpdateParagraphStyle: &docs.UpdateParagraphStyleRequest{
			Range: rng(p.start, p.end), ParagraphStyle: &docs.ParagraphStyle{NamedStyleType: p.named}, Fields: "namedStyleType"}})
	}
	for _, s := range m.styles {
		style = append(style, &docs.Request{UpdateTextStyle: &docs.UpdateTextStyleRequest{Range: rng(s.start, s.end), TextStyle: s.style, Fields: s.fields}})
	}
	// Merge consecutive bullet paragraphs of the same kind.
	var merged []span
	for _, b := range m.bullets {
		if n := len(merged); n > 0 && merged[n-1].end == b.start && merged[n-1].ordered == b.ordered {
			merged[n-1].end = b.end
		} else {
			merged = append(merged, b)
		}
	}
	for _, b := range merged {
		preset := "BULLET_DISC_CIRCLE_SQUARE"
		if b.ordered {
			preset = "NUMBERED_DECIMAL_ALPHA_ROMAN"
		}
		style = append(style, &docs.Request{CreateParagraphBullets: &docs.CreateParagraphBulletsRequest{Range: rng(b.start, b.end), BulletPreset: preset}})
	}
	return Requests{Insert: []*docs.Request{{InsertText: &docs.InsertTextRequest{Location: loc, Text: text}}}, Style: style, Length: m.pos}
}

// PlainText exposes the generated document text (used by tests).
func PlainText(markdown string) string { return buildModel(markdown).buf.String() }

// MarkdownToHTML is used for "one Doc per link": Drive converts the HTML into a Doc.
func MarkdownToHTML(markdown string) string {
	body := frontMatter.ReplaceAllString(markdown, "")
	var b bytes.Buffer
	_ = md.Convert([]byte(body), &b)
	return `<!doctype html><html><head><meta charset="utf-8"></head><body>` + b.String() + `</body></html>`
}
