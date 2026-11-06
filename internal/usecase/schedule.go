package usecase

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nuttapong111/link2doc/internal/domain"
)

var weekdays = map[string]time.Weekday{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

func parseHM(s string) (int, int) {
	if s == "" {
		s = "07:00"
	}
	p := strings.SplitN(s, ":", 2)
	h, _ := strconv.Atoi(p[0])
	m := 0
	if len(p) > 1 {
		m, _ = strconv.Atoi(p[1])
	}
	return min(max(h, 0), 23), min(max(m, 0), 59)
}

func monthDay(spec domain.ScheduleSpec) (last bool, day int) {
	switch v := spec.Date.(type) {
	case string:
		if v == "last" {
			return true, 0
		}
		day, _ = strconv.Atoi(v)
	case float64:
		day = int(v)
	case int:
		day = v
	}
	return false, min(max(day, 1), 28)
}

// ToCron describes the schedule as a cron string (for display).
func ToCron(s domain.ScheduleSpec) string {
	h, m := parseHM(s.Time)
	switch s.Frequency {
	case "hourly":
		return fmt.Sprintf("%d * * * *", m)
	case "daily":
		return fmt.Sprintf("%d %d * * *", m, h)
	case "weekly":
		wd, ok := weekdays[s.Day]
		if !ok {
			wd = 1
		}
		return fmt.Sprintf("%d %d * * %d", m, h, wd)
	case "monthly":
		if last, d := monthDay(s); last {
			return fmt.Sprintf("%d %d L * *", m, h)
		} else {
			return fmt.Sprintf("%d %d %d * *", m, h, d)
		}
	}
	return ""
}

// NextRun returns the first run time strictly after `after`, in the given time zone.
func NextRun(s domain.ScheduleSpec, after time.Time, loc *time.Location) *time.Time {
	h, m := parseHM(s.Time)
	a := after.In(loc)
	var t time.Time
	switch s.Frequency {
	case "hourly":
		t = time.Date(a.Year(), a.Month(), a.Day(), a.Hour(), m, 0, 0, loc)
		if !t.After(a) {
			t = t.Add(time.Hour)
		}
	case "daily":
		t = time.Date(a.Year(), a.Month(), a.Day(), h, m, 0, 0, loc)
		if !t.After(a) {
			t = t.AddDate(0, 0, 1)
		}
	case "weekly":
		wd, ok := weekdays[s.Day]
		if !ok {
			wd = time.Monday
		}
		t = time.Date(a.Year(), a.Month(), a.Day(), h, m, 0, 0, loc)
		t = t.AddDate(0, 0, (int(wd)-int(t.Weekday())+7)%7)
		if !t.After(a) {
			t = t.AddDate(0, 0, 7)
		}
	case "monthly":
		last, d := monthDay(s)
		at := func(y int, mo time.Month) time.Time {
			if last {
				return time.Date(y, mo+1, 0, h, m, 0, 0, loc) // day 0 of next month = last day
			}
			return time.Date(y, mo, d, h, m, 0, 0, loc)
		}
		t = at(a.Year(), a.Month())
		if !t.After(a) {
			t = at(a.Year(), a.Month()+1)
		}
	default:
		return nil
	}
	if s.EndsAt != nil && t.After(*s.EndsAt) {
		return nil
	}
	u := t.UTC()
	return &u
}

type ScheduleService struct{ *Deps }

func (s *ScheduleService) Upsert(ctx context.Context, itemID string, spec domain.ScheduleSpec) error {
	cron := ToCron(spec)
	if cron == "" {
		return s.Remove(ctx, itemID)
	}
	mode := spec.UpdateMode
	if mode != "new_tab" {
		mode = "replace"
	}
	return s.Schedules.Upsert(ctx, &domain.Schedule{
		ItemID: itemID, Frequency: spec.Frequency, Cron: cron, Spec: spec, UpdateMode: mode,
		EndsAt: spec.EndsAt, Active: true, NextRunAt: NextRun(spec, time.Now(), s.Location),
	})
}

func (s *ScheduleService) Pause(ctx context.Context, itemID, reason string) error {
	return s.Schedules.Update(ctx, itemID, map[string]any{"active": false, "paused_reason": reason, "next_run_at": nil})
}

func (s *ScheduleService) Resume(ctx context.Context, itemID string) error {
	sc, err := s.Schedules.Get(ctx, itemID)
	if err != nil {
		return err
	}
	return s.Schedules.Update(ctx, itemID, map[string]any{"active": true, "fail_streak": 0, "paused_reason": nil,
		"next_run_at": NextRun(sc.Spec, time.Now(), s.Location)})
}

func (s *ScheduleService) Remove(ctx context.Context, itemID string) error {
	return s.Schedules.Delete(ctx, itemID)
}

type ScheduleOverview struct {
	Schedules []domain.ScheduleRow `json:"schedules"`
	Changes   []domain.ChangeRow   `json:"changes"`
	Stats     domain.RunStats      `json:"stats"`
}

func (s *ScheduleService) Overview(ctx context.Context, userID int) (*ScheduleOverview, error) {
	rows, err := s.Schedules.ListForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if !rows[i].Active {
			rows[i].NextRunAt = nil
		}
		if rows[i].GdocID != nil {
			rows[i].DocURL = domain.Ptr(domain.DocURL(*rows[i].GdocID, domain.Str(rows[i].TabID)))
		}
	}
	changes, err := s.Schedules.RecentChanges(ctx, userID, 10)
	if err != nil {
		return nil, err
	}
	for i := range changes {
		if changes[i].GdocID != nil {
			changes[i].DocURL = domain.Ptr(domain.DocURL(*changes[i].GdocID, domain.Str(changes[i].TabID)))
		}
	}
	stats, err := s.Schedules.Stats(ctx, userID)
	if rows == nil {
		rows = []domain.ScheduleRow{}
	}
	if changes == nil {
		changes = []domain.ChangeRow{}
	}
	return &ScheduleOverview{Schedules: rows, Changes: changes, Stats: stats}, err
}

func (s *ScheduleService) PauseAll(ctx context.Context, userID int) (int, error) {
	ids, err := s.Schedules.ActiveItemIDs(ctx, userID)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := s.Pause(ctx, id, "หยุดทุกรอบชั่วคราว"); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func (s *ScheduleService) RunNow(ctx context.Context, itemID string) error {
	return s.Queue.Enqueue(ctx, domain.TaskRefresh, domain.RefreshPayload{ItemID: itemID, Manual: true}, "refresh:"+itemID)
}

// Tick queues a refresh for every schedule that is due. Called by the worker every 30 seconds.
func (s *ScheduleService) Tick(ctx context.Context) (int, error) {
	now := time.Now()
	due, err := s.Schedules.ClaimDue(ctx, now, func(sc domain.Schedule) *time.Time { return NextRun(sc.Spec, now, s.Location) })
	if err != nil {
		return 0, err
	}
	for _, sc := range due {
		if err := s.Queue.Enqueue(ctx, domain.TaskRefresh, domain.RefreshPayload{ItemID: sc.ItemID}, "refresh:"+sc.ItemID); err != nil {
			return 0, err
		}
	}
	return len(due), nil
}
