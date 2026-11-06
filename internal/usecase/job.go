package usecase

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/nuttapong111/link2doc/internal/domain"
)

type JobService struct{ *Deps }

type FileInput struct {
	FileID string `json:"fileId"`
	URL    string `json:"url"`
	Name   string `json:"name"`
	Pages  string `json:"pages"`
}

// CreateJobInput mirrors the "งานใหม่" form. Pointers let missing booleans default to true.
type CreateJobInput struct {
	Source        string               `json:"source"`
	URLs          []string             `json:"urls"`
	Files         []FileInput          `json:"files"`
	DocTitle      string               `json:"docTitle"`
	Target        string               `json:"target"`
	DocID         string               `json:"docId"`
	Folder        string               `json:"folder"`
	Layout        string               `json:"layout"`
	MainOnly      *bool                `json:"mainOnly"`
	KeepLinks     *bool                `json:"keepLinks"`
	KeepImages    bool                 `json:"keepImages"`
	Header        *bool                `json:"header"`
	Format        string               `json:"format"`
	AutoPublish   bool                 `json:"autoPublish"`
	ToGoogle      *bool                `json:"toGoogle"`
	ToDify        bool                 `json:"toDify"`
	DifyDatasetID string               `json:"difyDatasetId"`
	Chunk         *domain.ChunkOptions `json:"chunk"`
	Schedule      *domain.ScheduleSpec `json:"schedule"`
	OCR           *bool                `json:"ocr"`
	OCRLang       string               `json:"ocrLang"`
	StripHeaders  *bool                `json:"stripHeaders"`
	PageMarkers   *bool                `json:"pageMarkers"`
}

func orTrue(b *bool) bool { return b == nil || *b }

func normURLs(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range list {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			continue
		}
		u.Fragment = ""
		if k := u.String(); !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

func (s *JobService) Create(ctx context.Context, userID int, in CreateJobInput) (string, error) {
	source := "url"
	if in.Source == "pdf" {
		source = "pdf"
	}
	var urls []string
	var files []FileInput
	if source == "url" {
		urls = normURLs(in.URLs)
	} else {
		for _, f := range in.Files {
			if f.FileID != "" || f.URL != "" {
				files = append(files, f)
			}
		}
	}
	n := len(urls) + len(files)
	if n == 0 {
		return "", domain.NewError(400, "ไม่พบลิงก์หรือไฟล์ที่ใช้ได้")
	}
	limit := s.MaxLinksPerJob
	guest := s.IsGuest(ctx, userID)
	if guest {
		// Guests always write a new Doc into the company's default folder.
		in.Target, in.DocID, in.Folder = "new", "", ""
		limit = min(limit, s.GuestMaxLinks)
		recent, err := s.Jobs.CountSince(ctx, userID, time.Now().Add(-time.Hour))
		if err != nil {
			return "", err
		}
		if recent >= int64(s.GuestJobsPerHr) {
			return "", domain.NewError(429, "ส่งงานถี่เกินไป กรุณารอสักครู่แล้วลองใหม่")
		}
	}
	if n > limit {
		return "", domain.NewError(400, fmt.Sprintf("ใส่ได้ไม่เกิน %d รายการต่องาน", limit))
	}
	layout := in.Layout
	if layout != "h1" && layout != "doc" {
		layout = "tab"
	}
	var docID, folderID *string
	target := "new"
	if in.Target == "existing" {
		id := domain.ParseDriveID(in.DocID)
		if id == "" {
			return "", domain.NewError(400, "ลิงก์ Google Doc เดิมไม่ถูกต้อง")
		}
		docID, target = &id, "existing"
	}
	if in.Folder != "" {
		id := domain.ParseDriveID(in.Folder)
		if id == "" {
			return "", domain.NewError(400, "ลิงก์โฟลเดอร์ Drive ไม่ถูกต้อง")
		}
		folderID = &id
	}
	format := "formatted"
	if in.Format == "raw" {
		format = "raw"
	}
	lang := in.OCRLang
	if lang == "" {
		lang = "tha+eng"
	}
	title := strings.TrimSpace(in.DocTitle)
	if title == "" {
		now := time.Now().In(s.Location)
		title = fmt.Sprintf("เว็บ→MD %d/%d/%d", now.Day(), int(now.Month()), now.Year()+543)
	}
	job := &domain.Job{
		UserID: userID, Source: source, DocTitle: cut(title, 200), Target: target, Layout: layout,
		DocID: docID, FolderID: folderID, Status: domain.JobFetching,
		Options: domain.JobOptions{
			MainOnly: orTrue(in.MainOnly), KeepLinks: orTrue(in.KeepLinks), KeepImages: in.KeepImages, Header: orTrue(in.Header),
			Format: format, AutoPublish: in.AutoPublish, ToGoogle: orTrue(in.ToGoogle), ToDify: in.ToDify,
			DifyDatasetID: in.DifyDatasetID, Chunk: in.Chunk, Schedule: in.Schedule,
			OCR: orTrue(in.OCR), OCRLang: lang, StripHeaders: orTrue(in.StripHeaders), PageMarkers: orTrue(in.PageMarkers),
		},
	}
	var items []*domain.Item
	for i, u := range urls {
		items = append(items, &domain.Item{Position: i, SourceType: "url", URL: domain.Ptr(u), Status: domain.ItemQueued, Warnings: domain.StringList{}})
	}
	opt := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	for i, f := range files {
		items = append(items, &domain.Item{Position: len(urls) + i, SourceType: "pdf", URL: opt(f.URL), FileID: opt(f.FileID),
			FileName: opt(f.Name), PageRange: opt(f.Pages), Status: domain.ItemQueued, Warnings: domain.StringList{}})
	}
	if err := s.Jobs.Create(ctx, job, items); err != nil {
		return "", err
	}
	for _, it := range items {
		if err := s.Queue.Enqueue(ctx, domain.TaskFetch, domain.FetchPayload{ItemID: it.ID}, ""); err != nil {
			return "", err
		}
	}
	return job.ID, nil
}

func (s *JobService) List(ctx context.Context, userID int) ([]domain.JobSummary, error) {
	rows, err := s.Jobs.ListSummaries(ctx, userID)
	for i := range rows {
		if rows[i].DocID != nil {
			rows[i].DocURL = domain.Ptr(domain.DocURL(*rows[i].DocID, ""))
		}
	}
	if rows == nil {
		rows = []domain.JobSummary{}
	}
	return rows, err
}

type ItemView struct {
	domain.Item
	DocURL *string `json:"docUrl"`
}

type JobView struct {
	domain.Job
	DocURL *string    `json:"docUrl"`
	Items  []ItemView `json:"items"`
}

func (s *JobService) owned(ctx context.Context, userID int, id string) (*domain.Job, error) {
	j, err := s.Jobs.GetOwned(ctx, id, userID)
	if err == domain.ErrNotFound {
		return nil, domain.NewError(404, "ไม่พบงานนี้")
	}
	return j, err
}

func (s *JobService) ownedItem(ctx context.Context, userID int, id string) (*domain.Item, error) {
	it, err := s.Items.GetOwned(ctx, id, userID)
	if err == domain.ErrNotFound {
		return nil, domain.NewError(404, "ไม่พบรายการนี้")
	}
	return it, err
}

func (s *JobService) Get(ctx context.Context, userID int, id string, withMarkdown bool) (*JobView, error) {
	j, err := s.owned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	items, err := s.Items.ListByJob(ctx, j.ID, withMarkdown)
	if err != nil {
		return nil, err
	}
	v := &JobView{Job: *j, Items: make([]ItemView, 0, len(items))}
	if j.DocID != nil {
		v.DocURL = domain.Ptr(domain.DocURL(*j.DocID, ""))
	}
	for _, it := range items {
		iv := ItemView{Item: it}
		if it.GdocID != nil {
			iv.DocURL = domain.Ptr(domain.DocURL(*it.GdocID, domain.Str(it.TabID)))
		}
		v.Items = append(v.Items, iv)
	}
	return v, nil
}

type PatchItemInput struct {
	TabTitle *string              `json:"tabTitle"`
	Markdown *string              `json:"markdown"`
	Skip     *bool                `json:"skip"`
	Schedule *domain.ScheduleSpec `json:"schedule"`
	Position *int                 `json:"position"`
}

func (s *JobService) PatchItem(ctx context.Context, userID int, id string, in PatchItemInput) error {
	it, err := s.ownedItem(ctx, userID, id)
	if err != nil {
		return err
	}
	f := map[string]any{}
	if in.TabTitle != nil {
		f["tab_title"] = *in.TabTitle
	}
	if in.Markdown != nil {
		f["markdown"] = *in.Markdown
		if it.Status == domain.ItemFailed {
			f["status"], f["error"] = domain.ItemReady, nil
		}
	}
	if in.Skip != nil {
		if *in.Skip {
			f["status"] = domain.ItemSkipped
		} else if it.Status == domain.ItemSkipped {
			if it.Markdown == nil && in.Markdown == nil {
				f["status"] = domain.ItemFailed
			} else {
				f["status"] = domain.ItemReady
			}
		}
	}
	if in.Schedule != nil {
		f["schedule_override"] = *in.Schedule
	}
	if in.Position != nil {
		f["position"] = *in.Position
	}
	if len(f) == 0 {
		return nil
	}
	return s.Items.Update(ctx, it.ID, f)
}

func (s *JobService) Retry(ctx context.Context, userID int, id string) error {
	it, err := s.ownedItem(ctx, userID, id)
	if err != nil {
		return err
	}
	if err := s.Items.Update(ctx, it.ID, map[string]any{"status": domain.ItemQueued, "error": nil}); err != nil {
		return err
	}
	if _, err := s.Jobs.SetStatusIf(ctx, it.JobID, domain.JobFetching, domain.JobReview, domain.JobFetching); err != nil {
		return err
	}
	return s.Queue.Enqueue(ctx, domain.TaskFetch, domain.FetchPayload{ItemID: it.ID}, "fetch:"+it.ID)
}

func (s *JobService) Cancel(ctx context.Context, userID int, id string) error {
	j, err := s.owned(ctx, userID, id)
	if err != nil {
		return err
	}
	if err := s.Items.UpdateWhere(ctx, j.ID, []string{domain.ItemQueued}, map[string]any{"status": domain.ItemSkipped}); err != nil {
		return err
	}
	return s.Jobs.Update(ctx, j.ID, map[string]any{"status": domain.JobFailed, "error": "ยกเลิกโดยผู้ใช้", "finished_at": time.Now()})
}

func (s *JobService) Publish(ctx context.Context, userID int, id string, sched *domain.ScheduleSpec) error {
	j, err := s.owned(ctx, userID, id)
	if err != nil {
		return err
	}
	if sched != nil {
		if err := s.Jobs.MergeOptions(ctx, j.ID, map[string]any{"schedule": sched}); err != nil {
			return err
		}
	}
	if err := s.Jobs.Update(ctx, j.ID, map[string]any{"status": domain.JobPublishing}); err != nil {
		return err
	}
	return s.Queue.Enqueue(ctx, domain.TaskPublish, domain.PublishPayload{JobID: j.ID}, "publish:"+j.ID)
}

// RefreshAll re-fetches every written item of a job now ("ดึงใหม่").
func (s *JobService) RefreshAll(ctx context.Context, userID int, id string) (int, error) {
	j, err := s.owned(ctx, userID, id)
	if err != nil {
		return 0, err
	}
	items, err := s.Items.ListByJobStatus(ctx, j.ID, domain.ItemWritten)
	if err != nil {
		return 0, err
	}
	for _, it := range items {
		if err := s.Queue.Enqueue(ctx, domain.TaskRefresh, domain.RefreshPayload{ItemID: it.ID, Manual: true}, "refresh:"+it.ID); err != nil {
			return 0, err
		}
	}
	return len(items), nil
}

// OwnedItemID checks the item belongs to the user (for the schedule endpoints).
func (s *JobService) OwnedItemID(ctx context.Context, userID int, id string) (string, error) {
	it, err := s.ownedItem(ctx, userID, id)
	if err != nil {
		return "", err
	}
	return it.ID, nil
}
