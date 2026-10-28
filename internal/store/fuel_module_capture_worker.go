package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// FuelCaptureSource is a Core-authorized immutable local file reference.
type FuelCaptureSource struct {
	JobID, AttachmentID, StorageKey, MediaType, SHA256 string
	SizeBytes                                          int64
}

type fuelCaptureLease struct {
	identity                          auth.Identity
	job, token, vehicle, series, kind string
	generation                        int64
	source                            FuelCaptureSource
}

func (s *Store) fuelModuleCaptureClaim(ctx context.Context) (fuelCaptureLease, error) {
	out := fuelCaptureLease{}
	var household string
	err := s.Pool.QueryRow(ctx, `SELECT id,household_id FROM fuel.capture_jobs WHERE
 (state='queued' OR state='running' AND lease_until<clock_timestamp())
 AND(next_attempt_at<=clock_timestamp() OR attempts>=5 OR expires_at<=clock_timestamp())
 ORDER BY next_attempt_at,id LIMIT 1`).Scan(&out.job, &household)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	ctx = audit.WithHousehold(audit.WithSystem(ctx, "worker"), household)
	tx, err := audit.Begin(ctx, s.Pool)
	if err != nil {
		return out, err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, `SELECT id FROM core.households WHERE id=$1 FOR SHARE`, household); err != nil {
		return out, err
	}
	var sourceHash string
	var terminal bool
	err = tx.QueryRow(ctx, `SELECT j.user_id,j.membership_id,j.session_id,j.auth_version,m.role,j.vehicle_id,coalesce(j.odometer_series_id::text,''),
 j.kind,j.generation,j.attachment_id,encode(j.attachment_sha256,'hex'),j.attempts>=5 OR j.expires_at<=clock_timestamp()
 FROM fuel.capture_jobs j JOIN core.memberships m ON m.id=j.membership_id AND m.household_id=j.household_id
 WHERE j.id=$1 AND (j.state='queued' OR j.state='running' AND j.lease_until<clock_timestamp())
 AND(j.next_attempt_at<=clock_timestamp() OR j.attempts>=5 OR j.expires_at<=clock_timestamp())`, out.job).Scan(
		&out.identity.UserID, &out.identity.MembershipID, &out.identity.SessionID, &out.identity.AuthVersion, &out.identity.Role,
		&out.vehicle, &out.series, &out.kind, &out.generation, &out.source.AttachmentID, &sourceHash, &terminal)
	if errors.Is(err, pgx.ErrNoRows) {
		return fuelCaptureLease{}, nil
	}
	if err != nil {
		return out, err
	}
	out.identity.HouseholdID = household
	expire := func() (fuelCaptureLease, error) {
		var changed bool
		err := tx.QueryRow(ctx, `SELECT fuel.capture_transition($1,NULL,'expire','','{}','[]','')`, out.job).Scan(&changed)
		if err != nil {
			return fuelCaptureLease{}, err
		}
		return fuelCaptureLease{}, tx.Commit(ctx)
	}
	// Expired/exhausted jobs must reach the verified terminal transition even
	// when their retry time is in the future. They must never call the extractor.
	if terminal {
		return expire()
	}
	if err = checkSession(ctx, tx, out.identity); err != nil {
		if errors.Is(err, ErrForbidden) {
			return expire()
		}
		return out, err
	}
	if current, err := requireModule(ctx, tx, out.identity, "fuel"); err != nil || current != out.generation {
		if errors.Is(err, ErrNotFound) || err == nil {
			return expire()
		}
		if err != nil {
			return out, err
		}
		return out, ErrConflict
	}
	scope, err := fuelModuleScope(ctx, tx, out.identity)
	if err != nil || !scope.CanWrite || scope.Generation != out.generation {
		if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) || err == nil {
			return expire()
		}
		if err != nil {
			return out, err
		}
		return out, ErrForbidden
	}
	attachment, err := fuelModuleCoreAttachment(ctx, tx, out.identity, out.source.AttachmentID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return expire()
		}
		return out, err
	}
	if String(attachment["sha256"]) != sourceHash || String(attachment["resource_id"]) != out.vehicle {
		return expire()
	}
	var claimed bool
	err = tx.QueryRow(ctx, `SELECT true FROM fuel.capture_jobs WHERE id=$1 AND household_id=$2
 AND user_id=$3 AND membership_id=$4 AND session_id=$5 AND auth_version=$6 AND generation=$7
 AND (state='queued' OR state='running' AND lease_until<clock_timestamp()) AND attempts<5 AND expires_at>clock_timestamp()
 FOR UPDATE SKIP LOCKED`, out.job, household, out.identity.UserID, out.identity.MembershipID, out.identity.SessionID, out.identity.AuthVersion, out.generation).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return fuelCaptureLease{}, nil
	}
	if err != nil {
		return out, err
	}
	out.source.JobID = out.job
	out.source.StorageKey = String(attachment["storage_key"])
	out.source.MediaType = String(attachment["media_type"])
	out.source.SHA256 = sourceHash
	out.source.SizeBytes = Int(attachment["size_bytes"])
	out.token = value.UUID()
	var transitioned bool
	if err = tx.QueryRow(ctx, `SELECT fuel.capture_transition($1,$2,'claim','','{}','[]','')`, out.job, out.token).Scan(&transitioned); err != nil {
		return out, err
	}
	if !transitioned {
		return fuelCaptureLease{}, nil
	}
	return out, tx.Commit(ctx)
}

// FuelModuleCaptureHousekeeping processes at most one local proposal. It never
// confirms values or creates business facts, and performs OCR outside a DB tx.
func (s *Store) FuelModuleCaptureHousekeeping(ctx context.Context, enabled bool, extract func(context.Context, FuelCaptureSource) (string, error)) error {
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	lease, err := s.fuelModuleCaptureClaim(ctx)
	if err != nil || lease.job == "" {
		return err
	}
	text := ""
	code := ""
	if !enabled {
		code = "ocr_disabled"
	} else {
		text, err = extract(ctx, lease.source)
		if err != nil {
			code = "ocr_failed"
			text = ""
		}
	}
	if len(text) > 65536 {
		code = "ocr_output_limit"
		text = ""
	}
	ctx = audit.WithHousehold(audit.WithSystem(ctx, "worker"), lease.identity.HouseholdID)
	tx, err := audit.Begin(ctx, s.Pool)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, `SELECT id FROM core.households WHERE id=$1 FOR SHARE`, lease.identity.HouseholdID); err != nil {
		return err
	}
	if err = checkSession(ctx, tx, lease.identity); err != nil {
		return err
	}
	if current, err := requireModule(ctx, tx, lease.identity, "fuel"); err != nil || current != lease.generation {
		if err != nil {
			return err
		}
		return ErrConflict
	}
	scope, err := fuelModuleScope(ctx, tx, lease.identity)
	if err != nil || !scope.CanWrite || scope.Generation != lease.generation {
		if err != nil {
			return err
		}
		return ErrForbidden
	}
	attachment, err := fuelModuleCoreAttachment(ctx, tx, lease.identity, lease.source.AttachmentID)
	if err != nil {
		return err
	}
	if String(attachment["sha256"]) != lease.source.SHA256 || String(attachment["resource_id"]) != lease.vehicle {
		return ErrConflict
	}
	var live bool
	err = tx.QueryRow(ctx, `SELECT true FROM fuel.capture_jobs WHERE id=$1 AND state='running' AND lease_token=$2
 AND lease_until>clock_timestamp() AND expires_at>clock_timestamp() FOR UPDATE`, lease.job, lease.token).Scan(&live)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	suggestions, warnings, err := fuelModuleCaptureSuggestions(lease.kind, text, lease.source.AttachmentID, Record{})
	if err != nil {
		return err
	}
	if code != "" {
		suggestions = Record{}
		warnings = []FuelModuleWarning{{Code: code, Message: "Lokale Erkennung nicht verfügbar. Original manuell in der Erfassung prüfen."}}
	}
	proposal, err := json.Marshal(suggestions)
	if err != nil {
		return err
	}
	warningJSON, err := json.Marshal(warnings)
	if err != nil {
		return err
	}
	if len(proposal) > 131072 || len(warningJSON) > 65536 {
		code = "ocr_output_limit"
		text = ""
		proposal = []byte(`{}`)
		warningJSON = []byte(`[{"code":"ocr_output_limit","message":"Der Erkennungsvorschlag ist zu groß. Original manuell prüfen."}]`)
	}
	var transitioned bool
	err = tx.QueryRow(ctx, `SELECT fuel.capture_transition($1,$2,'publish',$3,$4::jsonb,$5::jsonb,$6)`, lease.job, lease.token, text, proposal, warningJSON, code).Scan(&transitioned)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
