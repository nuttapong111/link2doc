// Package postgres implements the domain repositories and the task queue with GORM.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nuttapong111/link2doc/internal/domain"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func Open(dsn string) (*gorm.DB, error) {
	var db *gorm.DB
	var err error
	// Railway may start the app before Postgres accepts connections.
	for i := 0; i < 10; i++ {
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Warn), TranslateError: true})
		if err == nil {
			var sqlDB interface{ Ping() error }
			if sqlDB, err = db.DB(); err == nil {
				if err = sqlDB.Ping(); err == nil {
					break
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(15)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	return db, nil
}

// Migrate creates or updates tables. Several services start at once, so a
// Postgres advisory lock keeps them from migrating at the same time.
func Migrate(db *gorm.DB) error {
	return db.Connection(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_lock(424242)").Error; err != nil {
			return err
		}
		defer tx.Exec("SELECT pg_advisory_unlock(424242)")
		if err := tx.Exec(`CREATE EXTENSION IF NOT EXISTS pgcrypto`).Error; err != nil {
			return err
		}
		if err := tx.AutoMigrate(&domain.User{}, &domain.Job{}, &domain.Item{}, &domain.Schedule{}, &domain.ItemRun{},
			&domain.SiteRule{}, &domain.DifyConnection{}, &domain.UserSettings{}, &domain.Task{}); err != nil {
			return err
		}
		for _, sql := range []string{
			`DO $$ BEGIN
			   ALTER TABLE jobs ADD CONSTRAINT jobs_user_fk FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
			 EXCEPTION WHEN duplicate_object THEN NULL; END $$`,
			`DO $$ BEGIN
			   ALTER TABLE items ADD CONSTRAINT items_job_fk FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE;
			 EXCEPTION WHEN duplicate_object THEN NULL; END $$`,
			`DO $$ BEGIN
			   ALTER TABLE schedules ADD CONSTRAINT schedules_item_fk FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE;
			 EXCEPTION WHEN duplicate_object THEN NULL; END $$`,
			`DO $$ BEGIN
			   ALTER TABLE item_runs ADD CONSTRAINT item_runs_item_fk FOREIGN KEY (item_id) REFERENCES items(id) ON DELETE CASCADE;
			 EXCEPTION WHEN duplicate_object THEN NULL; END $$`,
			// Only one pending task per unique key (e.g. one publish per job at a time).
			`CREATE UNIQUE INDEX IF NOT EXISTS tasks_unique_pending ON tasks(unique_key) WHERE status IN ('queued','running') AND unique_key IS NOT NULL`,
		} {
			if err := tx.Exec(sql).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.ErrNotFound
	}
	return err
}

func withCtx(db *gorm.DB, ctx context.Context) *gorm.DB { return db.WithContext(ctx) }
