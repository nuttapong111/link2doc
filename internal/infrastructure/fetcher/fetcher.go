// Package fetcher downloads public web pages safely: it blocks private and internal
// addresses (SSRF), checks every redirect hop, caps size and time, and spaces out
// requests to the same host.
package fetcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nuttapong111/link2doc/internal/domain"
)

const (
	defaultMaxBytes = 15 << 20
	timeout         = 20 * time.Second
	maxRedirects    = 5
)

type Fetcher struct {
	client       *http.Client
	userAgent    string
	allowPrivate bool
	delay        time.Duration

	mu   sync.Mutex
	next map[string]time.Time
}

func New(userAgent string, allowPrivate bool, perHostDelay time.Duration) *Fetcher {
	f := &Fetcher{userAgent: userAgent, allowPrivate: allowPrivate, delay: perHostDelay, next: map[string]time.Time{}}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	// Check the address actually dialed, so DNS rebinding cannot reach internal hosts.
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, fetchErr("หาโดเมนนี้ไม่เจอ")
		}
		for _, ip := range ips {
			if !f.allowPrivate && isPrivate(ip.IP) {
				return nil, fetchErr("ไม่อนุญาตให้ดึงที่อยู่ภายใน")
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}
	f.client = &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return f
}

func fetchErr(msg string) error { return domain.NewError(422, msg) }

func isPrivate(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
		if ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127 { // carrier-grade NAT
			return true
		}
		if ip[0] == 0 || ip[0] >= 224 {
			return true
		}
	}
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

func (f *Fetcher) checkURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fetchErr("ลิงก์ไม่ถูกต้อง")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fetchErr("รองรับเฉพาะ http และ https")
	}
	h := strings.ToLower(u.Hostname())
	if !f.allowPrivate && (h == "localhost" || strings.HasSuffix(h, ".internal") || strings.HasSuffix(h, ".local")) {
		return nil, fetchErr("ไม่อนุญาตให้ดึงที่อยู่ภายใน")
	}
	return u, nil
}

// WaitForHost keeps requests to one host at least the configured delay apart (per process).
func (f *Fetcher) WaitForHost(ctx context.Context, host string) {
	f.mu.Lock()
	now := time.Now()
	at := f.next[host]
	if at.Before(now) {
		at = now
	}
	f.next[host] = at.Add(f.delay)
	f.mu.Unlock()
	select {
	case <-time.After(time.Until(at)):
	case <-ctx.Done():
	}
}

func (f *Fetcher) Fetch(ctx context.Context, raw, accept string, maxBytes int64) (*domain.FetchResult, error) {
	if accept == "" {
		accept = "text/html,application/xhtml+xml,application/pdf;q=0.9,*/*;q=0.8"
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	u, err := f.checkURL(raw)
	if err != nil {
		return nil, err
	}
	for hop := 0; hop <= maxRedirects; hop++ {
		rctx, cancel := context.WithTimeout(ctx, timeout)
		req, _ := http.NewRequestWithContext(rctx, http.MethodGet, u.String(), nil)
		req.Header.Set("User-Agent", f.userAgent)
		req.Header.Set("Accept", accept)
		req.Header.Set("Accept-Language", "th,en;q=0.8")
		res, err := f.client.Do(req)
		if err != nil {
			cancel()
			var ae *domain.AppError
			if errors.As(err, &ae) {
				return nil, ae
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fetchErr("เว็บตอบช้าเกิน 20 วินาที")
			}
			return nil, fetchErr("เชื่อมต่อเว็บไม่ได้")
		}
		if res.StatusCode >= 300 && res.StatusCode < 400 && res.Header.Get("Location") != "" {
			loc, err := u.Parse(res.Header.Get("Location"))
			res.Body.Close()
			cancel()
			if err != nil {
				return nil, fetchErr("redirect ไม่ถูกต้อง")
			}
			if u, err = f.checkURL(loc.String()); err != nil {
				return nil, err
			}
			continue
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			cancel()
			switch res.StatusCode {
			case 401, 403:
				return nil, fetchErr(fmt.Sprintf("เว็บปฏิเสธการเข้าถึง (%d) อาจต้องล็อกอินหรือกันบอท", res.StatusCode))
			case 404:
				return nil, fetchErr("ไม่พบหน้านี้ (404)")
			}
			return nil, fetchErr(fmt.Sprintf("เว็บตอบกลับผิดพลาด (%d)", res.StatusCode))
		}
		body, err := io.ReadAll(io.LimitReader(res.Body, maxBytes+1))
		res.Body.Close()
		cancel()
		if err != nil {
			return nil, fetchErr("โหลดเนื้อหาไม่ครบ")
		}
		if int64(len(body)) > maxBytes {
			return nil, fetchErr(fmt.Sprintf("ไฟล์ใหญ่เกิน %d MB", maxBytes>>20))
		}
		return &domain.FetchResult{FinalURL: u.String(), ContentType: res.Header.Get("Content-Type"), Body: body}, nil
	}
	return nil, fetchErr("redirect มากเกินไป")
}
