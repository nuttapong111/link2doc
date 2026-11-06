// Package dify is a client for the Dify Knowledge (dataset) API.
// Newer Dify versions use hyphenated paths (create-by-text), older ones underscores.
package dify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nuttapong111/link2doc/internal/domain"
)

type Factory struct{}

func (Factory) New(baseURL, apiKey string) domain.DifyClient {
	b := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(b, "/v1") {
		b += "/v1"
	}
	return &Client{base: b, key: apiKey, http: &http.Client{Timeout: 2 * time.Minute}}
}

type Client struct {
	base, key string
	http      *http.Client
}

type httpErr struct {
	status int
	msg    string
}

func (e *httpErr) Error() string { return e.msg }

func (c *Client) call(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("เชื่อมต่อ Dify ไม่ได้: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode >= 300 {
		var e struct{ Message, Code string }
		_ = json.Unmarshal(raw, &e)
		msg := e.Message
		if msg == "" {
			msg = e.Code
		}
		if msg == "" {
			msg = string(raw[:min(len(raw), 200)])
		}
		return &httpErr{res.StatusCode, fmt.Sprintf("Dify %d: %s", res.StatusCode, msg)}
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func (c *Client) callEither(ctx context.Context, method, hyphen, underscore string, body, out any) error {
	err := c.call(ctx, method, hyphen, body, out)
	if he, ok := err.(*httpErr); ok && (he.status == 404 || he.status == 405) {
		return c.call(ctx, method, underscore, body, out)
	}
	return err
}

func (c *Client) ListDatasets(ctx context.Context) ([]domain.DifyDataset, error) {
	var r struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			DocumentCount int    `json:"document_count"`
		} `json:"data"`
	}
	if err := c.call(ctx, http.MethodGet, "/datasets?page=1&limit=100", nil, &r); err != nil {
		return nil, err
	}
	out := make([]domain.DifyDataset, 0, len(r.Data))
	for _, d := range r.Data {
		out = append(out, domain.DifyDataset{ID: d.ID, Name: d.Name, Documents: d.DocumentCount})
	}
	return out, nil
}

func processRule(ch *domain.ChunkOptions) map[string]any {
	if ch == nil || ch.Mode != "custom" {
		return map[string]any{"mode": "automatic"}
	}
	sep := ch.Separator
	if sep == "" {
		sep = `\n\n`
	}
	maxTok, overlap := ch.MaxTokens, ch.Overlap
	if maxTok == 0 {
		maxTok = 800
	}
	if overlap == 0 {
		overlap = 80
	}
	return map[string]any{
		"mode": "custom",
		"rules": map[string]any{
			"pre_processing_rules": []map[string]any{
				{"id": "remove_extra_spaces", "enabled": true},
				{"id": "remove_urls_emails", "enabled": false},
			},
			"segmentation": map[string]any{
				"separator": strings.ReplaceAll(sep, `\n`, "\n"), "max_tokens": maxTok, "chunk_overlap": overlap,
			},
		},
	}
}

type docResp struct {
	Document struct {
		ID string `json:"id"`
	} `json:"document"`
}

func (c *Client) CreateByText(ctx context.Context, datasetID, name, text, indexing string, ch *domain.ChunkOptions) (string, error) {
	if indexing == "" {
		indexing = "high_quality"
	}
	var r docResp
	body := map[string]any{"name": name, "text": text, "indexing_technique": indexing, "process_rule": processRule(ch)}
	err := c.callEither(ctx, http.MethodPost, "/datasets/"+datasetID+"/document/create-by-text",
		"/datasets/"+datasetID+"/document/create_by_text", body, &r)
	return r.Document.ID, err
}

func (c *Client) UpdateByText(ctx context.Context, datasetID, documentID, name, text string, ch *domain.ChunkOptions) (string, error) {
	var r docResp
	body := map[string]any{"name": name, "text": text, "process_rule": processRule(ch)}
	p := "/datasets/" + datasetID + "/documents/" + documentID
	err := c.callEither(ctx, http.MethodPost, p+"/update-by-text", p+"/update_by_text", body, &r)
	if r.Document.ID == "" {
		r.Document.ID = documentID
	}
	return r.Document.ID, err
}
