// Household is a self-hosted modular household PWA.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
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

// healthURL probes the configured listener; wildcard binds are reached on loopback.
// The health check runs without secrets, so it cannot use config.Load.
func healthURL(listen string) (string, error) {
	if listen == "" {
		listen = config.DefaultListen
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return "", errors.New("invalid HOUSEHOLD_LISTEN for health check")
	}
	if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/health/ready", nil
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
	if command == "push-keygen" {
		return runPushKeygen(os.Args[2:])
	}
	if command == "encryption-keygen" {
		return runEncryptionKeygen(os.Args[2:])
	}
	var cryptoBatch int
	var retirementID string
	var argumentErr error
	switch command {
	case "encryption-reencrypt":
		cryptoBatch, argumentErr = encryptionBatch(os.Args[2:])
	case "encryption-retirement-check":
		retirementID, argumentErr = encryptionRetirementID(os.Args[2:])
	}
	if argumentErr != nil {
		return argumentErr
	}
	if command == "healthcheck" {
		target, err := healthURL(os.Getenv("HOUSEHOLD_LISTEN"))
		if err != nil {
			return err
		}
		client := &http.Client{Timeout: 3 * time.Second}
		// security: the operator-owned listen address selects only this process's own listener.
		response, err := client.Get(target) // #nosec G704 -- operator configuration, never request input
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
	case "encryption-reencrypt":
		rotationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		report, err := maintenance.Reencrypt(rotationCtx, pool, cfg, cryptoBatch)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(report)
	case "encryption-retirement-check":
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		report, err := maintenance.CheckKeyRetirement(checkCtx, pool, cfg, retirementID)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(report)
	case "pantry-check", "pantry-rebuild":
		checkCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		drift, err := maintenance.PantryProjection(checkCtx, pool, command == "pantry-rebuild")
		if err != nil {
			return err
		}
		log.Info("stock projection checked", "mismatched_lots", drift, "rebuilt", command == "pantry-rebuild")
		if drift != 0 {
			return errors.New("stock projection differs from journal; operator review required")
		}
		return nil
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
		return errors.New("unknown command; use serve, migrate, healthcheck, recover, version, push-keygen, encryption-keygen, encryption-reencrypt or encryption-retirement-check")
	}
	if err = database.Ready(ctx, pool); err != nil {
		return err
	}
	app, err := web.New(ctx, pool, cfg, modules.Registry(), log)
	if err != nil {
		return err
	}
	defer app.Push.Close()
	server := &http.Server{Addr: cfg.Listen, Handler: app.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16 << 10}
	background := &worker.Worker{Pool: pool, Log: log, Cfg: cfg, Push: app.Push}
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
