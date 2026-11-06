// Package config reads all settings from environment variables. See .env.example.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port        string
	Mode        string // all | api | worker
	AppURL      string
	DatabaseURL string

	SessionSecret string
	EncryptionKey string

	GoogleClientID     string
	GoogleClientSecret string
	DriveScopeMode     string // full | file
	GoogleAPIKey       string
	GoogleAppID        string
	AllowedDomains     []string
	AllowedEmails      []string

	PDFWorkerURL   string
	PDFWorkerToken string

	ChromePath       string
	MaxLinksPerJob   int
	FetchConcurrency int
	PerDomainDelayMs int
	Timezone         string
	UserAgent        string
	AllowPrivate     bool
	DevLogin         bool
	StaticDir        string

	PublicMode     bool     // anyone can use the converter without signing in
	AdminEmails    []string // accounts whose Google Drive / Dify / settings serve guests
	GuestMaxLinks  int
	GuestJobsPerHr int
	AdminUsername  string
	AdminPassword  string
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(k)); err == nil && n > 0 {
		return n
	}
	return def
}

func list(k string) []string {
	var out []string
	for _, s := range strings.Split(os.Getenv(k), ",") {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Load reads the environment. Only the database, session and encryption settings are
// required to boot; Google settings are checked when someone signs in, so the service
// can be deployed before the OAuth client exists.
func Load() (*Config, error) {
	c := &Config{
		Port:               env("PORT", "8080"),
		Mode:               env("MODE", "all"),
		AppURL:             strings.TrimRight(env("APP_URL", "http://localhost:8080"), "/"),
		DatabaseURL:        env("DATABASE_URL", ""),
		SessionSecret:      env("SESSION_SECRET", ""),
		EncryptionKey:      env("ENCRYPTION_KEY", ""),
		GoogleClientID:     env("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret: env("GOOGLE_CLIENT_SECRET", ""),
		DriveScopeMode:     map[bool]string{true: "full", false: "file"}[env("GOOGLE_DRIVE_SCOPE", "") == "full"],
		GoogleAPIKey:       env("GOOGLE_API_KEY", ""),
		GoogleAppID:        env("GOOGLE_APP_ID", ""),
		AllowedDomains:     list("ALLOWED_DOMAIN"),
		AllowedEmails:      list("ALLOWED_EMAILS"),
		PDFWorkerURL:       strings.TrimRight(env("PDF_WORKER_URL", ""), "/"),
		PDFWorkerToken:     env("PDF_WORKER_TOKEN", ""),
		ChromePath:         env("CHROME_PATH", ""),
		MaxLinksPerJob:     envInt("MAX_LINKS_PER_JOB", 100),
		FetchConcurrency:   envInt("FETCH_CONCURRENCY", 4),
		PerDomainDelayMs:   envInt("PER_DOMAIN_DELAY_MS", 1000),
		Timezone:           env("SCHEDULE_TZ", "Asia/Bangkok"),
		UserAgent:          env("FETCH_USER_AGENT", "Link2DocBot/0.2 (+markdown converter)"),
		AllowPrivate:       env("ALLOW_PRIVATE_FETCH", "") == "true",
		StaticDir:          env("STATIC_DIR", ""),
		PublicMode:         env("PUBLIC_MODE", "") == "true",
		AdminEmails:        list("ADMIN_EMAILS"),
		GuestMaxLinks:      envInt("GUEST_MAX_LINKS", 20),
		GuestJobsPerHr:     envInt("GUEST_JOBS_PER_HOUR", 20),
		AdminUsername:      env("ADMIN_USERNAME", ""),
		AdminPassword:      env("ADMIN_PASSWORD", ""),
	}
	c.DevLogin = env("DEV_LOGIN", "") == "true" && !strings.HasPrefix(c.AppURL, "https")
	for k, v := range map[string]string{"DATABASE_URL": c.DatabaseURL, "SESSION_SECRET": c.SessionSecret, "ENCRYPTION_KEY": c.EncryptionKey} {
		if v == "" {
			return nil, fmt.Errorf("missing env var %s", k)
		}
	}
	switch c.Mode {
	case "all", "api", "worker":
	default:
		return nil, fmt.Errorf("MODE must be all, api or worker")
	}
	return c, nil
}

func (c *Config) GoogleConfigured() bool { return c.GoogleClientID != "" && c.GoogleClientSecret != "" }
func (c *Config) Secure() bool           { return strings.HasPrefix(c.AppURL, "https") }
