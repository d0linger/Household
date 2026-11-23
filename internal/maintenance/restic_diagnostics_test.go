package maintenance

import (
	"context"
	"errors"
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
