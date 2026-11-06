package usecase

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/nuttapong111/link2doc/internal/domain"
	"github.com/nuttapong111/link2doc/pkg/textutil"
)

type RefreshService struct {
	*Deps
	Fetch    *FetchService
	Schedule *ScheduleService
}

var headerRe = regexp.MustCompile(`(?s)^---\n.*?\n---\n?`)

type diff struct {
	added, removed int
	before, after  string
}

func lineSet(s string) (map[string]bool, []string) {
	set := map[string]bool{}
	var order []string
	for _, l := range strings.Split(s, "\n") {
		if !set[l] {
			set[l] = true
			order = append(order, l)
		}
	}
	return set, order
}

// diffSummary compares two versions line by line, ignoring the front-matter header.
func diffSummary(oldMd, newMd string) diff {
	a, ao := lineSet(headerRe.ReplaceAllString(oldMd, ""))
	b, bo := lineSet(headerRe.ReplaceAllString(newMd, ""))
	var d diff
	for _, l := range ao {
		if !b[l] && strings.TrimSpace(l) != "" {
			if d.removed == 0 {
				d.before = cut(l, 200)
			}
			d.removed++
		}
	}
	for _, l := range bo {
		if !a[l] && strings.TrimSpace(l) != "" {
			if d.added == 0 {
				d.after = cut(l, 200)
			}
			d.added++
		}
	}
	return d
}

// RefreshItem is the "refresh" task: fetch again, compare, and update Doc/Dify only when changed.
func (s *RefreshService) RefreshItem(ctx context.Context, itemID string, manual bool) error {
	it, err := s.Items.Get(ctx, itemID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	sched, err := s.Schedules.Get(ctx, itemID)
	virtual := false
	if errors.Is(err, domain.ErrNotFound) {
		if !manual {
			return nil
		}
		sched, virtual, err = &domain.Schedule{ItemID: itemID, Active: true, UpdateMode: "replace"}, true, nil
	}
	if err != nil {
		return err
	}
	if !sched.Active && !manual {
		return nil
	}
	if !manual && sched.EndsAt != nil && sched.EndsAt.Before(time.Now()) {
		return s.Schedule.Pause(ctx, itemID, "ครบกำหนดสิ้นสุด")
	}
	job, err := s.Jobs.Get(ctx, it.JobID)
	if err != nil {
		return err
	}
	now := time.Now()
	record := func(result string, extra map[string]any) {
		if !virtual {
			f := map[string]any{"last_run_at": now, "last_result": result}
			for k, v := range extra {
				f[k] = v
			}
			_ = s.Schedules.Update(ctx, itemID, f)
		}
	}

	out, cerr := s.Fetch.Convert(WithKnownHash(ctx, domain.Str(it.ContentHash)), it, job)
	if cerr != nil {
		streak := sched.FailStreak + 1
		record("failed", map[string]any{"fail_streak": streak})
		msg := userMessage(cerr)
		_ = s.Schedules.AddRun(ctx, &domain.ItemRun{ItemID: itemID, Result: "failed", Error: &msg})
		if streak >= 3 && !virtual {
			return s.Schedule.Pause(ctx, itemID, fmt.Sprintf("ดึงไม่สำเร็จ %d รอบติด: %s", streak, msg))
		}
		return nil
	}
	if out.BodyHash == domain.Str(it.ContentHash) {
		record("unchanged", map[string]any{"fail_streak": 0})
		return s.Schedules.AddRun(ctx, &domain.ItemRun{ItemID: itemID, Result: "unchanged"})
	}

	d := diffSummary(domain.Str(it.Markdown), out.Markdown)
	raw := job.Options.Format == "raw"
	tabID := domain.Str(it.TabID)
	name := it.DisplayTitle()
	if it.GdocID != nil {
		g, err := s.googleFor(ctx, job.UserID)
		if err != nil {
			return err
		}
		docID := *it.GdocID
		switch {
		case job.Layout == "doc":
			err = g.ReplaceDocFromMarkdown(ctx, docID, out.Markdown)
		case job.Layout == "tab" && sched.UpdateMode == "new_tab":
			if tabID, err = g.AddTab(ctx, docID, fmt.Sprintf("%s (%s)", name, thaiDate(now, s.Location))); err == nil {
				err = g.WriteMarkdown(ctx, docID, tabID, out.Markdown, raw, 1)
			}
		case job.Layout == "tab":
			if err = g.ClearTab(ctx, docID, tabID); err == nil {
				err = g.WriteMarkdown(ctx, docID, tabID, out.Markdown, raw, 1)
			}
		default: // h1: append an updated section at the end of the tab
			var end int64
			if end, err = g.TabEndIndex(ctx, docID, tabID); err == nil {
				md := fmt.Sprintf("# %s (อัปเดต %s)\n\n%s\n", name, thaiDateTime(now, s.Location), out.Markdown)
				err = g.WriteMarkdown(ctx, docID, tabID, md, raw, max(1, end-1))
			}
		}
		if err != nil {
			msg := "เขียนลง Doc ไม่สำเร็จ: " + userMessage(err)
			record("failed", map[string]any{"fail_streak": sched.FailStreak + 1})
			return s.Schedules.AddRun(ctx, &domain.ItemRun{ItemID: itemID, Result: "failed", Error: &msg})
		}
	}
	if it.DifyDocumentID != nil && job.Options.DifyDatasetID != "" {
		if dc, err := s.difyFor(ctx, job.UserID); err == nil && dc != nil {
			if _, err := dc.client.UpdateByText(ctx, job.Options.DifyDatasetID, *it.DifyDocumentID, cut(name, 200), out.Markdown, job.Options.Chunk); err != nil {
				msg := "ส่งเข้า Dify ไม่สำเร็จ: " + err.Error()
				_ = s.Schedules.AddRun(ctx, &domain.ItemRun{ItemID: itemID, Result: "failed", Error: &msg})
			}
		}
	}
	f := map[string]any{"markdown": out.Markdown, "content_hash": out.BodyHash, "word_count": textutil.CountWords(out.Markdown), "last_changed_at": now}
	if tabID != "" {
		f["tab_id"] = tabID
	}
	if err := s.Items.Update(ctx, itemID, f); err != nil {
		return err
	}
	record("updated", map[string]any{"fail_streak": 0})
	return s.Schedules.AddRun(ctx, &domain.ItemRun{ItemID: itemID, Result: "updated", Added: d.added, Removed: d.removed, BeforeLine: d.before, AfterLine: d.after})
}
