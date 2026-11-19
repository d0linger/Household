package web

import (
	"context"
	"errors"
	"time"

	"household/internal/buildinfo"
	"household/internal/pwned"
	"household/internal/store"
	"household/internal/updatecheck"
)

// optionalChecks holds the two opt-in outbound lookups. Both stay nil unless
// configured, so a default installation never contacts these services.
type optionalChecks struct {
	breach  *pwned.Checker
	updates *updatecheck.Checker
}

func (s *Server) configureOptionalChecks() {
	if s.Cfg.PasswordBreachCheck {
		s.checks.breach = pwned.New()
	}
	if s.Cfg.UpdateCheck {
		s.checks.updates = updatecheck.New(buildinfo.Revision)
	}
}

// rejectBreachedPassword refuses a new password that appears in known data
// breaches. An unreachable service accepts the password: the check is a hint.
func (s *Server) rejectBreachedPassword(ctx context.Context, password string) error {
	if s.checks.breach == nil {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	count, err := s.checks.breach.Count(checkCtx, password)
	if errors.Is(err, pwned.ErrUnavailable) {
		s.Log.Warn("password breach check unavailable; password accepted")
		return nil
	}
	if err != nil {
		return err
	}
	if count > 0 {
		return store.Invalid{Message: "Dieses Passwort ist aus Datenlecks bekannt. Bitte wähle ein anderes."}
	}
	return nil
}

// WatchUpdates refreshes the optional update lookup; the checker itself asks
// at most once a day.
func (s *Server) WatchUpdates(ctx context.Context) {
	if s.checks.updates == nil {
		return
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		lookupCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		result := s.checks.updates.Check(lookupCtx, time.Now())
		cancel()
		if result.Failed {
			s.Log.Warn("update check failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
