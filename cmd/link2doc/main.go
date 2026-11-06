// Command link2doc runs the web API, the background worker, or both (MODE=all|api|worker).
package main

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nuttapong111/link2doc/internal/config"
	httpd "github.com/nuttapong111/link2doc/internal/delivery/http"
	"github.com/nuttapong111/link2doc/internal/delivery/worker"
	"github.com/nuttapong111/link2doc/internal/infrastructure/converter"
	"github.com/nuttapong111/link2doc/internal/infrastructure/crypto"
	"github.com/nuttapong111/link2doc/internal/infrastructure/dify"
	"github.com/nuttapong111/link2doc/internal/infrastructure/fetcher"
	"github.com/nuttapong111/link2doc/internal/infrastructure/google"
	"github.com/nuttapong111/link2doc/internal/infrastructure/pdfworker"
	"github.com/nuttapong111/link2doc/internal/repository/postgres"
	"github.com/nuttapong111/link2doc/internal/usecase"
	"github.com/nuttapong111/link2doc/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		log.Fatalf("SCHEDULE_TZ: %v", err)
	}
	db, err := postgres.Open(cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	if err := postgres.Migrate(db); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	cipher, err := crypto.New(cfg.EncryptionKey)
	if err != nil {
		log.Fatal(err)
	}

	// Wire adapters into the use cases (dependency injection by hand).
	gauth := google.NewAuth(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.AppURL, cfg.DriveScopeMode, cfg.AllowedDomains)
	pdf := pdfworker.New(cfg.PDFWorkerURL, cfg.PDFWorkerToken)
	queue := postgres.NewTaskQueue(db)
	deps := &usecase.Deps{
		Users: postgres.NewUserRepo(db), Jobs: postgres.NewJobRepo(db), Items: postgres.NewItemRepo(db),
		Schedules: postgres.NewScheduleRepo(db), Settings: postgres.NewSettingsRepo(db), Queue: queue,
		Fetcher:   fetcher.New(cfg.UserAgent, cfg.AllowPrivate, time.Duration(cfg.PerDomainDelayMs)*time.Millisecond),
		Converter: converter.New(), PDF: pdf, Auth: gauth, Docs: gauth, Dify: dify.Factory{}, Cipher: cipher,
		Location: loc, MaxLinksPerJob: cfg.MaxLinksPerJob,
		PublicMode: cfg.PublicMode, AdminEmails: cfg.AdminEmails, GuestMaxLinks: cfg.GuestMaxLinks, GuestJobsPerHr: cfg.GuestJobsPerHr,
	}
	sched := &usecase.ScheduleService{Deps: deps}
	fetch := &usecase.FetchService{Deps: deps}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan struct{}, 2)
	running := 0

	if cfg.Mode == "all" || cfg.Mode == "worker" {
		running++
		w := &worker.Worker{
			Queue: queue, Fetch: fetch, Schedule: sched, Concurrency: cfg.FetchConcurrency,
			Publish: &usecase.PublishService{Deps: deps, Schedule: sched},
			Refresh: &usecase.RefreshService{Deps: deps, Fetch: fetch, Schedule: sched},
		}
		go func() { w.Run(ctx); done <- struct{}{} }()
	}

	if cfg.Mode == "all" || cfg.Mode == "api" {
		running++
		var static fs.FS = web.Static()
		if cfg.StaticDir != "" {
			static = os.DirFS(cfg.StaticDir) // edit the UI without rebuilding
		}
		router := httpd.NewRouter(cfg, httpd.Services{
			Auth: &usecase.AuthService{Deps: deps, AllowedDomains: cfg.AllowedDomains, AllowedEmails: cfg.AllowedEmails,
				AdminUsername: cfg.AdminUsername, AdminPassword: cfg.AdminPassword},
			Jobs:     &usecase.JobService{Deps: deps},
			Schedule: sched,
			Settings: &usecase.SettingsService{Deps: deps},
			PDF:      pdf,
		}, static)
		srv := &http.Server{Addr: ":" + cfg.Port, Handler: router, ReadHeaderTimeout: 15 * time.Second}
		go func() {
			log.Printf("web listening on :%s (mode %s)", cfg.Port, cfg.Mode)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatal(err)
			}
		}()
		go func() {
			<-ctx.Done()
			sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_ = srv.Shutdown(sctx)
			done <- struct{}{}
		}()
	}

	for i := 0; i < running; i++ {
		<-done
	}
	log.Print("stopped")
}
