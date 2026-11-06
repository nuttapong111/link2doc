// Package google implements Google sign-in and writing to Google Docs / Drive
// (My Drive and Shared drives).
package google

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/nuttapong111/link2doc/internal/domain"
	"golang.org/x/oauth2"
	goauth "golang.org/x/oauth2/google"
	docs "google.golang.org/api/docs/v1"
	drive "google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/idtoken"
	"google.golang.org/api/option"
)

const styleChunk = 400

// ---------- OAuth ----------

type Auth struct {
	cfg    *oauth2.Config
	domain string // hd hint when exactly one domain is allowed
}

func NewAuth(clientID, secret, appURL, scopeMode string, allowedDomains []string) *Auth {
	driveScope := drive.DriveFileScope
	if scopeMode == "full" {
		driveScope = drive.DriveScope
	}
	a := &Auth{cfg: &oauth2.Config{
		ClientID: clientID, ClientSecret: secret, Endpoint: goauth.Endpoint,
		RedirectURL: appURL + "/auth/google/callback",
		Scopes:      []string{"openid", "email", "profile", driveScope},
	}}
	if len(allowedDomains) == 1 {
		a.domain = allowedDomains[0]
	}
	return a
}

func (a *Auth) AuthURL(state string) string {
	opts := []oauth2.AuthCodeOption{oauth2.AccessTypeOffline, oauth2.ApprovalForce,
		oauth2.SetAuthURLParam("include_granted_scopes", "true")}
	if a.domain != "" {
		opts = append(opts, oauth2.SetAuthURLParam("hd", a.domain))
	}
	return a.cfg.AuthCodeURL(state, opts...)
}

func (a *Auth) Exchange(ctx context.Context, code string) (*domain.GoogleIdentity, error) {
	tok, err := a.cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("แลก token กับ Google ไม่สำเร็จ: %w", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	p, err := idtoken.Validate(ctx, raw, a.cfg.ClientID)
	if err != nil {
		return nil, fmt.Errorf("ตรวจ id_token ไม่ผ่าน: %w", err)
	}
	s := func(k string) string { v, _ := p.Claims[k].(string); return v }
	return &domain.GoogleIdentity{Sub: p.Subject, Email: strings.ToLower(s("email")), Name: s("name"),
		HostedDomain: s("hd"), RefreshToken: tok.RefreshToken}, nil
}

func (a *Auth) Revoke(ctx context.Context, refreshToken string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/revoke?token="+url.QueryEscape(refreshToken), nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err == nil {
		res.Body.Close()
	}
	return err
}

func (a *Auth) AccessToken(ctx context.Context, refreshToken string) (string, error) {
	t, err := a.cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken}).Token()
	if err != nil {
		return "", err
	}
	return t.AccessToken, nil
}

// ForUser opens Docs and Drive clients with the user's refresh token.
func (a *Auth) ForUser(ctx context.Context, refreshToken string) (domain.GoogleDocs, error) {
	ts := a.cfg.TokenSource(context.Background(), &oauth2.Token{RefreshToken: refreshToken})
	d, err := docs.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, err
	}
	dr, err := drive.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, err
	}
	return &Client{docs: d, drive: dr}, nil
}

// ---------- Docs / Drive ----------

type Client struct {
	docs  *docs.Service
	drive *drive.Service
}

// withBackoff retries on 429 and 5xx.
func withBackoff[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	delay := time.Second
	for i := 0; ; i++ {
		v, err := fn()
		var ge *googleapi.Error
		if err == nil || i >= 5 || !errors.As(err, &ge) || (ge.Code != 429 && ge.Code < 500) {
			return v, err
		}
		select {
		case <-time.After(delay + time.Duration(rand.Intn(300))*time.Millisecond):
		case <-ctx.Done():
			return v, ctx.Err()
		}
		delay *= 2
	}
}

func (c *Client) batch(ctx context.Context, docID string, reqs []*docs.Request) ([]*docs.Response, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	r, err := withBackoff(ctx, func() (*docs.BatchUpdateDocumentResponse, error) {
		return c.docs.Documents.BatchUpdate(docID, &docs.BatchUpdateDocumentRequest{Requests: reqs}).Context(ctx).Do()
	})
	if err != nil {
		return nil, err
	}
	return r.Replies, nil
}

func flatten(tabs []*docs.Tab, out []*docs.Tab) []*docs.Tab {
	for _, t := range tabs {
		out = append(out, t)
		out = flatten(t.ChildTabs, out)
	}
	return out
}

func (c *Client) tabs(ctx context.Context, docID string) ([]*docs.Tab, error) {
	d, err := withBackoff(ctx, func() (*docs.Document, error) {
		return c.docs.Documents.Get(docID).IncludeTabsContent(true).Context(ctx).Do()
	})
	if err != nil {
		return nil, err
	}
	return flatten(d.Tabs, nil), nil
}

func (c *Client) tab(ctx context.Context, docID, tabID string) (*docs.Tab, error) {
	tabs, err := c.tabs(ctx, docID)
	if err != nil {
		return nil, err
	}
	for _, t := range tabs {
		if t.TabProperties != nil && t.TabProperties.TabId == tabID {
			return t, nil
		}
	}
	if tabID == "" && len(tabs) > 0 {
		return tabs[0], nil
	}
	return nil, domain.NewError(404, "ไม่พบแท็บเดิมใน Doc (อาจถูกลบ)")
}

func endIndex(t *docs.Tab) int64 {
	if t == nil || t.DocumentTab == nil || t.DocumentTab.Body == nil || len(t.DocumentTab.Body.Content) == 0 {
		return 2
	}
	content := t.DocumentTab.Body.Content
	return content[len(content)-1].EndIndex
}

func (c *Client) FirstTabID(ctx context.Context, docID string) (string, error) {
	tabs, err := c.tabs(ctx, docID)
	if err != nil || len(tabs) == 0 || tabs[0].TabProperties == nil {
		return "", err
	}
	return tabs[0].TabProperties.TabId, nil
}

func (c *Client) TabEndIndex(ctx context.Context, docID, tabID string) (int64, error) {
	t, err := c.tab(ctx, docID, tabID)
	if err != nil {
		return 0, err
	}
	return endIndex(t), nil
}

func (c *Client) WriteMarkdown(ctx context.Context, docID, tabID, markdown string, raw bool, index int64) error {
	if index < 1 {
		index = 1
	}
	r := MarkdownToRequests(markdown, index, tabID, raw)
	if _, err := c.batch(ctx, docID, r.Insert); err != nil {
		return err
	}
	for i := 0; i < len(r.Style); i += styleChunk {
		if _, err := c.batch(ctx, docID, r.Style[i:min(i+styleChunk, len(r.Style))]); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) ClearTab(ctx context.Context, docID, tabID string) error {
	t, err := c.tab(ctx, docID, tabID)
	if err != nil {
		return err
	}
	if end := endIndex(t); end-1 > 1 {
		_, err = c.batch(ctx, docID, []*docs.Request{{DeleteContentRange: &docs.DeleteContentRangeRequest{
			Range: &docs.Range{StartIndex: 1, EndIndex: end - 1, TabId: tabID}}}})
	}
	return err
}

var tabUnsupported = regexp.MustCompile(`(?i)addDocumentTab|Unknown name|Invalid JSON`)

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func (c *Client) AddTab(ctx context.Context, docID, title string) (string, error) {
	replies, err := c.batch(ctx, docID, []*docs.Request{{AddDocumentTab: &docs.AddDocumentTabRequest{
		TabProperties: &docs.TabProperties{Title: cut(title, 100)}}}})
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && ge.Code == 400 && tabUnsupported.MatchString(ge.Message) {
			return "", domain.ErrTabsUnsupported
		}
		return "", err
	}
	if len(replies) == 0 || replies[0].AddDocumentTab == nil || replies[0].AddDocumentTab.TabProperties == nil {
		return "", errors.New("สร้างแท็บไม่สำเร็จ")
	}
	return replies[0].AddDocumentTab.TabProperties.TabId, nil
}

func (c *Client) RenameTab(ctx context.Context, docID, tabID, title string) error {
	_, err := c.batch(ctx, docID, []*docs.Request{{UpdateDocumentTabProperties: &docs.UpdateDocumentTabPropertiesRequest{
		TabProperties: &docs.TabProperties{TabId: tabID, Title: cut(title, 100)}, Fields: "title"}}})
	return err
}

func (c *Client) create(ctx context.Context, f *drive.File, html string) (string, error) {
	out, err := withBackoff(ctx, func() (*drive.File, error) {
		call := c.drive.Files.Create(f).SupportsAllDrives(true).Fields("id").Context(ctx)
		if html != "" {
			call = call.Media(strings.NewReader(html), googleapi.ContentType("text/html"))
		}
		return call.Do()
	})
	if err != nil {
		return "", err
	}
	return out.Id, nil
}

func (c *Client) CreateDoc(ctx context.Context, title, folderID string) (string, error) {
	return c.create(ctx, &drive.File{Name: title, MimeType: "application/vnd.google-apps.document", Parents: []string{folderID}}, "")
}

func (c *Client) CreateDocFromMarkdown(ctx context.Context, title, folderID, markdown string) (string, error) {
	return c.create(ctx, &drive.File{Name: title, MimeType: "application/vnd.google-apps.document", Parents: []string{folderID}}, MarkdownToHTML(markdown))
}

func (c *Client) CreateFolder(ctx context.Context, name string) (string, error) {
	return c.create(ctx, &drive.File{Name: name, MimeType: "application/vnd.google-apps.folder"}, "")
}

func (c *Client) ReplaceDocFromMarkdown(ctx context.Context, fileID, markdown string) error {
	_, err := withBackoff(ctx, func() (*drive.File, error) {
		return c.drive.Files.Update(fileID, &drive.File{}).SupportsAllDrives(true).
			Media(strings.NewReader(MarkdownToHTML(markdown)), googleapi.ContentType("text/html")).Context(ctx).Do()
	})
	return err
}

func (c *Client) CheckFolder(ctx context.Context, id string) (*domain.DriveFolder, error) {
	f, err := c.drive.Files.Get(id).SupportsAllDrives(true).Fields("id,name,mimeType,capabilities(canAddChildren)").Context(ctx).Do()
	if err != nil {
		var ge *googleapi.Error
		if errors.As(err, &ge) && ge.Code == 404 {
			return nil, domain.NewError(404, "ไม่พบโฟลเดอร์ หรือบัญชีนี้ไม่มีสิทธิ์เข้าถึง")
		}
		return nil, err
	}
	if f.MimeType != "application/vnd.google-apps.folder" {
		return nil, domain.NewError(400, "ลิงก์นี้ไม่ใช่โฟลเดอร์")
	}
	if f.Capabilities != nil && !f.Capabilities.CanAddChildren {
		return nil, domain.NewError(403, "คุณไม่มีสิทธิ์เพิ่มไฟล์ในโฟลเดอร์นี้")
	}
	return &domain.DriveFolder{ID: f.Id, Name: f.Name}, nil
}
