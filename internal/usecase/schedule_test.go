package usecase

import (
	"testing"
	"time"

	"github.com/nuttapong111/link2doc/internal/domain"
)

func TestNextRun(t *testing.T) {
	bkk, _ := time.LoadLocation("Asia/Bangkok")
	now := time.Date(2026, 11, 6, 8, 0, 0, 0, bkk) // Friday 08:00
	cases := []struct {
		spec domain.ScheduleSpec
		want time.Time
	}{
		{domain.ScheduleSpec{Frequency: "daily", Time: "07:00"}, time.Date(2026, 11, 7, 7, 0, 0, 0, bkk)},
		{domain.ScheduleSpec{Frequency: "daily", Time: "09:15"}, time.Date(2026, 11, 6, 9, 15, 0, 0, bkk)},
		{domain.ScheduleSpec{Frequency: "hourly", Time: "00:30"}, time.Date(2026, 11, 6, 8, 30, 0, 0, bkk)},
		{domain.ScheduleSpec{Frequency: "weekly", Day: "mon", Time: "07:00"}, time.Date(2026, 11, 9, 7, 0, 0, 0, bkk)},
		{domain.ScheduleSpec{Frequency: "weekly", Day: "fri", Time: "07:00"}, time.Date(2026, 11, 13, 7, 0, 0, 0, bkk)},
		{domain.ScheduleSpec{Frequency: "monthly", Date: "last", Time: "07:00"}, time.Date(2026, 11, 30, 7, 0, 0, 0, bkk)},
		{domain.ScheduleSpec{Frequency: "monthly", Date: float64(1), Time: "07:00"}, time.Date(2026, 12, 1, 7, 0, 0, 0, bkk)},
	}
	for _, c := range cases {
		got := NextRun(c.spec, now, bkk)
		if got == nil || !got.Equal(c.want) {
			t.Errorf("%+v: got %v want %v", c.spec, got, c.want)
		}
	}
	if NextRun(domain.ScheduleSpec{Frequency: "once"}, now, bkk) != nil {
		t.Error("once must not schedule")
	}
	if ToCron(domain.ScheduleSpec{Frequency: "daily", Time: "07:00"}) != "0 7 * * *" {
		t.Error("cron")
	}
}

func TestDiffSummaryIgnoresHeader(t *testing.T) {
	a := "---\nfetched_at: 1\n---\nบรรทัดเดิม\nเหมือนกัน"
	b := "---\nfetched_at: 2\n---\nบรรทัดใหม่\nเหมือนกัน"
	d := diffSummary(a, b)
	if d.added != 1 || d.removed != 1 || d.before != "บรรทัดเดิม" || d.after != "บรรทัดใหม่" {
		t.Fatalf("%+v", d)
	}
}
