package usecase

import (
	"context"
	"strings"

	"github.com/nuttapong111/link2doc/internal/domain"
)

// ---------- sign-in ----------

type AuthService struct {
	*Deps
	AllowedDomains []string
	AllowedEmails  []string
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// SignIn finishes the Google callback and returns the user id.
func (s *AuthService) SignIn(ctx context.Context, code string) (int, error) {
	id, err := s.Auth.Exchange(ctx, code)
	if err != nil {
		return 0, err
	}
	domainPart := id.Email[strings.LastIndex(id.Email, "@")+1:]
	if len(s.AllowedDomains) > 0 && !contains(s.AllowedDomains, domainPart) && !contains(s.AllowedDomains, id.HostedDomain) {
		return 0, domain.NewError(403, "บัญชี "+id.Email+" ไม่อยู่ในโดเมนที่อนุญาต")
	}
	if len(s.AllowedEmails) > 0 && !contains(s.AllowedEmails, id.Email) {
		return 0, domain.NewError(403, "บัญชี "+id.Email+" ไม่ได้รับอนุญาต")
	}
	var tok *string
	if id.RefreshToken != "" {
		enc, err := s.Cipher.Encrypt(id.RefreshToken)
		if err != nil {
			return 0, err
		}
		tok = &enc
	}
	u, err := s.Users.UpsertGoogle(ctx, id.Sub, id.Email, id.Name, tok)
	if err != nil {
		return 0, err
	}
	return u.ID, nil
}

func (s *AuthService) DevSignIn(ctx context.Context) (int, error) {
	u, err := s.Users.UpsertDev(ctx)
	if err != nil {
		return 0, err
	}
	return u.ID, nil
}

func (s *AuthService) Disconnect(ctx context.Context, userID int) error {
	u, err := s.Users.Get(ctx, userID)
	if err != nil {
		return err
	}
	if u.RefreshTokenEnc != nil {
		if tok, err := s.Cipher.Decrypt(*u.RefreshTokenEnc); err == nil {
			_ = s.Auth.Revoke(ctx, tok)
		}
	}
	return s.Users.ClearToken(ctx, userID)
}

type Me struct {
	ID     int     `json:"id"`
	Email  string  `json:"email"`
	Name   *string `json:"name"`
	Google bool    `json:"google"`
}

func (s *AuthService) Me(ctx context.Context, userID int) (*Me, error) {
	u, err := s.Users.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &Me{ID: u.ID, Email: u.Email, Name: u.Name, Google: u.RefreshTokenEnc != nil}, nil
}

func (s *AuthService) PickerToken(ctx context.Context, userID int) (string, error) {
	u, err := s.Users.Get(ctx, userID)
	if err != nil {
		return "", err
	}
	if u.RefreshTokenEnc == nil {
		return "", domain.NewError(401, "ยังไม่ได้เชื่อมบัญชี Google")
	}
	tok, err := s.Cipher.Decrypt(*u.RefreshTokenEnc)
	if err != nil {
		return "", err
	}
	return s.Auth.AccessToken(ctx, tok)
}

// ---------- settings ----------

type SettingsService struct{ *Deps }

type SettingsView struct {
	HeaderTemplate    *string                `json:"headerTemplate"`
	DefaultFolderID   *string                `json:"defaultFolderId"`
	DefaultFolderName *string                `json:"defaultFolderName"`
	Rules             []domain.SiteRule      `json:"rules"`
	Dify              *domain.DifyConnection `json:"dify"`
}

func (s *SettingsService) Get(ctx context.Context, userID int) (*SettingsView, error) {
	st, err := s.Settings.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	rules, err := s.Settings.ListRules(ctx, userID)
	if err != nil {
		return nil, err
	}
	if rules == nil {
		rules = []domain.SiteRule{}
	}
	d, err := s.Settings.GetDify(ctx, userID)
	return &SettingsView{HeaderTemplate: st.HeaderTemplate, DefaultFolderID: st.DefaultFolderID, DefaultFolderName: st.DefaultFolderName, Rules: rules, Dify: d}, err
}

func (s *SettingsService) CheckFolder(ctx context.Context, userID int, input string) (*domain.DriveFolder, error) {
	id := domain.ParseDriveID(input)
	if id == "" {
		return nil, domain.NewError(400, "ลิงก์โฟลเดอร์ไม่ถูกต้อง")
	}
	g, err := s.googleFor(ctx, userID)
	if err != nil {
		return nil, err
	}
	return g.CheckFolder(ctx, id)
}

func (s *SettingsService) Save(ctx context.Context, userID int, headerTemplate, defaultFolder string) (*string, error) {
	st := &domain.UserSettings{UserID: userID}
	if headerTemplate != "" {
		st.HeaderTemplate = &headerTemplate
	}
	if defaultFolder != "" {
		f, err := s.CheckFolder(ctx, userID, defaultFolder)
		if err != nil {
			return nil, err
		}
		st.DefaultFolderID, st.DefaultFolderName = &f.ID, &f.Name
	}
	return st.DefaultFolderName, s.Settings.Save(ctx, st)
}

func (s *SettingsService) SaveRule(ctx context.Context, userID int, domainIn, include, exclude string) (*domain.SiteRule, error) {
	d := strings.ToLower(strings.TrimSpace(domainIn))
	d = strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
	if i := strings.Index(d, "/"); i >= 0 {
		d = d[:i]
	}
	if d == "" {
		return nil, domain.NewError(400, "ใส่โดเมนก่อน")
	}
	r := &domain.SiteRule{UserID: userID, Domain: d}
	if include != "" {
		r.IncludeSel = &include
	}
	if exclude != "" {
		r.ExcludeSel = &exclude
	}
	return r, s.Settings.UpsertRule(ctx, r)
}

func (s *SettingsService) DeleteRule(ctx context.Context, userID, id int) error {
	return s.Settings.DeleteRule(ctx, userID, id)
}

// TestRule fetches a page and converts it with the given selectors, without saving.
func (s *SettingsService) TestRule(ctx context.Context, rawURL, include, exclude string) (string, string, error) {
	res, err := s.Fetcher.Fetch(ctx, rawURL, "", 0)
	if err != nil {
		return "", "", err
	}
	rule := &domain.SiteRule{IncludeSel: &include, ExcludeSel: &exclude}
	out, err := s.Converter.ToMarkdown(s.Converter.Decode(res.Body, res.ContentType), res.FinalURL, domain.ConvertOptions{MainOnly: true, KeepLinks: true, Rule: rule})
	if err != nil {
		return "", "", err
	}
	return out.Title, cut(out.Markdown, 3000), nil
}

func (s *SettingsService) SaveDify(ctx context.Context, userID int, baseURL, apiKey, datasetID, indexing string) error {
	existing, err := s.Settings.GetDify(ctx, userID)
	if err != nil {
		return err
	}
	keyEnc := ""
	if apiKey != "" {
		if keyEnc, err = s.Cipher.Encrypt(apiKey); err != nil {
			return err
		}
	} else if existing != nil {
		keyEnc = existing.APIKeyEnc
	}
	if baseURL == "" || keyEnc == "" {
		return domain.NewError(400, "ใส่ Dify API URL และ API key")
	}
	if indexing != "economy" {
		indexing = "high_quality"
	}
	c := &domain.DifyConnection{UserID: userID, BaseURL: baseURL, APIKeyEnc: keyEnc, IndexingTechnique: indexing}
	if datasetID != "" {
		c.DefaultDatasetID = &datasetID
	}
	return s.Settings.SaveDify(ctx, c)
}

func (s *SettingsService) DifyDatasets(ctx context.Context, userID int) (bool, []domain.DifyDataset, error) {
	d, err := s.difyFor(ctx, userID)
	if err != nil || d == nil {
		return false, []domain.DifyDataset{}, err
	}
	list, err := d.client.ListDatasets(ctx)
	return true, list, err
}
