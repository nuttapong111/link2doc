// Package usecase holds the application logic. It depends only on domain
// interfaces; concrete adapters are injected from cmd/link2doc.
package usecase

import (
	"context"
	"time"

	"github.com/nuttapong111/link2doc/internal/domain"
)

type Deps struct {
	Users     domain.UserRepository
	Jobs      domain.JobRepository
	Items     domain.ItemRepository
	Schedules domain.ScheduleRepository
	Settings  domain.SettingsRepository
	Queue     domain.TaskQueue

	Fetcher   domain.Fetcher
	Converter domain.HTMLConverter
	Browser   domain.Browser
	PDF       domain.PDFService
	Auth      domain.GoogleAuth
	Docs      domain.GoogleDocsFactory
	Dify      domain.DifyFactory
	Cipher    domain.Cipher

	Location       *time.Location
	MaxLinksPerJob int

	// Public mode: guests use the company (owner) account's Google Drive, Dify and settings.
	PublicMode     bool
	AdminEmails    []string
	GuestMaxLinks  int
	GuestJobsPerHr int
}

// IsGuest reports whether the user is an anonymous visitor.
func (d *Deps) IsGuest(ctx context.Context, userID int) bool {
	u, err := d.Users.Get(ctx, userID)
	return err == nil && u.IsGuest()
}

// accountFor returns whose Google/Dify/settings serve this user: the user themself, or the
// owner account for guests. strict=false falls back to the guest when no owner is connected.
func (d *Deps) accountFor(ctx context.Context, userID int, strict bool) (int, error) {
	u, err := d.Users.Get(ctx, userID)
	if err != nil {
		return 0, err
	}
	if !u.IsGuest() {
		return userID, nil
	}
	owner, err := d.Users.FindOwner(ctx, d.AdminEmails)
	if err == domain.ErrNotFound {
		if strict {
			return 0, domain.NewError(503, "ผู้ดูแลระบบยังไม่ได้เชื่อม Google Drive ของบริษัท กรุณาติดต่อผู้ดูแล")
		}
		return userID, nil
	}
	if err != nil {
		return 0, err
	}
	return owner.ID, nil
}

// googleFor opens a Docs/Drive session for the user.
func (d *Deps) googleFor(ctx context.Context, userID int) (domain.GoogleDocs, error) {
	userID, err := d.accountFor(ctx, userID, true)
	if err != nil {
		return nil, err
	}
	u, err := d.Users.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.RefreshTokenEnc == nil {
		return nil, domain.NewError(401, "ยังไม่ได้เชื่อมบัญชี Google หรือสิทธิ์หมดอายุ กรุณาเข้าสู่ระบบใหม่")
	}
	tok, err := d.Cipher.Decrypt(*u.RefreshTokenEnc)
	if err != nil {
		return nil, err
	}
	return d.Docs.ForUser(ctx, tok)
}

type difyConn struct {
	client domain.DifyClient
	conn   *domain.DifyConnection
}

func (d *Deps) difyFor(ctx context.Context, userID int) (*difyConn, error) {
	userID, err := d.accountFor(ctx, userID, false)
	if err != nil {
		return nil, err
	}
	c, err := d.Settings.GetDify(ctx, userID)
	if err != nil || c == nil {
		return nil, err
	}
	key, err := d.Cipher.Decrypt(c.APIKeyEnc)
	if err != nil {
		return nil, err
	}
	return &difyConn{client: d.Dify.New(c.BaseURL, key), conn: c}, nil
}

func thaiDate(t time.Time, loc *time.Location) string {
	months := []string{"ม.ค.", "ก.พ.", "มี.ค.", "เม.ย.", "พ.ค.", "มิ.ย.", "ก.ค.", "ส.ค.", "ก.ย.", "ต.ค.", "พ.ย.", "ธ.ค."}
	t = t.In(loc)
	return itoa(t.Day()) + " " + months[t.Month()-1]
}

func thaiDateTime(t time.Time, loc *time.Location) string {
	t = t.In(loc)
	return thaiDate(t, loc) + " " + itoa(t.Year()+543) + " " + t.Format("15:04")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
