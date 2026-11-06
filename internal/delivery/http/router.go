// Package http is the REST API and static UI (gin).
package http

import (
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nuttapong111/link2doc/internal/config"
	"github.com/nuttapong111/link2doc/internal/domain"
	"github.com/nuttapong111/link2doc/internal/usecase"
	"google.golang.org/api/googleapi"
)

type Handler struct {
	cfg      *config.Config
	sess     *Sessions
	auth     *usecase.AuthService
	jobs     *usecase.JobService
	sched    *usecase.ScheduleService
	settings *usecase.SettingsService
	pdf      PDFProxy
}

// PDFProxy is what the upload endpoint needs from the pdf-worker client.
type PDFProxy interface {
	Enabled() bool
	Base() string
	Token() string
}

type Services struct {
	Auth     *usecase.AuthService
	Jobs     *usecase.JobService
	Schedule *usecase.ScheduleService
	Settings *usecase.SettingsService
	PDF      PDFProxy
}

// fail writes an error as JSON. AppError and Google API messages are shown to the user.
func fail(c *gin.Context, err error) {
	status, msg := http.StatusInternalServerError, "เกิดข้อผิดพลาด"
	var ae *domain.AppError
	var ge *googleapi.Error
	switch {
	case errors.As(err, &ae):
		status, msg = ae.Status, ae.Message
	case errors.Is(err, domain.ErrNotFound):
		status, msg = http.StatusNotFound, "ไม่พบข้อมูล"
	case errors.As(err, &ge):
		status, msg = ge.Code, ge.Message
		if status < 400 {
			status = http.StatusBadGateway
		}
	default:
		msg = err.Error()
	}
	if status >= 500 {
		log.Printf("error %s %s: %v", c.Request.Method, c.Request.URL.Path, err)
	}
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

func ok(c *gin.Context, err error) {
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func NewRouter(cfg *config.Config, s Services, static fs.FS) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	_ = r.SetTrustedProxies(nil)
	r.MaxMultipartMemory = 8 << 20
	h := &Handler{cfg: cfg, sess: NewSessions(cfg.SessionSecret, cfg.Secure()), auth: s.Auth, jobs: s.Jobs,
		sched: s.Schedule, settings: s.Settings, pdf: s.PDF}

	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	r.GET("/auth/google", h.googleStart)
	r.GET("/auth/google/callback", h.googleCallback)
	if cfg.DevLogin {
		r.GET("/auth/dev", h.devLogin)
	}
	r.POST("/auth/login", h.passwordLogin)
	r.POST("/auth/logout", func(c *gin.Context) { h.sess.Logout(c); c.JSON(200, gin.H{"ok": true}) })
	r.GET("/api/me", h.me)

	api := r.Group("/api", h.sess.RequireAuth)
	// Company-only endpoints: guests (public mode) cannot change settings or browse Drive.
	staff := api.Group("", h.requireStaff)
	staff.POST("/google/disconnect", h.disconnect)
	staff.GET("/google/picker", h.picker)

	api.POST("/jobs", h.createJob)
	api.GET("/jobs", h.listJobs)
	api.GET("/jobs/:id", h.getJob)
	api.POST("/jobs/:id/cancel", h.cancelJob)
	api.POST("/jobs/:id/publish", h.publishJob)
	api.POST("/jobs/:id/refresh", h.refreshJob)
	api.PATCH("/items/:id", h.patchItem)
	api.POST("/items/:id/retry", h.retryItem)

	api.GET("/schedules", h.listSchedules)
	api.POST("/schedules/pause-all", h.pauseAll)
	api.PATCH("/schedules/:itemId", h.patchSchedule)
	api.DELETE("/schedules/:itemId", h.deleteSchedule)
	api.POST("/schedules/:itemId/run", h.runSchedule)

	api.GET("/settings", h.getSettings)
	staff.PUT("/settings", h.putSettings)
	staff.POST("/drive/check-folder", h.checkFolder)
	staff.POST("/site-rules", h.saveRule)
	staff.POST("/site-rules/test", h.testRule)
	staff.DELETE("/site-rules/:id", h.deleteRule)
	staff.PUT("/dify", h.saveDify)
	api.GET("/dify/datasets", h.difyDatasets)

	api.POST("/pdf/upload", h.uploadPDF)

	// Static single-page app; unknown non-API paths serve index.html.
	files := http.FS(static)
	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/auth/") {
			c.JSON(404, gin.H{"error": "ไม่พบ endpoint นี้"})
			return
		}
		if p != "/" {
			if f, err := static.Open(strings.TrimPrefix(p, "/")); err == nil {
				f.Close()
				c.Header("Cache-Control", "public, max-age=3600")
				c.FileFromFS(p, files)
				return
			}
		}
		c.Header("Cache-Control", "no-cache")
		index, err := fs.ReadFile(static, "index.html")
		if err != nil {
			c.String(500, "missing index.html")
			return
		}
		c.Data(200, "text/html; charset=utf-8", index)
	})
	return r
}

// ---------- auth ----------

func (h *Handler) requireStaff(c *gin.Context) {
	if h.auth.IsGuest(c, uid(c)) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "เฉพาะผู้ดูแลระบบ"})
		return
	}
	c.Next()
}

func (h *Handler) passwordLogin(c *gin.Context) {
	var b struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	_ = c.ShouldBindJSON(&b)
	id, err := h.auth.PasswordLogin(c, b.Username, b.Password)
	if err != nil {
		fail(c, err)
		return
	}
	h.sess.Login(c, id)
	c.JSON(200, gin.H{"ok": true})
}

func (h *Handler) googleStart(c *gin.Context) {
	if !h.cfg.GoogleConfigured() {
		c.String(503, "ยังไม่ได้ตั้งค่า GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET ใน Railway")
		return
	}
	if h.cfg.PublicMode {
		// In public mode Google is only used to connect the company Drive to the admin account.
		if id := h.sess.UserID(c); id == 0 || h.auth.IsGuest(c, id) {
			c.Redirect(http.StatusFound, "/#/login")
			return
		}
	}
	c.Redirect(http.StatusFound, h.auth.Auth.AuthURL(h.sess.NewState(c)))
}

func (h *Handler) googleCallback(c *gin.Context) {
	if !h.sess.CheckState(c, c.Query("state")) {
		c.String(400, "เซสชันเข้าสู่ระบบหมดอายุ กรุณาลองใหม่")
		return
	}
	if cur := h.sess.UserID(c); cur != 0 && !h.auth.IsGuest(c, cur) && h.cfg.PublicMode {
		if err := h.auth.LinkGoogle(c, cur, c.Query("code")); err != nil {
			var ae *domain.AppError
			if errors.As(err, &ae) {
				c.String(ae.Status, ae.Message)
			} else {
				log.Printf("google link: %v", err)
				c.String(500, "เชื่อม Google ไม่สำเร็จ: "+err.Error())
			}
			return
		}
		c.Redirect(http.StatusFound, "/#/settings")
		return
	}
	if h.cfg.PublicMode {
		c.String(403, "เข้าสู่ระบบผู้ดูแลก่อน แล้วค่อยเชื่อม Google Drive")
		return
	}
	id, err := h.auth.SignIn(c, c.Query("code"))
	if err != nil {
		var ae *domain.AppError
		if errors.As(err, &ae) {
			c.String(ae.Status, ae.Message)
		} else {
			log.Printf("google callback: %v", err)
			c.String(500, "เข้าสู่ระบบไม่สำเร็จ: "+err.Error())
		}
		return
	}
	h.sess.Login(c, id)
	c.Redirect(http.StatusFound, "/")
}

func (h *Handler) devLogin(c *gin.Context) {
	id, err := h.auth.DevSignIn(c)
	if err != nil {
		fail(c, err)
		return
	}
	h.sess.Login(c, id)
	c.Redirect(http.StatusFound, "/")
}

func (h *Handler) me(c *gin.Context) {
	id := h.sess.UserID(c)
	me, err := h.auth.Me(c, id)
	if id == 0 || errors.Is(err, domain.ErrNotFound) {
		if !h.cfg.PublicMode {
			h.sess.Logout(c)
			c.JSON(200, gin.H{"user": nil})
			return
		}
		// Public mode: every visitor gets a guest account so the converter opens straight away.
		if id, err = h.auth.NewGuest(c); err != nil {
			fail(c, err)
			return
		}
		h.sess.Login(c, id)
		me, err = h.auth.Me(c, id)
	}
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"user": me, "features": gin.H{
		"pdf": h.pdf.Enabled(), "browser": false, "driveScope": h.cfg.DriveScopeMode,
		"picker": h.cfg.GoogleAPIKey != "" && h.cfg.GoogleAppID != "" && !me.Guest, "maxLinks": maxLinks(h.cfg, me.Guest),
		"public": h.cfg.PublicMode, "googleConfigured": h.cfg.GoogleConfigured(),
	}})
}

func maxLinks(cfg *config.Config, guest bool) int {
	if guest {
		return min(cfg.MaxLinksPerJob, cfg.GuestMaxLinks)
	}
	return cfg.MaxLinksPerJob
}

func (h *Handler) disconnect(c *gin.Context) {
	err := h.auth.Disconnect(c, uid(c))
	if me, e := h.auth.Me(c, uid(c)); e == nil && !me.Admin {
		h.sess.Logout(c) // Google-login users lose their sign-in; the password admin stays signed in
	}
	ok(c, err)
}

func (h *Handler) picker(c *gin.Context) {
	if h.cfg.GoogleAPIKey == "" {
		c.JSON(404, gin.H{"error": "ยังไม่ได้ตั้งค่า GOOGLE_API_KEY"})
		return
	}
	tok, err := h.auth.PickerToken(c, uid(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"token": tok, "apiKey": h.cfg.GoogleAPIKey, "appId": h.cfg.GoogleAppID, "clientId": h.cfg.GoogleClientID})
}

// ---------- jobs ----------

func (h *Handler) createJob(c *gin.Context) {
	var in usecase.CreateJobInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, domain.NewError(400, "ข้อมูลไม่ถูกต้อง: "+err.Error()))
		return
	}
	id, err := h.jobs.Create(c, uid(c), in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"id": id})
}

func (h *Handler) listJobs(c *gin.Context) {
	rows, err := h.jobs.List(c, uid(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, rows)
}

func (h *Handler) getJob(c *gin.Context) {
	v, err := h.jobs.Get(c, uid(c), c.Param("id"), c.Query("markdown") == "1")
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, v)
}

func (h *Handler) cancelJob(c *gin.Context) { ok(c, h.jobs.Cancel(c, uid(c), c.Param("id"))) }

func (h *Handler) publishJob(c *gin.Context) {
	var body struct {
		Schedule *domain.ScheduleSpec `json:"schedule"`
	}
	_ = c.ShouldBindJSON(&body)
	ok(c, h.jobs.Publish(c, uid(c), c.Param("id"), body.Schedule))
}

func (h *Handler) refreshJob(c *gin.Context) {
	n, err := h.jobs.RefreshAll(c, uid(c), c.Param("id"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"queued": n})
}

func (h *Handler) patchItem(c *gin.Context) {
	var in usecase.PatchItemInput
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, domain.NewError(400, "ข้อมูลไม่ถูกต้อง"))
		return
	}
	ok(c, h.jobs.PatchItem(c, uid(c), c.Param("id"), in))
}

func (h *Handler) retryItem(c *gin.Context) { ok(c, h.jobs.Retry(c, uid(c), c.Param("id"))) }

// ---------- schedules ----------

func (h *Handler) listSchedules(c *gin.Context) {
	v, err := h.sched.Overview(c, uid(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, v)
}

func (h *Handler) ownedItem(c *gin.Context) (string, bool) {
	id, err := h.jobs.OwnedItemID(c, uid(c), c.Param("itemId"))
	if err != nil {
		fail(c, err)
		return "", false
	}
	return id, true
}

func (h *Handler) patchSchedule(c *gin.Context) {
	id, found := h.ownedItem(c)
	if !found {
		return
	}
	var body struct {
		Active *bool `json:"active"`
		domain.ScheduleSpec
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		fail(c, domain.NewError(400, "ข้อมูลไม่ถูกต้อง"))
		return
	}
	var err error
	if body.Active != nil && !*body.Active {
		err = h.sched.Pause(c, id, "หยุดโดยผู้ใช้")
	} else if body.Active != nil && *body.Active {
		err = h.sched.Resume(c, id)
	}
	if err == nil && body.Frequency != "" {
		err = h.sched.Upsert(c, id, body.ScheduleSpec)
	}
	ok(c, err)
}

func (h *Handler) deleteSchedule(c *gin.Context) {
	if id, found := h.ownedItem(c); found {
		ok(c, h.sched.Remove(c, id))
	}
}

func (h *Handler) runSchedule(c *gin.Context) {
	if id, found := h.ownedItem(c); found {
		ok(c, h.sched.RunNow(c, id))
	}
}

func (h *Handler) pauseAll(c *gin.Context) {
	n, err := h.sched.PauseAll(c, uid(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"paused": n})
}

// ---------- settings ----------

func (h *Handler) getSettings(c *gin.Context) {
	v, err := h.settings.Get(c, uid(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, v)
}

func (h *Handler) putSettings(c *gin.Context) {
	var b struct {
		HeaderTemplate string `json:"headerTemplate"`
		DefaultFolder  string `json:"defaultFolder"`
	}
	_ = c.ShouldBindJSON(&b)
	name, err := h.settings.Save(c, uid(c), b.HeaderTemplate, b.DefaultFolder)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true, "folderName": name})
}

func (h *Handler) checkFolder(c *gin.Context) {
	var b struct {
		Folder string `json:"folder"`
	}
	_ = c.ShouldBindJSON(&b)
	f, err := h.settings.CheckFolder(c, uid(c), b.Folder)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, f)
}

type ruleBody struct {
	Domain  string `json:"domain"`
	URL     string `json:"url"`
	Include string `json:"include"`
	Exclude string `json:"exclude"`
}

func (h *Handler) saveRule(c *gin.Context) {
	var b ruleBody
	_ = c.ShouldBindJSON(&b)
	r, err := h.settings.SaveRule(c, uid(c), b.Domain, b.Include, b.Exclude)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"id": r.ID})
}

func (h *Handler) testRule(c *gin.Context) {
	var b ruleBody
	_ = c.ShouldBindJSON(&b)
	title, preview, err := h.settings.TestRule(c, b.URL, b.Include, b.Exclude)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"title": title, "preview": preview})
}

func (h *Handler) deleteRule(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	ok(c, h.settings.DeleteRule(c, uid(c), id))
}

func (h *Handler) saveDify(c *gin.Context) {
	var b struct {
		BaseURL          string `json:"baseUrl"`
		APIKey           string `json:"apiKey"`
		DefaultDatasetID string `json:"defaultDatasetId"`
		Indexing         string `json:"indexing"`
	}
	_ = c.ShouldBindJSON(&b)
	ok(c, h.settings.SaveDify(c, uid(c), b.BaseURL, b.APIKey, b.DefaultDatasetID, b.Indexing))
}

func (h *Handler) difyDatasets(c *gin.Context) {
	connected, list, err := h.settings.DifyDatasets(c, uid(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"connected": connected, "data": list})
}

// ---------- PDF upload: streamed straight to pdf-worker ----------

func (h *Handler) uploadPDF(c *gin.Context) {
	if !h.pdf.Enabled() {
		c.JSON(503, gin.H{"error": "ยังไม่ได้เปิดบริการ pdf-worker"})
		return
	}
	req, _ := http.NewRequestWithContext(c, http.MethodPost, h.pdf.Base()+"/files", c.Request.Body)
	req.Header.Set("Content-Type", c.GetHeader("Content-Type"))
	req.ContentLength = c.Request.ContentLength
	if t := h.pdf.Token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fail(c, domain.NewError(502, "ส่งไฟล์ไป pdf-worker ไม่สำเร็จ"))
		return
	}
	defer res.Body.Close()
	c.Status(res.StatusCode)
	c.Header("Content-Type", "application/json")
	_, _ = io.Copy(c.Writer, res.Body)
}
