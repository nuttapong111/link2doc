package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nuttapong111/link2doc/internal/domain"
	"gorm.io/gorm"
)

// TaskQueue is a small job queue on Postgres (FOR UPDATE SKIP LOCKED), so the
// app needs no Redis. Tasks stuck in "running" (worker crashed) are picked up again.
type TaskQueue struct {
	db         *gorm.DB
	staleAfter time.Duration
}

func NewTaskQueue(db *gorm.DB) *TaskQueue { return &TaskQueue{db: db, staleAfter: 30 * time.Minute} }

func (q *TaskQueue) Enqueue(ctx context.Context, kind string, payload any, uniqueKey string) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var key any
	if uniqueKey != "" {
		key = uniqueKey
	}
	return withCtx(q.db, ctx).Exec(`INSERT INTO tasks(kind, payload, unique_key, status, run_at, attempts, created_at)
	  VALUES (?, ?::jsonb, ?, 'queued', now(), 0, now()) ON CONFLICT DO NOTHING`, kind, string(b), key).Error
}

func (q *TaskQueue) Claim(ctx context.Context, kinds []string) (*domain.Task, error) {
	var tasks []domain.Task
	err := withCtx(q.db, ctx).Raw(`UPDATE tasks SET status='running', locked_at=now(), attempts=attempts+1
	  WHERE id = (
	    SELECT id FROM tasks
	    WHERE kind IN ? AND ((status='queued' AND run_at <= now()) OR (status='running' AND locked_at < ?))
	    ORDER BY run_at, id FOR UPDATE SKIP LOCKED LIMIT 1)
	  RETURNING *`, kinds, time.Now().Add(-q.staleAfter)).Scan(&tasks).Error
	if err != nil || len(tasks) == 0 {
		return nil, err
	}
	return &tasks[0], nil
}

func (q *TaskQueue) Finish(ctx context.Context, id int64, taskErr error) error {
	fields := map[string]any{"status": "done", "locked_at": nil}
	if taskErr != nil {
		fields["status"] = "failed"
		fields["error"] = taskErr.Error()
	}
	if err := withCtx(q.db, ctx).Model(&domain.Task{}).Where("id = ?", id).Updates(fields).Error; err != nil {
		return err
	}
	// Keep the table small: drop finished tasks older than a week.
	return withCtx(q.db, ctx).Exec(`DELETE FROM tasks WHERE status IN ('done','failed') AND created_at < now() - interval '7 days'`).Error
}
