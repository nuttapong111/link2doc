package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nuttapong111/link2doc/internal/domain"
)

type PublishService struct {
	*Deps
	Schedule *ScheduleService
}

type publishResult struct {
	itemID, gdocID, tabID string
	err                   error
}

// resolveFolder picks where new files go: job folder > user's default > a "Link2Doc" folder made for them.
func (s *PublishService) resolveFolder(ctx context.Context, g domain.GoogleDocs, job *domain.Job) (string, error) {
	if id := domain.ParseDriveID(domain.Str(job.FolderID)); id != "" {
		return id, nil
	}
	acct, err := s.accountFor(ctx, job.UserID, true)
	if err != nil {
		return "", err
	}
	st, err := s.Settings.Get(ctx, acct)
	if err != nil {
		return "", err
	}
	if st.DefaultFolderID != nil && *st.DefaultFolderID != "" {
		return *st.DefaultFolderID, nil
	}
	id, err := g.CreateFolder(ctx, "Link2Doc")
	if err != nil {
		return "", err
	}
	return id, s.Settings.SetDefaultFolder(ctx, acct, id, "Link2Doc")
}

func (s *PublishService) toGoogle(ctx context.Context, job *domain.Job, items []domain.Item) (docID, folderID, layout string, results []publishResult, warnings []string, err error) {
	g, err := s.googleFor(ctx, job.UserID)
	if err != nil {
		return
	}
	raw := job.Options.Format == "raw"
	if folderID, err = s.resolveFolder(ctx, g, job); err != nil {
		return
	}
	layout = job.Layout

	if layout == "doc" {
		for _, it := range items {
			id, e := g.CreateDocFromMarkdown(ctx, it.DisplayTitle(), folderID, domain.Str(it.Markdown))
			results = append(results, publishResult{itemID: it.ID, gdocID: id, err: e})
		}
		return
	}

	docID = domain.ParseDriveID(domain.Str(job.DocID))
	isNew := docID == ""
	title := job.DocTitle
	if title == "" {
		title = "Link2Doc"
	}
	if isNew {
		if docID, err = g.CreateDoc(ctx, title, folderID); err != nil {
			return
		}
	}
	firstTab, err := g.FirstTabID(ctx, docID)
	if err != nil {
		return
	}

	if layout == "tab" {
		for _, it := range items {
			tabID, e := g.AddTab(ctx, docID, it.DisplayTitle())
			if errors.Is(e, domain.ErrTabsUnsupported) && len(results) == 0 {
				warnings = append(warnings, "บัญชีนี้ยังสร้างแท็บผ่าน API ไม่ได้ จึงวางเป็นหัวข้อต่อกันแทน")
				layout = "h1"
				break
			}
			if e == nil {
				e = g.WriteMarkdown(ctx, docID, tabID, domain.Str(it.Markdown), raw, 1)
			}
			results = append(results, publishResult{itemID: it.ID, gdocID: docID, tabID: tabID, err: e})
		}
	}

	if layout == "h1" {
		// Append every item as a Heading 1 section at the end of the first tab.
		for _, it := range items {
			end, e := g.TabEndIndex(ctx, docID, firstTab)
			if e == nil {
				md := "# " + it.DisplayTitle() + "\n\n" + domain.Str(it.Markdown) + "\n"
				e = g.WriteMarkdown(ctx, docID, firstTab, md, raw, max(1, end-1))
			}
			results = append(results, publishResult{itemID: it.ID, gdocID: docID, tabID: firstTab, err: e})
		}
	}

	// Table of contents in the first tab of a new tabbed Doc.
	if isNew && layout == "tab" && firstTab != "" {
		byID := map[string]domain.Item{}
		for _, it := range items {
			byID[it.ID] = it
		}
		var lines []string
		for _, r := range results {
			if r.err != nil || r.tabID == "" {
				continue
			}
			it := byID[r.itemID]
			name := strings.NewReplacer("[", "", "]", "").Replace(it.DisplayTitle())
			src := domain.Str(it.URL)
			if src == "" {
				src = domain.Str(it.FileName)
			}
			lines = append(lines, fmt.Sprintf("- [%s](%s) · ต้นทาง: %s", name, domain.DocURL(docID, r.tabID), src))
		}
		toc := fmt.Sprintf("# %s\n\nสร้างโดย Link2Doc เมื่อ %s\n\n%s\n", title, thaiDateTime(time.Now(), s.Location), strings.Join(lines, "\n"))
		if e := g.RenameTab(ctx, docID, firstTab, "สารบัญ"); e == nil {
			e = g.WriteMarkdown(ctx, docID, firstTab, toc, false, 1)
			if e != nil {
				warnings = append(warnings, "เขียนหน้าสารบัญไม่สำเร็จ: "+e.Error())
			}
		} else {
			warnings = append(warnings, "เขียนหน้าสารบัญไม่สำเร็จ: "+e.Error())
		}
	}
	return
}

// PublishJob is the "publish" task: write ready items to Google Docs and/or Dify, then set up schedules.
func (s *PublishService) PublishJob(ctx context.Context, jobID string) error {
	job, err := s.Jobs.Get(ctx, jobID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	items, err := s.Items.ListByJobStatus(ctx, jobID, domain.ItemReady, domain.ItemWritten)
	if err != nil {
		return err
	}
	if err := s.Jobs.Update(ctx, jobID, map[string]any{"status": domain.JobPublishing, "error": nil}); err != nil {
		return err
	}
	fail := func(e error) error {
		return s.Jobs.Update(ctx, jobID, map[string]any{"status": domain.JobFailed, "finished_at": time.Now(), "error": userMessage(e)})
	}
	var errs, warns []string

	if job.Options.ToGoogle {
		docID, folderID, layout, results, w, err := s.toGoogle(ctx, job, items)
		if err != nil {
			return fail(err)
		}
		warns = append(warns, w...)
		for _, r := range results {
			if r.err != nil {
				errs = append(errs, r.err.Error())
				_ = s.Items.Update(ctx, r.itemID, map[string]any{"error": "เขียนลง Doc ไม่สำเร็จ: " + r.err.Error()})
				continue
			}
			f := map[string]any{"status": domain.ItemWritten, "gdoc_id": r.gdocID, "tab_id": nil}
			if r.tabID != "" {
				f["tab_id"] = r.tabID
			}
			_ = s.Items.Update(ctx, r.itemID, f)
		}
		f := map[string]any{"folder_id": folderID, "layout": layout}
		if docID != "" {
			f["doc_id"] = docID
		}
		if err := s.Jobs.Update(ctx, jobID, f); err != nil {
			return err
		}
	}

	if job.Options.ToDify {
		d, err := s.difyFor(ctx, job.UserID)
		if err != nil {
			return fail(err)
		}
		if d == nil {
			return fail(domain.NewError(400, "ยังไม่ได้เชื่อม Dify ในหน้าตั้งค่า"))
		}
		dataset := job.Options.DifyDatasetID
		if dataset == "" {
			dataset = domain.Str(d.conn.DefaultDatasetID)
		}
		if dataset == "" {
			return fail(domain.NewError(400, "ยังไม่ได้เลือก Knowledge ของ Dify"))
		}
		for _, it := range items {
			name := cut(it.DisplayTitle(), 200)
			var docID string
			var e error
			if it.DifyDocumentID != nil {
				docID, e = d.client.UpdateByText(ctx, dataset, *it.DifyDocumentID, name, domain.Str(it.Markdown), job.Options.Chunk)
			} else {
				docID, e = d.client.CreateByText(ctx, dataset, name, domain.Str(it.Markdown), d.conn.IndexingTechnique, job.Options.Chunk)
			}
			if e != nil {
				errs = append(errs, e.Error())
				_ = s.Items.Update(ctx, it.ID, map[string]any{"error": "ส่งเข้า Dify ไม่สำเร็จ: " + e.Error()})
				continue
			}
			_ = s.Items.Update(ctx, it.ID, map[string]any{"dify_document_id": docID, "status": domain.ItemWritten})
		}
		if err := s.Jobs.MergeOptions(ctx, jobID, map[string]any{"difyDatasetId": dataset}); err != nil {
			return err
		}
	}

	// Schedules: the item's own setting wins over the job's.
	for _, it := range items {
		spec := job.Options.Schedule
		if it.ScheduleOverride != nil {
			spec = it.ScheduleOverride
		}
		if spec != nil && spec.Frequency != "" && spec.Frequency != "once" {
			if err := s.Schedule.Upsert(ctx, it.ID, *spec); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}

	c, err := s.Items.Counts(ctx, jobID)
	if err != nil {
		return err
	}
	status := domain.JobDone
	if c.Written == 0 {
		status = domain.JobFailed
	} else if c.Written < c.NotSkip || len(errs) > 0 {
		status = domain.JobPartial
	}
	var msg any
	if all := append(warns, errs...); len(all) > 0 {
		msg = strings.Join(all, "\n")
	}
	return s.Jobs.Update(ctx, jobID, map[string]any{"status": status, "finished_at": time.Now(), "error": msg})
}
