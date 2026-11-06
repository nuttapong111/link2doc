package google

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestMarkdownToRequestsThaiIndexes(t *testing.T) {
	md := "---\ntitle: ทดสอบ\n---\n\n# หัวข้อ 😀\n\nข้อความ **ตัวหนา** และ [ลิงก์](https://example.com)\n\n- หนึ่ง\n- สอง\n\n| a | b |\n|---|---|\n| 1 | 2 |\n"
	r := MarkdownToRequests(md, 1, "t.1", false)
	text := r.Insert[0].InsertText.Text
	n := int64(len(utf16.Encode([]rune(text))))
	if r.Length != n {
		t.Fatalf("length %d, utf16 %d", r.Length, n)
	}
	for _, want := range []string{"title: ทดสอบ\n", "หัวข้อ 😀\n", "ตัวหนา", "หนึ่ง\nสอง\n", "a  |  b\n1  |  2\n"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %q", want, text)
		}
	}
	var bold, link, bullets, heading bool
	for _, q := range r.Style {
		if s := q.UpdateTextStyle; s != nil {
			got := string(utf16.Decode(utf16.Encode([]rune(text))[s.Range.StartIndex-1 : s.Range.EndIndex-1]))
			if s.Fields == "bold" && got == "ตัวหนา" {
				bold = true
			}
			if s.Fields == "link" && got == "ลิงก์" && s.TextStyle.Link.Url == "https://example.com" {
				link = true
			}
			if s.Range.TabId != "t.1" {
				t.Errorf("missing tab id")
			}
		}
		if q.CreateParagraphBullets != nil {
			bullets = true
		}
		if p := q.UpdateParagraphStyle; p != nil && p.ParagraphStyle.NamedStyleType == "HEADING_1" {
			heading = true
		}
	}
	if !bold || !link || !bullets || !heading {
		t.Fatalf("bold=%v link=%v bullets=%v heading=%v", bold, link, bullets, heading)
	}
}

func TestRaw(t *testing.T) {
	r := MarkdownToRequests("# a", 5, "", true)
	if r.Insert[0].InsertText.Text != "# a\n" || r.Insert[0].InsertText.Location.Index != 5 || len(r.Style) != 0 {
		t.Fatal("raw mode should insert text as is")
	}
}
