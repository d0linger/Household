package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"household/internal/backupplan"
	"household/internal/config"
	"household/internal/maintenance"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// runRestoreCheck verifies one snapshot in a disposable database:
//
//	household restore-check [--offsite] SNAPSHOT|latest
func runRestoreCheck(ctx context.Context, pool *pgxpool.Pool, cfg config.Config, args []string) error {
	kind := "local"
	if len(args) > 0 && args[0] == "--offsite" {
		kind, args = "offsite", args[1:]
	}
	// Validate before any work, including creating a restore target.
	if len(args) != 1 || args[0] != "latest" && !backupplan.ValidSnapshot(args[0]) {
		return errors.New("usage: household restore-check [--offsite] FULL_SNAPSHOT_ID|latest")
	}
	local, err := config.LocalBackupTarget()
	if err != nil {
		return err
	}
	target := local
	if kind == "offsite" {
		offsite, err := config.OffsiteBackupTarget(local)
		if err != nil {
			return err
		}
		if offsite == nil {
			return maintenance.ErrOffsiteUnconfigured
		}
		target = *offsite
	}
	snapshot := args[0]
	if snapshot == "latest" {
		if err = pool.QueryRow(ctx, `SELECT snapshot_reference FROM core.backup_runs WHERE repository_reference=$1 AND state='complete'
 AND snapshot_reference IS NOT NULL ORDER BY started_at DESC LIMIT 1`, target.Label()).Scan(&snapshot); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errors.New("no completed snapshot to check")
			}
			return fmt.Errorf("look up the latest snapshot: %w", err)
		}
	}
	superuser, err := config.Secret("POSTGRES_PASSWORD")
	if err != nil {
		return err
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	// An explicit target, or no superuser to create one, is used as it is.
	// Otherwise each check gets its own database on the source's server.
	return maintenance.CheckRestore(verifyCtx, cfg.DatabaseURL, superuser, func(targetURL string) error {
		return maintenance.VerifyRestore(verifyCtx, pool, cfg, target, snapshot, targetURL)
	})
}

// runBackupHealth is the backup service's container healthcheck: it fails
// when a target's last attempt failed or its schedule is overdue.
func runBackupHealth(ctx context.Context, pool *pgxpool.Pool) error {
	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	report, err := maintenance.CheckHealth(checkCtx, pool)
	if err != nil {
		return err
	}
	if err = json.NewEncoder(os.Stdout).Encode(report); err != nil {
		return err
	}
	if !report.Healthy {
		return errors.New("backup overdue or failed")
	}
	return nil
}

// runBackupService runs the owner backup service or one of its operator
// commands that need the live-restore machinery:
//
//	household backup-loop
//	household restore [--offsite] SNAPSHOT --confirm "WIEDERHERSTELLEN XXXXXXXX"
//	household restore-rollback
func runBackupService(ctx context.Context, log *slog.Logger, cfg config.Config, command string, args []string) error {
	superuser, err := config.Secret("POSTGRES_PASSWORD")
	if err != nil {
		return err
	}
	service, err := maintenance.NewService(ctx, cfg, log, superuser)
	if err != nil {
		return err
	}
	defer service.Close()
	switch command {
	case "backup-loop":
		return service.Run(ctx)
	case "restore-rollback":
		if len(args) != 0 {
			return errors.New("usage: household restore-rollback")
		}
		if err = service.RecoverRestore(ctx); err != nil {
			return err
		}
		return service.Rollback(ctx)
	}
	target := "local"
	if len(args) > 0 && args[0] == "--offsite" {
		target, args = "offsite", args[1:]
	}
	if len(args) != 3 || args[1] != "--confirm" || !backupplan.ValidSnapshot(args[0]) {
		return errors.New(`usage: household restore [--offsite] FULL_SNAPSHOT_ID --confirm "WIEDERHERSTELLEN XXXXXXXX"`)
	}
	if !backupplan.ConfirmationMatches(args[0], args[2]) {
		return errors.New("the confirmation must be: " + backupplan.RestoreConfirmation(args[0]))
	}
	if err = service.RecoverRestore(ctx); err != nil {
		return err
	}
	return service.RestoreNow(ctx, value.UUID(), target, args[0], backupplan.RestoreConfirmation(args[0]))
}
