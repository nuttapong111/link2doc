package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nuttapong111/link2doc/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ---------- users ----------

type UserRepo struct{ db *gorm.DB }

func NewUserRepo(db *gorm.DB) *UserRepo { return &UserRepo{db} }

func (r *UserRepo) UpsertGoogle(ctx context.Context, sub, email, name string, tok *string) (*domain.User, error) {
	u := domain.User{GoogleSub: sub, Email: email, Name: &name, RefreshTokenEnc: tok}
	err := withCtx(r.db, ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "google_sub"}},
		DoUpdates: clause.Assignments(map[string]any{
			"email": email, "name": name,
			"refresh_token_enc": gorm.Expr("COALESCE(EXCLUDED.refresh_token_enc, users.refresh_token_enc)"),
		}),
	}).Create(&u).Error
	if err != nil {
		return nil, err
	}
	return r.bySub(ctx, sub)
}

func (r *UserRepo) UpsertDev(ctx context.Context) (*domain.User, error) {
	u := domain.User{GoogleSub: "dev", Email: "dev@localhost", Name: domain.Ptr("Dev")}
	if err := withCtx(r.db, ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&u).Error; err != nil {
		return nil, err
	}
	return r.bySub(ctx, "dev")
}

func (r *UserRepo) bySub(ctx context.Context, sub string) (*domain.User, error) {
	var u domain.User
	return &u, notFound(withCtx(r.db, ctx).Where("google_sub = ?", sub).First(&u).Error)
}

func (r *UserRepo) Get(ctx context.Context, id int) (*domain.User, error) {
	var u domain.User
	if err := withCtx(r.db, ctx).First(&u, id).Error; err != nil {
		return nil, notFound(err)
	}
	return &u, nil
}

func (r *UserRepo) ClearToken(ctx context.Context, id int) error {
	return withCtx(r.db, ctx).Model(&domain.User{}).Where("id = ?", id).Update("refresh_token_enc", nil).Error
}

// ---------- jobs ----------

type JobRepo struct{ db *gorm.DB }

func NewJobRepo(db *gorm.DB) *JobRepo { return &JobRepo{db} }

func (r *JobRepo) Create(ctx context.Context, job *domain.Job, items []*domain.Item) error {
	return withCtx(r.db, ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(job).Error; err != nil {
			return err
		}
		for _, it := range items {
			it.JobID = job.ID
			if err := tx.Create(it).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *JobRepo) Get(ctx context.Context, id string) (*domain.Job, error) {
	var j domain.Job
	if err := withCtx(r.db, ctx).Where("id = ?", id).First(&j).Error; err != nil {
		return nil, notFound(err)
	}
	return &j, nil
}

func (r *JobRepo) GetOwned(ctx context.Context, id string, userID int) (*domain.Job, error) {
	var j domain.Job
	if err := withCtx(r.db, ctx).Where("id = ? AND user_id = ?", id, userID).First(&j).Error; err != nil {
		return nil, notFound(err)
	}
	return &j, nil
}

func (r *JobRepo) ListSummaries(ctx context.Context, userID int) ([]domain.JobSummary, error) {
	var rows []domain.JobSummary
	err := withCtx(r.db, ctx).Raw(`SELECT j.id, j.doc_title, j.layout, j.status, j.doc_id, j.folder_id, j.source, j.created_at,
	    COALESCE((j.options->>'toDify')::boolean, false) AS to_dify,
	    count(i.*) AS total, count(i.*) FILTER (WHERE i.status='written') AS written,
	    count(s.*) FILTER (WHERE s.active) AS scheduled,
	    string_agg(DISTINCT substring(i.url from '://([^/]+)'), ', ') AS domains
	  FROM jobs j LEFT JOIN items i ON i.job_id=j.id LEFT JOIN schedules s ON s.item_id=i.id
	  WHERE j.user_id=? GROUP BY j.id ORDER BY j.created_at DESC LIMIT 200`, userID).Scan(&rows).Error
	return rows, err
}

func (r *JobRepo) Update(ctx context.Context, id string, fields map[string]any) error {
	return withCtx(r.db, ctx).Model(&domain.Job{}).Where("id = ?", id).Updates(fields).Error
}

func (r *JobRepo) SetStatusIf(ctx context.Context, id, to string, from ...string) (bool, error) {
	res := withCtx(r.db, ctx).Model(&domain.Job{}).Where("id = ? AND status IN ?", id, from).Update("status", to)
	return res.RowsAffected > 0, res.Error
}

func (r *JobRepo) MergeOptions(ctx context.Context, id string, patch map[string]any) error {
	b, _ := json.Marshal(patch)
	return withCtx(r.db, ctx).Exec(`UPDATE jobs SET options = options || ?::jsonb WHERE id = ?`, string(b), id).Error
}

// ---------- items ----------

type ItemRepo struct{ db *gorm.DB }

func NewItemRepo(db *gorm.DB) *ItemRepo { return &ItemRepo{db} }

func (r *ItemRepo) Get(ctx context.Context, id string) (*domain.Item, error) {
	var it domain.Item
	if err := withCtx(r.db, ctx).Where("id = ?", id).First(&it).Error; err != nil {
		return nil, notFound(err)
	}
	return &it, nil
}

func (r *ItemRepo) GetOwned(ctx context.Context, id string, userID int) (*domain.Item, error) {
	var it domain.Item
	err := withCtx(r.db, ctx).Joins("JOIN jobs j ON j.id = items.job_id").
		Where("items.id = ? AND j.user_id = ?", id, userID).First(&it).Error
	if err != nil {
		return nil, notFound(err)
	}
	return &it, nil
}

func (r *ItemRepo) ListByJob(ctx context.Context, jobID string, withMarkdown bool) ([]domain.Item, error) {
	var items []domain.Item
	q := withCtx(r.db, ctx).Where("job_id = ?", jobID).Order("position")
	if !withMarkdown {
		q = q.Omit("markdown")
	}
	return items, q.Find(&items).Error
}

func (r *ItemRepo) ListByJobStatus(ctx context.Context, jobID string, statuses ...string) ([]domain.Item, error) {
	var items []domain.Item
	return items, withCtx(r.db, ctx).Where("job_id = ? AND status IN ?", jobID, statuses).Order("position").Find(&items).Error
}

func (r *ItemRepo) Update(ctx context.Context, id string, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	return withCtx(r.db, ctx).Model(&domain.Item{}).Where("id = ?", id).Updates(fields).Error
}

func (r *ItemRepo) UpdateWhere(ctx context.Context, jobID string, from []string, fields map[string]any) error {
	return withCtx(r.db, ctx).Model(&domain.Item{}).Where("job_id = ? AND status IN ?", jobID, from).Updates(fields).Error
}

func (r *ItemRepo) Counts(ctx context.Context, jobID string) (domain.ItemCounts, error) {
	var c domain.ItemCounts
	err := withCtx(r.db, ctx).Raw(`SELECT count(*) FILTER (WHERE status IN ('queued','fetching')) AS pending,
	    count(*) FILTER (WHERE status='ready') AS ready, count(*) FILTER (WHERE status='written') AS written,
	    count(*) FILTER (WHERE status<>'skipped') AS not_skip FROM items WHERE job_id = ?`, jobID).Scan(&c).Error
	return c, err
}

// ---------- schedules ----------

type ScheduleRepo struct{ db *gorm.DB }

func NewScheduleRepo(db *gorm.DB) *ScheduleRepo { return &ScheduleRepo{db} }

func (r *ScheduleRepo) Get(ctx context.Context, itemID string) (*domain.Schedule, error) {
	var s domain.Schedule
	if err := withCtx(r.db, ctx).Where("item_id = ?", itemID).First(&s).Error; err != nil {
		return nil, notFound(err)
	}
	return &s, nil
}

func (r *ScheduleRepo) Upsert(ctx context.Context, s *domain.Schedule) error {
	return withCtx(r.db, ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "item_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"frequency", "cron", "spec", "update_mode", "ends_at", "active", "fail_streak", "paused_reason", "next_run_at"}),
	}).Create(s).Error
}

func (r *ScheduleRepo) Update(ctx context.Context, itemID string, fields map[string]any) error {
	return withCtx(r.db, ctx).Model(&domain.Schedule{}).Where("item_id = ?", itemID).Updates(fields).Error
}

func (r *ScheduleRepo) Delete(ctx context.Context, itemID string) error {
	return withCtx(r.db, ctx).Where("item_id = ?", itemID).Delete(&domain.Schedule{}).Error
}

func (r *ScheduleRepo) ListForUser(ctx context.Context, userID int) ([]domain.ScheduleRow, error) {
	var rows []domain.ScheduleRow
	err := withCtx(r.db, ctx).Raw(`SELECT s.*, i.title, i.tab_title, i.url, i.file_name, i.gdoc_id, i.tab_id, i.last_changed_at, j.doc_title
	  FROM schedules s JOIN items i ON i.id=s.item_id JOIN jobs j ON j.id=i.job_id
	  WHERE j.user_id=? ORDER BY s.created_at DESC`, userID).Scan(&rows).Error
	return rows, err
}

func (r *ScheduleRepo) ActiveItemIDs(ctx context.Context, userID int) ([]string, error) {
	var ids []string
	err := withCtx(r.db, ctx).Raw(`SELECT s.item_id FROM schedules s JOIN items i ON i.id=s.item_id JOIN jobs j ON j.id=i.job_id
	  WHERE j.user_id=? AND s.active`, userID).Scan(&ids).Error
	return ids, err
}

func (r *ScheduleRepo) ClaimDue(ctx context.Context, now time.Time, next func(domain.Schedule) *time.Time) ([]domain.Schedule, error) {
	var due []domain.Schedule
	err := withCtx(r.db, ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("active AND next_run_at IS NOT NULL AND next_run_at <= ?", now).Limit(50).Find(&due).Error; err != nil {
			return err
		}
		for _, s := range due {
			if err := tx.Model(&domain.Schedule{}).Where("id = ?", s.ID).Update("next_run_at", next(s)).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return due, err
}

func (r *ScheduleRepo) AddRun(ctx context.Context, run *domain.ItemRun) error {
	return withCtx(r.db, ctx).Create(run).Error
}

func (r *ScheduleRepo) RecentChanges(ctx context.Context, userID int, limit int) ([]domain.ChangeRow, error) {
	var rows []domain.ChangeRow
	err := withCtx(r.db, ctx).Raw(`SELECT r.*, i.title, i.tab_title, i.gdoc_id, i.tab_id FROM item_runs r
	  JOIN items i ON i.id=r.item_id JOIN jobs j ON j.id=i.job_id
	  WHERE j.user_id=? AND r.result='updated' ORDER BY r.ran_at DESC LIMIT ?`, userID, limit).Scan(&rows).Error
	return rows, err
}

func (r *ScheduleRepo) Stats(ctx context.Context, userID int) (domain.RunStats, error) {
	var s domain.RunStats
	err := withCtx(r.db, ctx).Raw(`SELECT count(*) FILTER (WHERE r.ran_at > date_trunc('day', now())) AS today,
	    count(*) FILTER (WHERE r.result='updated' AND r.ran_at > now() - interval '7 days') AS changed7
	  FROM item_runs r JOIN items i ON i.id=r.item_id JOIN jobs j ON j.id=i.job_id WHERE j.user_id=?`, userID).Scan(&s).Error
	return s, err
}

// ---------- settings, site rules, Dify ----------

type SettingsRepo struct{ db *gorm.DB }

func NewSettingsRepo(db *gorm.DB) *SettingsRepo { return &SettingsRepo{db} }

func (r *SettingsRepo) Get(ctx context.Context, userID int) (*domain.UserSettings, error) {
	s := domain.UserSettings{UserID: userID}
	err := withCtx(r.db, ctx).Where("user_id = ?", userID).Limit(1).Find(&s).Error
	return &s, err
}

func (r *SettingsRepo) Save(ctx context.Context, s *domain.UserSettings) error {
	return withCtx(r.db, ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"header_template":     gorm.Expr("EXCLUDED.header_template"),
			"default_folder_id":   gorm.Expr("COALESCE(EXCLUDED.default_folder_id, user_settings.default_folder_id)"),
			"default_folder_name": gorm.Expr("COALESCE(EXCLUDED.default_folder_name, user_settings.default_folder_name)"),
		}),
	}).Create(s).Error
}

func (r *SettingsRepo) SetDefaultFolder(ctx context.Context, userID int, id, name string) error {
	s := domain.UserSettings{UserID: userID, DefaultFolderID: &id, DefaultFolderName: &name}
	return withCtx(r.db, ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"default_folder_id", "default_folder_name"}),
	}).Create(&s).Error
}

func (r *SettingsRepo) ListRules(ctx context.Context, userID int) ([]domain.SiteRule, error) {
	var rules []domain.SiteRule
	return rules, withCtx(r.db, ctx).Where("user_id = ?", userID).Order("domain").Find(&rules).Error
}

func (r *SettingsRepo) RuleForHost(ctx context.Context, userID int, host string) (*domain.SiteRule, error) {
	var rules []domain.SiteRule
	err := withCtx(r.db, ctx).Where("user_id = ? AND (? = domain OR ? LIKE '%.' || domain)", userID, host, host).
		Order("length(domain) DESC").Limit(1).Find(&rules).Error
	if err != nil || len(rules) == 0 {
		return nil, err
	}
	return &rules[0], nil
}

func (r *SettingsRepo) UpsertRule(ctx context.Context, rule *domain.SiteRule) error {
	return withCtx(r.db, ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "domain"}},
		DoUpdates: clause.AssignmentColumns([]string{"include_sel", "exclude_sel"}),
	}).Create(rule).Error
}

func (r *SettingsRepo) DeleteRule(ctx context.Context, userID, id int) error {
	return withCtx(r.db, ctx).Where("id = ? AND user_id = ?", id, userID).Delete(&domain.SiteRule{}).Error
}

func (r *SettingsRepo) GetDify(ctx context.Context, userID int) (*domain.DifyConnection, error) {
	var c []domain.DifyConnection
	if err := withCtx(r.db, ctx).Where("user_id = ?", userID).Limit(1).Find(&c).Error; err != nil || len(c) == 0 {
		return nil, err
	}
	return &c[0], nil
}

func (r *SettingsRepo) SaveDify(ctx context.Context, c *domain.DifyConnection) error {
	return withCtx(r.db, ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"base_url", "api_key_enc", "default_dataset_id", "indexing_technique"}),
	}).Create(c).Error
}
