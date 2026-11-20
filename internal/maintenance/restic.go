package maintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"household/internal/config"
)

// ToolError reports a failed operator tool with a public, nonsecret code that
// the backup page can explain. Tool output itself is never stored or shown:
// it can contain repository locations or provider messages.
type ToolError struct {
	Program string
	Code    string
	err     error
}

func (e *ToolError) Error() string {
	return fmt.Sprintf("%s failed (%s); inspect the operator tool configuration", e.Program, e.Code)
}

func (e *ToolError) Unwrap() error { return e.err }

// ResultCode maps an error to the public code stored with backup work.
func ResultCode(err error) string {
	var tool *ToolError
	switch {
	case err == nil:
		return "ok"
	case errors.As(err, &tool):
		return tool.Code
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, ErrRestoreIncompatible):
		return "snapshot_incompatible"
	case errors.Is(err, ErrLiveKeyMissing):
		return "live_key_missing"
	case errors.Is(err, ErrInsufficientSpace):
		return "disk_full"
	case errors.Is(err, ErrDrainTimeout):
		return "drain_timeout"
	}
	return "failed"
}

// classifyResticOutput turns known restic diagnostics into stable codes.
func classifyResticOutput(stderr []byte) string {
	text := strings.ToLower(string(stderr))
	switch {
	// Access and connectivity failures come first: a refused or unreachable
	// repository often also reports that its config file could not be opened,
	// and must never be mistaken for a missing repository to initialize.
	case strings.Contains(text, "wrong password") || strings.Contains(text, "no key found"):
		return "wrong_password"
	case strings.Contains(text, "nosuchbucket") || strings.Contains(text, "bucket does not exist"):
		return "bucket_missing"
	case strings.Contains(text, "access denied") || strings.Contains(text, "accessdenied") || strings.Contains(text, "403") ||
		strings.Contains(text, "signaturedoesnotmatch") || strings.Contains(text, "invalidaccesskeyid") || strings.Contains(text, "401 unauthorized"):
		return "access_denied"
	case strings.Contains(text, "connection refused") || strings.Contains(text, "no such host") || strings.Contains(text, "i/o timeout") ||
		strings.Contains(text, "dial tcp") || strings.Contains(text, "tls:") || strings.Contains(text, "certificate"):
		return "repository_unreachable"
	case strings.Contains(text, "no space left"):
		return "disk_full"
	// Only explicit not-found diagnostics; "unable to open config file" alone
	// also covers access and transport failures.
	case strings.Contains(text, "is there a repository at the following location") || strings.Contains(text, "repository does not exist") ||
		strings.Contains(text, "config file not found"):
		return "repository_missing"
	case strings.Contains(text, "repository contains errors") || strings.Contains(text, "check failed") || strings.Contains(text, "pack") && strings.Contains(text, "damaged"):
		return "repository_damaged"
	case strings.Contains(text, "unable to create lock") || strings.Contains(text, "repository is already locked"):
		return "repository_locked"
	}
	return "tool_failed"
}

// resticCacheDirectory keeps the repository index on the backup work volume:
// a remote repository's cache does not fit the container's small /tmp.
func resticCacheDirectory() string {
	if dir := os.Getenv("RESTIC_CACHE_DIR"); dir != "" {
		return dir
	}
	return workDirectory() + "/restic-cache"
}

func workDirectory() string {
	if base := os.Getenv("HOUSEHOLD_BACKUP_WORK"); base != "" {
		return base
	}
	return "/work"
}

// resticEnvironment passes restic only process basics plus the variables of the
// repositories this one command uses, never the whole service environment.
func resticEnvironment(target config.BackupTarget, source *config.BackupTarget) []string {
	env := []string{"RESTIC_CACHE_DIR=" + resticCacheDirectory(), "RESTIC_PROGRESS_FPS=0.01"}
	for _, name := range []string{"PATH", "HOME", "TMPDIR", "TZ", "SSL_CERT_FILE", "SSL_CERT_DIR", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	if _, ok := os.LookupEnv("HOME"); !ok {
		env = append(env, "HOME="+workDirectory())
	}
	env = append(env, target.Environment()...)
	if source != nil {
		env = append(env, source.SourceEnvironment()...)
	}
	return env
}

// resticTool is replaced in tests that exercise the orchestration without a
// restic binary.
var resticTool = func(ctx context.Context, env, args []string) (stdout, stderr []byte, err error) {
	// security: the executable is fixed, arguments never pass a shell, and the
	// environment is built from validated configuration only.
	cmd := exec.CommandContext(ctx, "restic", args...) // #nosec G204 G702 -- fixed operator tool; no shell
	cmd.Env = env
	cmd.WaitDelay = 10 * time.Second
	var out, errOut limitedBuffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err = cmd.Run()
	return out.Bytes(), errOut.Bytes(), err
}

// restic runs one restic command against target (and source for copy/init).
func restic(ctx context.Context, target config.BackupTarget, source *config.BackupTarget, args ...string) ([]byte, error) {
	stdout, stderr, err := resticTool(ctx, resticEnvironment(target, source), args)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// restic 0.17+ exits with 10 exactly when the repository does not exist;
		// that structured result wins over text matches, which can come from
		// the repository path itself.
		code := classifyResticOutput(stderr)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 10 {
			code = "repository_missing"
		}
		return nil, &ToolError{Program: "restic", Code: code, err: err}
	}
	return stdout, nil
}

// resticSnapshot is the subset of `restic snapshots --json` Household reads.
type resticSnapshot struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Original string    `json:"original"`
	Tags     []string  `json:"tags"`
}

func listSnapshots(ctx context.Context, target config.BackupTarget) ([]resticSnapshot, error) {
	raw, err := restic(ctx, target, nil, "snapshots", "--tag", "household", "--json")
	if err != nil {
		return nil, err
	}
	var snapshots []resticSnapshot
	if err = json.Unmarshal(raw, &snapshots); err != nil {
		return nil, &ToolError{Program: "restic", Code: "unexpected_output", err: err}
	}
	return snapshots, nil
}

// ensureRepository opens target and initializes it when it does not exist
// yet. An offsite repository is created with the local chunker parameters so
// `restic copy` deduplicates against earlier copies.
func ensureRepository(ctx context.Context, target config.BackupTarget, chunkerSource *config.BackupTarget) error {
	_, err := restic(ctx, target, nil, "cat", "config")
	if err == nil {
		return nil
	}
	var tool *ToolError
	if !errors.As(err, &tool) || tool.Code != "repository_missing" {
		return err // only a confirmed missing repository is initialized
	}
	args := []string{"init"}
	if chunkerSource != nil {
		args = append(args, "--copy-chunker-params")
	}
	if _, initErr := restic(ctx, target, chunkerSource, args...); initErr != nil {
		// The original error explains the repository better than a refused init.
		return errors.Join(err, initErr)
	}
	return nil
}

// forget applies a retention policy to Household snapshots. An empty grouping
// ignores temporary paths, container hosts and release tags.
func forget(ctx context.Context, target config.BackupTarget, daily, weekly, monthly int) error {
	_, err := restic(ctx, target, nil, "forget", "--tag", "household", "--group-by", "",
		"--keep-daily", fmt.Sprint(daily), "--keep-weekly", fmt.Sprint(weekly), "--keep-monthly", fmt.Sprint(monthly), "--prune")
	return err
}

// backupSummary is restic's final JSON message of `restic backup --json`.
type backupSummary struct {
	MessageType string `json:"message_type"`
	SnapshotID  string `json:"snapshot_id"`
	DataAdded   int64  `json:"data_added"`
	TotalBytes  int64  `json:"total_bytes_processed"`
}

func parseBackupSummary(output []byte) backupSummary {
	var result backupSummary
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		var event backupSummary
		if json.Unmarshal(line, &event) == nil && event.SnapshotID != "" {
			result = event
		}
	}
	return result
}
