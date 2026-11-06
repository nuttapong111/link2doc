// Package worker runs background tasks from the Postgres queue and fires due schedules.
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nuttapong111/link2doc/internal/domain"
	"github.com/nuttapong111/link2doc/internal/usecase"
)

type Worker struct {
	Queue       domain.TaskQueue
	Fetch       *usecase.FetchService
	Publish     *usecase.PublishService
	Refresh     *usecase.RefreshService
	Schedule    *usecase.ScheduleService
	Concurrency int
}

// pool claims tasks of the given kinds with n goroutines.
func (w *Worker) pool(ctx context.Context, wg *sync.WaitGroup, n int, kinds ...string) {
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				t, err := w.Queue.Claim(ctx, kinds)
				if err != nil {
					log.Printf("worker: claim: %v", err)
				}
				if t == nil {
					select {
					case <-time.After(time.Second):
					case <-ctx.Done():
					}
					continue
				}
				err = w.run(ctx, t)
				if err != nil {
					log.Printf("worker: task %d %s failed: %v", t.ID, t.Kind, err)
				}
				// Use a fresh context so a shutdown still records the result.
				if ferr := w.Queue.Finish(context.Background(), t.ID, err); ferr != nil {
					log.Printf("worker: finish %d: %v", t.ID, ferr)
				}
			}
		}()
	}
}

func (w *Worker) run(ctx context.Context, t *domain.Task) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	tctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	switch t.Kind {
	case domain.TaskFetch:
		var p domain.FetchPayload
		if err := json.Unmarshal(t.Payload, &p); err != nil {
			return err
		}
		return w.Fetch.FetchItem(tctx, p.ItemID)
	case domain.TaskPublish:
		var p domain.PublishPayload
		if err := json.Unmarshal(t.Payload, &p); err != nil {
			return err
		}
		return w.Publish.PublishJob(tctx, p.JobID)
	case domain.TaskRefresh:
		var p domain.RefreshPayload
		if err := json.Unmarshal(t.Payload, &p); err != nil {
			return err
		}
		return w.Refresh.RefreshItem(tctx, p.ItemID, p.Manual)
	}
	return fmt.Errorf("unknown task kind %q", t.Kind)
}

// Run blocks until ctx is cancelled, then waits for running tasks to finish.
func (w *Worker) Run(ctx context.Context) {
	var wg sync.WaitGroup
	w.pool(ctx, &wg, max(1, w.Concurrency), domain.TaskFetch)
	w.pool(ctx, &wg, 1, domain.TaskPublish)
	w.pool(ctx, &wg, 2, domain.TaskRefresh)

	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			if n, err := w.Schedule.Tick(ctx); err != nil {
				log.Printf("scheduler: %v", err)
			} else if n > 0 {
				log.Printf("scheduler: queued %d refresh", n)
			}
			select {
			case <-tick.C:
			case <-ctx.Done():
				return
			}
		}
	}()
	log.Printf("worker started (fetch concurrency %d)", w.Concurrency)
	wg.Wait()
}
