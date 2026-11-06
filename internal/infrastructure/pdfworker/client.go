// Package pdfworker talks to the Python pdf-worker service over Railway's private network.
package pdfworker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sync"
	"time"

	"github.com/nuttapong111/link2doc/internal/domain"
)

type Client struct {
	base, token string
	http        *http.Client

	mu       sync.Mutex
	claude   bool
	claudeAt time.Time
}

func New(base, token string) *Client {
	return &Client{base: base, token: token, http: &http.Client{Timeout: 15 * time.Minute}}
}

func (c *Client) Enabled() bool { return c.base != "" }

// ClaudeReady says whether pdf-worker has an ANTHROPIC_API_KEY (cached for a minute).
func (c *Client) ClaudeReady(ctx context.Context) bool {
	if c.base == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.claudeAt) < time.Minute {
		return c.claude
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var out struct {
		Claude bool `json:"claude"`
	}
	if err := c.do(ctx, http.MethodGet, "/healthz", "", nil, &out); err == nil {
		c.claude = out.Claude
	}
	c.claudeAt = time.Now()
	return c.claude
}

// Base and Token are used by the upload proxy in the HTTP layer.
func (c *Client) Base() string  { return c.base }
func (c *Client) Token() string { return c.token }

func (c *Client) do(ctx context.Context, method, path, ctype string, body io.Reader, out any) error {
	if c.base == "" {
		return domain.NewError(503, "ยังไม่ได้ตั้งค่า PDF_WORKER_URL")
	}
	req, _ := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("pdf-worker: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		var e struct {
			Detail string `json:"detail"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Detail != "" {
			return domain.NewError(res.StatusCode, e.Detail)
		}
		return fmt.Errorf("pdf-worker: %d %s", res.StatusCode, raw)
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) Clean(ctx context.Context, markdown, title, pageURL string) (*domain.CleanResult, error) {
	b, _ := json.Marshal(map[string]string{"markdown": markdown, "title": title, "url": pageURL})
	var out domain.CleanResult
	return &out, c.do(ctx, http.MethodPost, "/clean", "application/json", bytes.NewReader(b), &out)
}

func (c *Client) Info(ctx context.Context, fileID string) (*domain.PDFInfo, error) {
	var out domain.PDFInfo
	return &out, c.do(ctx, http.MethodGet, "/files/"+fileID+"/info", "", nil, &out)
}

func (c *Client) Convert(ctx context.Context, fileID string, r domain.PDFConvertRequest) (*domain.PDFConvertResult, error) {
	b, _ := json.Marshal(r)
	var out domain.PDFConvertResult
	return &out, c.do(ctx, http.MethodPost, "/files/"+fileID+"/convert", "application/json", bytes.NewReader(b), &out)
}

func (c *Client) Upload(ctx context.Context, data []byte, name string) (*domain.PDFUploaded, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", name)
	_, _ = part.Write(data)
	_ = w.Close()
	var out domain.PDFUploaded
	return &out, c.do(ctx, http.MethodPost, "/files", w.FormDataContentType(), &buf, &out)
}
