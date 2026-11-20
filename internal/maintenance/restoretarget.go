package maintenance

import (
	"context"
	"errors"
	"time"

	"household/internal/config"
	"household/internal/database"
)

// WithRestoreTarget creates a fresh, uniquely named restore database on the
// server of the source connection, prepares the Household roles in it, runs
// verify against it with the target's credentials, and always drops it again.
func WithRestoreTarget(ctx context.Context, source, target, superuser string, verify func(target string) error) error {
	owner, err := config.Secret("HOUSEHOLD_OWNER_PASSWORD")
	if err != nil {
		return err
	}
	app, err := config.Secret("HOUSEHOLD_APP_PASSWORD")
	if err != nil || owner == "" || app == "" {
		return errors.New("restore preparation needs HOUSEHOLD_OWNER_PASSWORD and HOUSEHOLD_APP_PASSWORD")
	}
	maintenanceURL, err := config.RestoreMaintenanceURL(source, superuser)
	if err != nil {
		return err
	}
	return database.WithAutomaticRestoreDatabase(ctx, maintenanceURL, func(name string) error {
		restoreURL, err := config.WithDatabase(maintenanceURL, name)
		if err != nil {
			return err
		}
		runTarget, err := config.WithDatabase(target, name)
		if err != nil {
			return err
		}
		if err = ValidateAutomaticRestoreTarget(source, runTarget); err != nil {
			return err
		}
		setupCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		if err = database.PrepareRestoreDatabase(setupCtx, restoreURL, owner, app); err != nil {
			return err
		}
		return verify(runTarget)
	})
}

// CheckRestore verifies one snapshot of target in a disposable database: an
// explicit HOUSEHOLD_RESTORE_DATABASE_URL (or, without a superuser password,
// household_restore) is used as it is; otherwise each check gets its own
// database on the source's server.
func CheckRestore(ctx context.Context, cfgSource string, superuser string, verify func(target string) error) error {
	target, explicit, err := config.RestoreTarget(cfgSource)
	if err != nil {
		return err
	}
	if err = ValidateRestoreTarget(cfgSource, target); err != nil {
		return err
	}
	if explicit || superuser == "" {
		return verify(target)
	}
	return WithRestoreTarget(ctx, cfgSource, target, superuser, verify)
}
