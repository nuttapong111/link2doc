package domain

import (
	"context"
	"time"
)

// ---------- Repositories (implemented in repository/postgres) ----------

type UserRepository interface {
	UpsertGoogle(ctx context.Context, sub, email, name string, refreshTokenEnc *string) (*User, error)
	UpsertDev(ctx context.Context) (*User, error)
	Get(ctx context.Context, id int) (*User, error)
	ClearToken(ctx context.Context, id int) error
	CreateGuest(ctx context.Context) (*User, error)
	UpsertAdmin(ctx context.Context, username string) (*User, error)
	SetGoogle(ctx context.Context, id int, email string, refreshTokenEnc *string) error
	// FindOwner returns the account whose Google token serves guests: the first user with a
	// token whose email is in emails (any user with a token when emails is empty).
	FindOwner(ctx context.Context, emails []string) (*User, error)
}

// JobSummary is one row of the history list.
type JobSummary struct {
	ID        string    `json:"id"`
	DocTitle  string    `json:"doc_title"`
	Layout    string    `json:"layout"`
	Status    string    `json:"status"`
	DocID     *string   `json:"doc_id"`
	FolderID  *string   `json:"folder_id"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
	ToDify    bool      `json:"to_dify"`
	Total     int       `json:"total"`
	Written   int       `json:"written"`
	Scheduled int       `json:"scheduled"`
	Domains   *string   `json:"domains"`
	DocURL    *string   `json:"docUrl" gorm:"-"`
}

type JobRepository interface {
	Create(ctx context.Context, job *Job, items []*Item) error
	Get(ctx context.Context, id string) (*Job, error)
	GetOwned(ctx context.Context, id string, userID int) (*Job, error)
	ListSummaries(ctx context.Context, userID int) ([]JobSummary, error)
	Update(ctx context.Context, id string, fields map[string]any) error
	// SetStatusIf changes the status only when it is currently one of from. Reports whether it changed.
	SetStatusIf(ctx context.Context, id, to string, from ...string) (bool, error)
	MergeOptions(ctx context.Context, id string, patch map[string]any) error
	CountSince(ctx context.Context, userID int, since time.Time) (int64, error)
}

type ItemCounts struct {
	Pending int
	Ready   int
	Written int
	NotSkip int
}

type ItemRepository interface {
	Get(ctx context.Context, id string) (*Item, error)
	GetOwned(ctx context.Context, id string, userID int) (*Item, error)
	ListByJob(ctx context.Context, jobID string, withMarkdown bool) ([]Item, error)
	ListByJobStatus(ctx context.Context, jobID string, statuses ...string) ([]Item, error)
	Update(ctx context.Context, id string, fields map[string]any) error
	UpdateWhere(ctx context.Context, jobID string, fromStatus []string, fields map[string]any) error
	Counts(ctx context.Context, jobID string) (ItemCounts, error)
}

type ScheduleRow struct {
	Schedule
	Title         *string    `json:"title"`
	TabTitle      *string    `json:"tab_title"`
	URL           *string    `json:"url"`
	FileName      *string    `json:"file_name"`
	GdocID        *string    `json:"gdoc_id"`
	TabID         *string    `json:"tab_id"`
	LastChangedAt *time.Time `json:"last_changed_at"`
	DocTitle      *string    `json:"doc_title"`
	DocURL        *string    `json:"docUrl" gorm:"-"`
}

type ChangeRow struct {
	ItemRun
	Title    *string `json:"title"`
	TabTitle *string `json:"tab_title"`
	GdocID   *string `json:"gdoc_id"`
	TabID    *string `json:"tab_id"`
	DocURL   *string `json:"docUrl" gorm:"-"`
}

type RunStats struct {
	Today    int `json:"today"`
	Changed7 int `json:"changed7"`
}

type ScheduleRepository interface {
	Get(ctx context.Context, itemID string) (*Schedule, error)
	Upsert(ctx context.Context, s *Schedule) error
	Update(ctx context.Context, itemID string, fields map[string]any) error
	Delete(ctx context.Context, itemID string) error
	ListForUser(ctx context.Context, userID int) ([]ScheduleRow, error)
	ActiveItemIDs(ctx context.Context, userID int) ([]string, error)
	// ClaimDue returns active schedules whose next run is due and moves their next_run_at forward
	// using next, inside one transaction so several workers never run the same schedule twice.
	ClaimDue(ctx context.Context, now time.Time, next func(Schedule) *time.Time) ([]Schedule, error)
	AddRun(ctx context.Context, r *ItemRun) error
	RecentChanges(ctx context.Context, userID int, limit int) ([]ChangeRow, error)
	Stats(ctx context.Context, userID int) (RunStats, error)
}

type SettingsRepository interface {
	Get(ctx context.Context, userID int) (*UserSettings, error) // never nil
	Save(ctx context.Context, s *UserSettings) error
	SetDefaultFolder(ctx context.Context, userID int, id, name string) error
	ListRules(ctx context.Context, userID int) ([]SiteRule, error)
	RuleForHost(ctx context.Context, userID int, host string) (*SiteRule, error)
	UpsertRule(ctx context.Context, r *SiteRule) error
	DeleteRule(ctx context.Context, userID, id int) error
	GetDify(ctx context.Context, userID int) (*DifyConnection, error)
	SaveDify(ctx context.Context, c *DifyConnection) error
}

type TaskQueue interface {
	// Enqueue adds a task. A non-empty uniqueKey is ignored while a task with the same key is pending.
	Enqueue(ctx context.Context, kind string, payload any, uniqueKey string) error
	// Claim locks the next due task of the given kinds, or returns nil.
	Claim(ctx context.Context, kinds []string) (*Task, error)
	Finish(ctx context.Context, id int64, err error) error
}

// ---------- External services (implemented in infrastructure/*) ----------

type FetchResult struct {
	FinalURL    string
	ContentType string
	Body        []byte
}

type Fetcher interface {
	Fetch(ctx context.Context, url string, accept string, maxBytes int64) (*FetchResult, error)
	WaitForHost(ctx context.Context, host string)
}

type ConvertOptions struct {
	MainOnly   bool
	KeepImages bool
	KeepLinks  bool
	Rule       *SiteRule
}

type Converted struct {
	Title      string
	Markdown   string
	TextLength int
	Warnings   []string
}

type HTMLConverter interface {
	Decode(body []byte, contentType string) string
	ToMarkdown(html, pageURL string, o ConvertOptions) (*Converted, error)
}

// Browser renders JavaScript-heavy pages. Optional.
type Browser interface {
	Available() bool
	Render(ctx context.Context, url string) (html, finalURL string, err error)
}

type PDFInfo struct {
	Pages        int    `json:"pages"`
	ScannedPages []int  `json:"scanned_pages"`
	Title        string `json:"title"`
	Name         string `json:"name"`
}

type PDFConvertRequest struct {
	Pages        []int  `json:"pages"`
	OCR          bool   `json:"ocr"`
	Lang         string `json:"lang"`
	StripHeaders bool   `json:"strip_headers"`
	PageMarkers  bool   `json:"page_markers"`
}

type PDFConvertResult struct {
	Markdown string `json:"markdown"`
	OCRPages []int  `json:"ocr_pages"`
}

type PDFUploaded struct {
	FileID string `json:"file_id"`
	Name   string `json:"name"`
	Pages  int    `json:"pages"`
}

type PDFService interface {
	Enabled() bool
	Info(ctx context.Context, fileID string) (*PDFInfo, error)
	Convert(ctx context.Context, fileID string, r PDFConvertRequest) (*PDFConvertResult, error)
	Upload(ctx context.Context, data []byte, name string) (*PDFUploaded, error)
}

type GoogleIdentity struct {
	Sub          string
	Email        string
	Name         string
	HostedDomain string
	RefreshToken string
}

// GoogleAuth handles the OAuth sign-in.
type GoogleAuth interface {
	AuthURL(state string) string
	Exchange(ctx context.Context, code string) (*GoogleIdentity, error)
	Revoke(ctx context.Context, refreshToken string) error
	AccessToken(ctx context.Context, refreshToken string) (string, error)
}

type DriveFolder struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// GoogleDocs is a session bound to one user's credentials.
type GoogleDocs interface {
	CreateDoc(ctx context.Context, title, folderID string) (string, error)
	CreateDocFromMarkdown(ctx context.Context, title, folderID, markdown string) (string, error)
	ReplaceDocFromMarkdown(ctx context.Context, fileID, markdown string) error
	CreateFolder(ctx context.Context, name string) (string, error)
	CheckFolder(ctx context.Context, id string) (*DriveFolder, error)
	FirstTabID(ctx context.Context, docID string) (string, error)
	TabEndIndex(ctx context.Context, docID, tabID string) (int64, error)
	AddTab(ctx context.Context, docID, title string) (string, error)
	RenameTab(ctx context.Context, docID, tabID, title string) error
	ClearTab(ctx context.Context, docID, tabID string) error
	WriteMarkdown(ctx context.Context, docID, tabID, markdown string, raw bool, index int64) error
}

type GoogleDocsFactory interface {
	ForUser(ctx context.Context, refreshToken string) (GoogleDocs, error)
}

// ErrTabsUnsupported is returned by AddTab when the Docs API refuses addDocumentTab.
var ErrTabsUnsupported = NewError(400, "tabs unsupported")

type DifyDataset struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Documents int    `json:"documents"`
}

type DifyClient interface {
	ListDatasets(ctx context.Context) ([]DifyDataset, error)
	CreateByText(ctx context.Context, datasetID, name, text, indexing string, chunk *ChunkOptions) (string, error)
	UpdateByText(ctx context.Context, datasetID, documentID, name, text string, chunk *ChunkOptions) (string, error)
}

type DifyFactory interface {
	New(baseURL, apiKey string) DifyClient
}

type Cipher interface {
	Encrypt(plain string) (string, error)
	Decrypt(enc string) (string, error)
}
