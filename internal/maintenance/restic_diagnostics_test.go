package maintenance

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"

	"household/internal/config"
)

func TestRepositoryInitializationReportsActualFailure(t *testing.T) {
	original := resticTool
	t.Cleanup(func() { resticTool = original })
	target := config.BackupTarget{Repository: "s3:https://storage.example.test/bucket/household"}
	call := 0
	resticTool = func(_ context.Context, _ []string, args []string) ([]byte, []byte, error) {
		call++
		switch call {
		case 1:
			if len(args) != 2 || args[0] != "cat" || args[1] != "config" {
				t.Fatalf("expected a config check, got %v", args)
			}
			return nil, []byte("config file not found"), errors.New("tool fixture")
		case 2:
			if args[0] != "init" {
				t.Fatalf("expected an initialization, got %v", args)
			}
			return nil, []byte("AccessDenied"), errors.New("tool fixture")
		default:
			t.Fatal("unexpected extra repository command")
			return nil, nil, nil
		}
	}
	if got := ResultCode(ensureRepository(t.Context(), target, nil)); got != "access_denied" || call != 2 {
		t.Fatalf("expected the initialization failure, got %q after %d calls", got, call)
	}
	resticTool = func(_ context.Context, _ []string, args []string) ([]byte, []byte, error) {
		if args[0] == "init" {
			t.Fatal("a denied config read must never trigger initialization")
		}
		return nil, []byte("403 Forbidden; repository does not exist"), errors.New("tool fixture")
	}
	if got := ResultCode(ensureRepository(t.Context(), target, nil)); got != "access_denied" {
		t.Fatalf("expected access denial for an ambiguous config read, got %q", got)
	}
}

func TestResticExitTenDiagnostics(t *testing.T) {
	const fixture = "HOUSEHOLD_TEST_RESTIC_EXIT_TEN"
	if os.Getenv(fixture) == "1" {
		os.Exit(10)
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestResticExitTenDiagnostics$")
	cmd.Env = append(os.Environ(), fixture+"=1")
	exitErr := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(exitErr, &exit) || exit.ExitCode() != 10 {
		t.Fatalf("expected a real exit-10 process error, got %v", exitErr)
	}

	original := resticTool
	t.Cleanup(func() { resticTool = original })
	target := config.BackupTarget{Repository: "s3:https://storage.example.test/bucket/household"}
	for _, tc := range []struct {
		name       string
		diagnostic string
		want       string
	}{
		{"access denial", "AccessDenied while opening config", "access_denied"},
		{"transport", "dial tcp: connection refused", "repository_unreachable"},
		{"unclassified fallback", "could not open the configuration", "repository_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var opened bool
			resticTool = func(_ context.Context, _ []string, args []string) ([]byte, []byte, error) {
				if args[0] == "init" {
					t.Fatal("diagnostic must gate initialization before retrying the command")
				}
				opened = true
				return nil, []byte(tc.diagnostic), exitErr
			}
			_, err := restic(t.Context(), target, nil, "cat", "config")
			if got := ResultCode(err); got != tc.want || !opened {
				t.Fatalf("exit 10: want %q, got %q", tc.want, got)
			}
			if tc.want != "repository_missing" {
				if got := ResultCode(ensureRepository(t.Context(), target, nil)); got != tc.want {
					t.Fatalf("unexpected repository initialization result %q", got)
				}
			}
		})
	}
}
