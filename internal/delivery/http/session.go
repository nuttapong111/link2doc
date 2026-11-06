package http

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	sessionCookie = "l2d"
	stateCookie   = "l2d_state"
	sessionTTL    = 30 * 24 * time.Hour
	ctxUserID     = "uid"
)

// Sessions keeps the signed-in user id in an HMAC-signed cookie (no server-side store).
type Sessions struct {
	secret []byte
	secure bool
}

func NewSessions(secret string, secure bool) *Sessions {
	return &Sessions{secret: []byte(secret), secure: secure}
}

func (s *Sessions) sign(v string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(v))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Sessions) set(c *gin.Context, name, value string, ttl time.Duration) {
	http.SetCookie(c.Writer, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: int(ttl.Seconds()),
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteLaxMode})
}

func (s *Sessions) Login(c *gin.Context, uid int) {
	v := fmt.Sprintf("%d:%d", uid, time.Now().Add(sessionTTL).Unix())
	s.set(c, sessionCookie, base64.RawURLEncoding.EncodeToString([]byte(v))+"."+s.sign(v), sessionTTL)
}

func (s *Sessions) Logout(c *gin.Context) { s.set(c, sessionCookie, "", -time.Second) }

func (s *Sessions) UserID(c *gin.Context) int {
	ck, err := c.Cookie(sessionCookie)
	if err != nil {
		return 0
	}
	payload, sig, ok := strings.Cut(ck, ".")
	if !ok {
		return 0
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil || !hmac.Equal([]byte(sig), []byte(s.sign(string(raw)))) {
		return 0
	}
	uidS, expS, _ := strings.Cut(string(raw), ":")
	exp, _ := strconv.ParseInt(expS, 10, 64)
	uid, _ := strconv.Atoi(uidS)
	if time.Now().Unix() > exp {
		return 0
	}
	return uid
}

// NewState stores a random OAuth state in a short-lived cookie (CSRF protection).
func (s *Sessions) NewState(c *gin.Context) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	st := hex.EncodeToString(b)
	s.set(c, stateCookie, st, 10*time.Minute)
	return st
}

func (s *Sessions) CheckState(c *gin.Context, got string) bool {
	want, err := c.Cookie(stateCookie)
	s.set(c, stateCookie, "", -time.Second)
	return err == nil && want != "" && hmac.Equal([]byte(want), []byte(got))
}

// RequireAuth rejects requests without a session.
func (s *Sessions) RequireAuth(c *gin.Context) {
	uid := s.UserID(c)
	if uid == 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "กรุณาเข้าสู่ระบบ"})
		return
	}
	c.Set(ctxUserID, uid)
	c.Next()
}

func uid(c *gin.Context) int { return c.GetInt(ctxUserID) }
