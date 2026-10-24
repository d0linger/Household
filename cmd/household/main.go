// Household is a self-hosted modular household PWA.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"household/internal/auth"
	"household/internal/config"
	"household/internal/database"
	"household/internal/maintenance"
	"household/internal/modules"
	"household/internal/web"
	"household/internal/worker"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("household stopped", "error", err)
		os.Exit(1)
	}
}
func run(log *slog.Logger) error {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command == "version" {
		fmt.Println("Household 0.1.0")
		return nil
	}
	if command == "healthcheck" {
		client := &http.Client{Timeout: 3 * time.Second}
		response, err := client.Get("http://127.0.0.1:8080/health/ready")
		if err != nil {
			return errors.New("health endpoint unavailable")
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != 200 {
			return errors.New("application not ready")
		}
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if command == "migrate" {
		migrationCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		return database.Migrate(migrationCtx, cfg.DatabaseURL)
	}
	pool, err := database.Open(ctx, cfg.DatabaseURL, cfg.MaxConnections)
	if err != nil {
		return err
	}
	defer pool.Close()
	switch command {
	case "backup":
		backupCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		snapshot, err := maintenance.Backup(backupCtx, pool, cfg)
		if err != nil {
			return err
		}
		log.Info("encrypted backup completed", "snapshot", snapshot)
		return nil
	case "restore-check":
		if len(os.Args) != 3 {
			return errors.New("usage: household restore-check SNAPSHOT")
		}
		target := os.Getenv("HOUSEHOLD_RESTORE_DATABASE_URL")
		if file := os.Getenv("HOUSEHOLD_RESTORE_DATABASE_URL_FILE"); file != "" {
			// security: operator-owned environment setting, never HTTP input.
			b, e := os.ReadFile(file) // #nosec G304 G703 -- trusted operator secret path
			if e != nil {
				return e
			}
			target = strings.TrimSpace(string(b))
		}
		verifyCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		return maintenance.VerifyRestore(verifyCtx, pool, cfg, os.Args[2], target)
	case "purge":
		if len(os.Args) != 3 {
			return errors.New("usage: household purge REQUEST_UUID")
		}
		return maintenance.Purge(ctx, pool, os.Args[2])
	case "backup-loop":
		for {
			backupCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
			snapshot, err := maintenance.Backup(backupCtx, pool, cfg)
			cancel()
			if err != nil {
				log.Error("daily backup failed", "error", err)
			} else {
				log.Info("daily encrypted backup completed", "snapshot", snapshot)
			}
			zone, err := time.LoadLocation("Europe/Vienna")
			if err != nil {
				return err
			}
			now := time.Now().In(zone)
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 3, 0, 0, 0, zone)
			timer := time.NewTimer(time.Until(next))
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
	}
	if command == "recover" {
		if len(os.Args) != 3 && (len(os.Args) != 4 || os.Args[3] != "--reset-mfa") {
			return errors.New("usage: household recover USERNAME; password supplied through HOUSEHOLD_ADMIN_PASSWORD_FILE")
		}
		a, err := auth.New(pool, cfg)
		if err != nil {
			return err
		}
		if !auth.PasswordValid(cfg.AdminPassword) {
			return errors.New("recovery password must contain at least 14 characters and at most 72 bytes")
		}
		return a.RecoverPassword(ctx, os.Args[2], cfg.AdminPassword, len(os.Args) == 4)
	}
	if command != "serve" {
		return errors.New("unknown command; use serve, migrate, healthcheck, recover, or version")
	}
	if err = database.Ready(ctx, pool); err != nil {
		return err
	}
	app, err := web.New(ctx, pool, cfg, modules.Registry(), log)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: cfg.Listen, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16 << 10}
	background := &worker.Worker{Pool: pool, Log: log, Cfg: cfg}
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); background.Run(ctx) }()
	defer func() { stop(); <-workerDone }()
	result := make(chan error, 1)
	go func() {
		log.Info("household listening", "address", cfg.Listen, "origin", cfg.Origin)
		result <- server.ListenAndServe()
	}()
	select {
	case err = <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}
	app.Stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err = server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		return err
	}
	select {
	case <-workerDone:
	case <-shutdownCtx.Done():
		return shutdownCtx.Err()
	}
	return nil
}
