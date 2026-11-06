// Package domain holds the core entities and the interfaces (ports) that the
// use cases depend on. It imports nothing from the outer layers.
package domain

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// Job status values.
const (
	JobFetching   = "fetching"
	JobReview     = "review"
	JobPublishing = "publishing"
	JobDone       = "done"
	JobPartial    = "partial"
	JobFailed     = "failed"
)

// Item status values.
const (
	ItemQueued   = "queued"
	ItemFetching = "fetching"
	ItemReady    = "ready"
	ItemFailed   = "failed"
	ItemSkipped  = "skipped"
	ItemWritten  = "written"
)

type User struct {
	ID              int       `gorm:"primaryKey" json:"id"`
	GoogleSub       string    `gorm:"uniqueIndex;not null" json:"-"`
	Email           string    `gorm:"not null" json:"email"`
	Name            *string   `json:"name"`
	RefreshTokenEnc *string   `json:"-"`
	CreatedAt       time.Time `json:"created_at"`
}

// ChunkOptions controls how Dify splits a document.
type ChunkOptions struct {
	Mode      string `json:"mode,omitempty"`
	Separator string `json:"separator,omitempty"`
	MaxTokens int    `json:"maxTokens,omitempty"`
	Overlap   int    `json:"overlap,omitempty"`
}

// ScheduleSpec is what the user picks in the "ดึงซ้ำอัตโนมัติ" box.
type ScheduleSpec struct {
	Frequency  string     `json:"frequency"`            // once | hourly | daily | weekly | monthly
	Time       string     `json:"time,omitempty"`       // HH:MM
	Day        string     `json:"day,omitempty"`        // mon..sun
	Date       any        `json:"date,omitempty"`       // 1..28 or "last"
	UpdateMode string     `json:"updateMode,omitempty"` // replace | new_tab
	EndsAt     *time.Time `json:"endsAt,omitempty"`
}

type JobOptions struct {
	MainOnly      bool          `json:"mainOnly"`
	KeepLinks     bool          `json:"keepLinks"`
	KeepImages    bool          `json:"keepImages"`
	Header        bool          `json:"header"`
	Format        string        `json:"format"` // formatted | raw
	AutoPublish   bool          `json:"autoPublish"`
	ToGoogle      bool          `json:"toGoogle"`
	ToDify        bool          `json:"toDify"`
	DifyDatasetID string        `json:"difyDatasetId,omitempty"`
	Chunk         *ChunkOptions `json:"chunk,omitempty"`
	Schedule      *ScheduleSpec `json:"schedule,omitempty"`
	OCR           bool          `json:"ocr"`
	OCRLang       string        `json:"ocrLang"`
	StripHeaders  bool          `json:"stripHeaders"`
	PageMarkers   bool          `json:"pageMarkers"`
}

func (o JobOptions) Value() (driver.Value, error) { return jsonValue(o) }
func (o *JobOptions) Scan(v any) error            { return jsonScan(v, o) }

func (s ScheduleSpec) Value() (driver.Value, error) { return jsonValue(s) }
func (s *ScheduleSpec) Scan(v any) error            { return jsonScan(v, s) }

// StringList is stored as a JSON array.
type StringList []string

func (l StringList) Value() (driver.Value, error) {
	if l == nil {
		l = StringList{}
	}
	return jsonValue(l)
}
func (l *StringList) Scan(v any) error { return jsonScan(v, l) }

type Job struct {
	ID         string     `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	UserID     int        `gorm:"not null;index" json:"user_id"`
	Source     string     `gorm:"not null;default:url" json:"source"`
	DocTitle   string     `json:"doc_title"`
	Target     string     `gorm:"not null;default:new" json:"target"`
	Layout     string     `gorm:"not null;default:tab" json:"layout"`
	DocID      *string    `json:"doc_id"`
	FolderID   *string    `json:"folder_id"`
	Options    JobOptions `gorm:"type:jsonb;not null;default:'{}'" json:"options"`
	Status     string     `gorm:"not null;default:fetching" json:"status"`
	Error      *string    `json:"error"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

type Item struct {
	ID               string        `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	JobID            string        `gorm:"type:uuid;not null;index:items_job_idx,priority:1" json:"job_id"`
	Position         int           `gorm:"not null;index:items_job_idx,priority:2" json:"position"`
	SourceType       string        `gorm:"not null;default:url" json:"source_type"`
	URL              *string       `json:"url"`
	FileID           *string       `json:"file_id"`
	FileName         *string       `json:"file_name"`
	PageRange        *string       `json:"page_range"`
	Title            *string       `json:"title"`
	TabTitle         *string       `json:"tab_title"`
	Status           string        `gorm:"not null;default:queued" json:"status"`
	Error            *string       `json:"error"`
	Progress         int           `gorm:"not null;default:0" json:"progress"`
	Markdown         *string       `json:"markdown,omitempty"`
	WordCount        *int          `json:"word_count"`
	ContentHash      *string       `json:"-"`
	FetchedVia       *string       `json:"fetched_via"`
	Warnings         StringList    `gorm:"type:jsonb;not null;default:'[]'" json:"warnings"`
	ScheduleOverride *ScheduleSpec `gorm:"type:jsonb" json:"schedule_override"`
	GdocID           *string       `json:"gdoc_id"`
	TabID            *string       `json:"tab_id"`
	DifyDocumentID   *string       `json:"dify_document_id"`
	LastChangedAt    *time.Time    `json:"last_changed_at"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
}

// DisplayTitle is the best human name for an item.
func (i *Item) DisplayTitle() string {
	for _, s := range []*string{i.TabTitle, i.Title, i.FileName, i.URL} {
		if s != nil && *s != "" {
			return *s
		}
	}
	return "Untitled"
}

type Schedule struct {
	ID           string       `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	ItemID       string       `gorm:"type:uuid;uniqueIndex;not null" json:"item_id"`
	Frequency    string       `gorm:"not null" json:"frequency"`
	Cron         string       `gorm:"not null" json:"cron"`
	Spec         ScheduleSpec `gorm:"type:jsonb;not null;default:'{}'" json:"spec"`
	UpdateMode   string       `gorm:"not null;default:replace" json:"update_mode"`
	Active       bool         `gorm:"not null;default:true" json:"active"`
	PausedReason *string      `json:"paused_reason"`
	FailStreak   int          `gorm:"not null;default:0" json:"fail_streak"`
	EndsAt       *time.Time   `json:"ends_at"`
	NextRunAt    *time.Time   `gorm:"index" json:"next_run"`
	LastRunAt    *time.Time   `json:"last_run_at"`
	LastResult   *string      `json:"last_result"`
	CreatedAt    time.Time    `json:"created_at"`
}

type ItemRun struct {
	ID         int64     `gorm:"primaryKey" json:"id"`
	ItemID     string    `gorm:"type:uuid;not null;index" json:"item_id"`
	RanAt      time.Time `gorm:"autoCreateTime" json:"ran_at"`
	Result     string    `gorm:"not null" json:"result"` // unchanged | updated | failed
	Added      int       `json:"added"`
	Removed    int       `json:"removed"`
	BeforeLine string    `json:"before_line"`
	AfterLine  string    `json:"after_line"`
	Error      *string   `json:"error"`
}

type SiteRule struct {
	ID         int     `gorm:"primaryKey" json:"id"`
	UserID     int     `gorm:"not null;uniqueIndex:site_rules_user_domain" json:"-"`
	Domain     string  `gorm:"not null;uniqueIndex:site_rules_user_domain" json:"domain"`
	IncludeSel *string `json:"include_sel"`
	ExcludeSel *string `json:"exclude_sel"`
}

type DifyConnection struct {
	UserID            int     `gorm:"primaryKey;autoIncrement:false" json:"-"`
	BaseURL           string  `gorm:"not null" json:"base_url"`
	APIKeyEnc         string  `gorm:"not null" json:"-"`
	DefaultDatasetID  *string `json:"default_dataset_id"`
	IndexingTechnique string  `gorm:"not null;default:high_quality" json:"indexing_technique"`
}

type UserSettings struct {
	UserID            int `gorm:"primaryKey;autoIncrement:false"`
	HeaderTemplate    *string
	DefaultFolderID   *string
	DefaultFolderName *string
}

// Task is a unit of background work stored in Postgres (the job queue).
type Task struct {
	ID        int64           `gorm:"primaryKey"`
	Kind      string          `gorm:"not null"`
	Payload   json.RawMessage `gorm:"type:jsonb;not null"`
	UniqueKey *string         `gorm:"index"`
	Status    string          `gorm:"not null;default:queued;index:tasks_pick,priority:1"`
	RunAt     time.Time       `gorm:"not null;default:now();index:tasks_pick,priority:2"`
	Attempts  int             `gorm:"not null;default:0"`
	LockedAt  *time.Time
	Error     *string
	CreatedAt time.Time
}

// Task kinds.
const (
	TaskFetch   = "fetch"
	TaskPublish = "publish"
	TaskRefresh = "refresh"
)

type FetchPayload struct {
	ItemID string `json:"itemId"`
}
type PublishPayload struct {
	JobID string `json:"jobId"`
}
type RefreshPayload struct {
	ItemID string `json:"itemId"`
	Manual bool   `json:"manual"`
}

// AppError carries an HTTP status and a message that is safe to show to users.
type AppError struct {
	Status  int
	Message string
}

func (e *AppError) Error() string { return e.Message }

func NewError(status int, msg string) error { return &AppError{Status: status, Message: msg} }

var ErrNotFound = errors.New("not found")

func jsonValue(v any) (driver.Value, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func jsonScan(v any, dst any) error {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return json.Unmarshal(t, dst)
	case string:
		return json.Unmarshal([]byte(t), dst)
	}
	return errors.New("unsupported json column type")
}

func Ptr[T any](v T) *T { return &v }

func Str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
