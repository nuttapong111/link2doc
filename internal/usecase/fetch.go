package usecase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/nuttapong111/link2doc/internal/domain"
	"github.com/nuttapong111/link2doc/pkg/textutil"
)

const pdfBatch = 25 // pages per pdf-worker call, so progress can be reported

var (
	pdfURL  = regexp.MustCompile(`(?i)\.pdf($|[?#])`)
	htmlish = regexp.MustCompile(`(?i)html|xml|text/plain`)
)

type FetchService struct{ *Deps }

type converted struct {
	Title    string
	Markdown string // with header
	BodyHash string
	Warnings []string
	FinalURL string
	Via      string
}

func (s *FetchService) convertWeb(ctx context.Context, it *domain.Item, job *domain.Job) (*converted, error) {
	raw := domain.Str(it.URL)
	if pdfURL.MatchString(raw) {
		return s.convertPDF(ctx, it, job, nil)
	}
	u, _ := url.Parse(raw)
	s.Fetcher.WaitForHost(ctx, u.Hostname())
	res, err := s.Fetcher.Fetch(ctx, raw, "", 0)
	if err != nil {
		return nil, err
	}
	if strings.Contains(strings.ToLower(res.ContentType), "application/pdf") || pdfURL.MatchString(res.FinalURL) {
		it.URL = &res.FinalURL
		return s.convertPDF(ctx, it, job, res.Body)
	}
	if !htmlish.MatchString(res.ContentType) {
		return nil, domain.NewError(422, "ไม่รองรับไฟล์ชนิด "+strings.Split(res.ContentType, ";")[0])
	}
	fu, _ := url.Parse(res.FinalURL)
	acct, err := s.accountFor(ctx, job.UserID, false)
	if err != nil {
		return nil, err
	}
	rule, err := s.Settings.RuleForHost(ctx, acct, fu.Hostname())
	if err != nil {
		return nil, err
	}
	html := s.Converter.Decode(res.Body, res.ContentType)
	o := domain.ConvertOptions{MainOnly: job.Options.MainOnly, KeepImages: job.Options.KeepImages, KeepLinks: job.Options.KeepLinks, Rule: rule}
	out, err := s.Converter.ToMarkdown(html, res.FinalURL, o)
	if err != nil {
		return nil, err
	}
	via := "http"
	// Very little text usually means the page renders with JavaScript.
	if out.TextLength < 300 && s.Browser != nil && s.Browser.Available() {
		if h, final, err := s.Browser.Render(ctx, res.FinalURL); err == nil {
			if b, err := s.Converter.ToMarkdown(h, final, o); err == nil && b.TextLength > out.TextLength {
				out, via = b, "browser"
			}
		}
	}
	if out.TextLength < 30 {
		return nil, domain.NewError(422, "ไม่พบเนื้อหาในหน้านี้ (อาจต้องล็อกอิน หรือหน้าใช้ JavaScript)")
	}
	return &converted{Title: out.Title, Markdown: out.Markdown, Warnings: out.Warnings, FinalURL: res.FinalURL, Via: via}, nil
}

func (s *FetchService) convertPDF(ctx context.Context, it *domain.Item, job *domain.Job, body []byte) (*converted, error) {
	if !s.PDF.Enabled() {
		return nil, domain.NewError(503, "ยังไม่ได้เปิดบริการ pdf-worker")
	}
	fileID := domain.Str(it.FileID)
	if fileID == "" || body != nil {
		raw := domain.Str(it.URL)
		if body == nil {
			// PDF given by link: download here (SSRF-checked) and hand the bytes to pdf-worker.
			u, _ := url.Parse(raw)
			s.Fetcher.WaitForHost(ctx, u.Hostname())
			res, err := s.Fetcher.Fetch(ctx, raw, "application/pdf,*/*", 200<<20)
			if err != nil {
				return nil, err
			}
			body = res.Body
		}
		if !bytes.HasPrefix(body, []byte("%PDF-")) {
			return nil, domain.NewError(422, "ไฟล์จากลิงก์นี้ไม่ใช่ PDF")
		}
		name := "file.pdf"
		if u, err := url.Parse(raw); err == nil {
			if b := path.Base(u.Path); b != "" && b != "/" && b != "." {
				name, _ = url.PathUnescape(b)
			}
		}
		up, err := s.PDF.Upload(ctx, body, name)
		if err != nil {
			return nil, err
		}
		fileID = up.FileID
		it.FileID, it.FileName = &up.FileID, &up.Name
		if err := s.Items.Update(ctx, it.ID, map[string]any{"file_id": up.FileID, "file_name": up.Name}); err != nil {
			return nil, err
		}
	}
	info, err := s.PDF.Info(ctx, fileID)
	if err != nil {
		return nil, err
	}
	pages := textutil.ParsePageRange(domain.Str(it.PageRange), info.Pages)
	if len(pages) == 0 {
		return nil, domain.NewError(422, "ช่วงหน้าที่ระบุไม่อยู่ในไฟล์")
	}
	lang := job.Options.OCRLang
	if lang == "" {
		lang = "tha+eng"
	}
	var parts []string
	ocrPages, claudePages := 0, 0
	for i := 0; i < len(pages); i += pdfBatch {
		r, err := s.PDF.Convert(ctx, fileID, domain.PDFConvertRequest{
			Pages: pages[i:min(i+pdfBatch, len(pages))], OCR: job.Options.OCR, Lang: lang,
			StripHeaders: job.Options.StripHeaders, PageMarkers: job.Options.PageMarkers, Engine: job.Options.PDFEngine,
		})
		if err != nil {
			return nil, err
		}
		parts = append(parts, r.Markdown)
		ocrPages += len(r.OCRPages)
		claudePages += len(r.ClaudePages)
		pct := int(math.Min(100, math.Round(float64(i+pdfBatch)/float64(len(pages))*100)))
		_ = s.Items.Update(ctx, it.ID, map[string]any{"progress": pct})
	}
	md := strings.TrimSpace(strings.Join(parts, "\n\n"))
	var warnings []string
	via := "pdf"
	if ocrPages > 0 {
		warnings = append(warnings, fmt.Sprintf("OCR %d หน้า ควรตรวจคำผิด", ocrPages))
		via = "pdf+ocr"
	}
	if claudePages > 0 {
		warnings = append(warnings, fmt.Sprintf("Claude อ่าน %d หน้า", claudePages))
		via = "pdf+claude"
	}
	if strings.Contains(md, "> อ่านหน้านี้ไม่สำเร็จ") {
		warnings = append(warnings, "บางหน้าอ่านไม่สำเร็จ ดูข้อความ \"อ่านหน้านี้ไม่สำเร็จ\" ใน Markdown")
	}
	title := info.Title
	if title == "" {
		title = domain.Str(it.FileName)
	}
	if title == "" {
		title = info.Name
	}
	title = regexp.MustCompile(`(?i)\.pdf$`).ReplaceAllString(title, "")
	if title == "" {
		title = "PDF"
	}
	return &converted{Title: title, Markdown: md, Warnings: warnings, FinalURL: domain.Str(it.URL), Via: via}, nil
}

// Convert fetches and converts one item, and returns the Markdown with its header.
func (s *FetchService) Convert(ctx context.Context, it *domain.Item, job *domain.Job) (*converted, error) {
	var out *converted
	var err error
	if it.SourceType == "pdf" {
		out, err = s.convertPDF(ctx, it, job, nil)
	} else {
		out, err = s.convertWeb(ctx, it, job)
	}
	if err != nil {
		return nil, err
	}
	body := out.Markdown
	out.BodyHash = textutil.SHA256(body)
	if job.Options.Header {
		acct, err := s.accountFor(ctx, job.UserID, false)
		if err != nil {
			return nil, err
		}
		st, err := s.Settings.Get(ctx, acct)
		if err != nil {
			return nil, err
		}
		src := out.FinalURL
		if src == "" {
			src = domain.Str(it.FileName)
		}
		hdr := textutil.RenderHeader(domain.Str(st.HeaderTemplate), out.Title, src, time.Now().UTC().Format("2006-01-02 15:04"))
		out.Markdown = hdr + "\n\n" + body
	}
	return out, nil
}

// userMessage turns an error into a message for the review screen.
func userMessage(err error) string {
	var ae *domain.AppError
	if errors.As(err, &ae) {
		return ae.Message
	}
	return "เกิดข้อผิดพลาด: " + err.Error()
}

// FetchItem is the "fetch" task: convert one item and store the result.
func (s *FetchService) FetchItem(ctx context.Context, itemID string) error {
	it, err := s.Items.Get(ctx, itemID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && it.Status == domain.ItemSkipped) {
		return nil
	}
	if err != nil {
		return err
	}
	job, err := s.Jobs.Get(ctx, it.JobID)
	if err != nil {
		return err
	}
	if err := s.Items.Update(ctx, itemID, map[string]any{"status": domain.ItemFetching, "error": nil, "progress": 0}); err != nil {
		return err
	}
	out, cerr := s.Convert(ctx, it, job)
	if cerr != nil {
		err = s.Items.Update(ctx, itemID, map[string]any{"status": domain.ItemFailed, "error": userMessage(cerr)})
	} else {
		fields := map[string]any{
			"status": domain.ItemReady, "title": out.Title, "markdown": out.Markdown,
			"word_count": textutil.CountWords(out.Markdown), "content_hash": out.BodyHash, "fetched_via": out.Via,
			"warnings": domain.StringList(out.Warnings), "progress": 100,
		}
		if it.TabTitle == nil {
			t := out.Title
			if t == "" {
				t = domain.Str(it.URL)
			}
			fields["tab_title"] = textutil.TabTitle(t)
		}
		if out.FinalURL != "" {
			fields["url"] = out.FinalURL
		}
		err = s.Items.Update(ctx, itemID, fields)
	}
	if err != nil {
		return err
	}
	return s.afterFetch(ctx, job.ID)
}

// afterFetch moves the job to review (or publishes it) once every item is done.
func (s *FetchService) afterFetch(ctx context.Context, jobID string) error {
	c, err := s.Items.Counts(ctx, jobID)
	if err != nil || c.Pending > 0 {
		return err
	}
	job, err := s.Jobs.Get(ctx, jobID)
	if err != nil || job.Status != domain.JobFetching {
		return err
	}
	if job.Options.AutoPublish && c.Ready > 0 {
		return s.Queue.Enqueue(ctx, domain.TaskPublish, domain.PublishPayload{JobID: jobID}, "publish:"+jobID)
	}
	_, err = s.Jobs.SetStatusIf(ctx, jobID, domain.JobReview, domain.JobFetching)
	return err
}
