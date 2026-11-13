package store

import (
	"context"
	"errors"
	"strconv"
	"time"

	"household/internal/civiltime"
	"household/internal/notification"

	"github.com/jackc/pgx/v5"
)

const DocumentNotificationScanJobKind = "document_notification_scan_v1"

// Sources are closed internal kinds. These records contain no document title,
// extracted text, filename, mailbox or arbitrary destination URL.
func documentNotificationIntent(ctx context.Context, tx pgx.Tx, household, kind, resource string, now time.Time) (*notification.Intent, error) {
	switch kind {
	case "deadline":
		return documentDeadlineIntent(ctx, tx, household, resource, now)
	case "processing", "import":
		return documentFailureIntent(ctx, tx, household, kind, resource, now)
	default:
		return nil, ErrConflict
	}
}

func documentDeadlineIntent(ctx context.Context, tx pgx.Tx, household, resource string, now time.Time) (*notification.Intent, error) {
	var user, due, zone string
	var generation, revision, deadlineRevision int64
	var offset int
	var activated, assigned, configured, confirmed, documentChanged time.Time
	err := tx.QueryRow(ctx, `SELECT m.user_id::text,x.due_on::text,h.timezone,ms.data_generation,n.version,x.version,x.remind_days,
 ms.changed_at,n.responsible_since,n.updated_at,x.notification_updated_at,d.notification_active_since
 FROM documents.notification_settings n JOIN documents.deadlines x ON x.household_id=n.household_id AND x.id=n.id
 JOIN documents.documents d ON d.household_id=x.household_id AND d.id=x.document_id
 JOIN core.households h ON h.id=n.household_id
 JOIN core.module_states ms ON ms.household_id=n.household_id AND ms.module_id='documents'
 JOIN core.memberships m ON m.household_id=n.household_id AND m.id=n.responsible_member_id
 JOIN core.users u ON u.id=m.user_id WHERE n.household_id=$1 AND n.id=$2 AND n.enabled
 AND x.cancelled_at IS NULL AND x.due_on>='2000-01-01'::date AND x.due_on<'10000-01-01'::date
 AND d.archived_at IS NULL AND ms.state='enabled'
 AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password`, household, resource).
		Scan(&user, &due, &zone, &generation, &revision, &deadlineRevision, &offset,
			&activated, &assigned, &configured, &confirmed, &documentChanged)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	at, err := documentNotificationStage(due, zone, offset)
	if err != nil {
		return nil, err
	}
	if at.After(now) || !at.Add(24*time.Hour).After(now) || activated.After(at) || assigned.After(at) ||
		configured.After(at) || confirmed.After(at) || documentChanged.After(at) {
		return nil, nil
	}
	return &notification.Intent{Schema: notification.Schema, HouseholdID: household, UserID: user,
		Module: "documents", Generation: generation, ResourceType: "document_deadline", ResourceID: resource,
		Revision: revision, Category: "documents", Occurrence: "deadline/" + strconv.FormatInt(deadlineRevision, 10) + "/" + due,
		EventAt: at, NotBefore: at, ExpiresAt: at.Add(24 * time.Hour)}, nil
}

// Documents already supports a366-day lead time. Keep that domain limit while
// resolving the original civil date, including timezone gaps/folds.
func documentNotificationStage(due, zone string, offset int) (time.Time, error) {
	date, err := time.Parse(time.DateOnly, due)
	if err != nil || date.Format(time.DateOnly) != due || date.Year() < 2000 || date.Year() > 9999 || offset < 0 || offset > 366 {
		return time.Time{}, ErrConflict
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, err
	}
	at, _, err := civiltime.Resolve(date.AddDate(0, 0, -offset), "09:00", location)
	return at, err
}

func documentFailureIntent(ctx context.Context, tx pgx.Tx, household, kind, resource string, now time.Time) (*notification.Intent, error) {
	var user string
	var generation, epoch int64
	var occurred, activated time.Time
	var err error
	switch kind {
	case "processing":
		err = tx.QueryRow(ctx, `SELECT e.recipient_user_id::text,e.source_generation,e.epoch,e.occurred_at,ms.changed_at
 FROM documents.notification_processing_events e JOIN documents.processing p ON p.household_id=e.household_id AND p.id=e.id
 JOIN documents.documents d ON d.household_id=p.household_id AND d.id=p.document_id
 JOIN core.module_states ms ON ms.household_id=e.household_id AND ms.module_id='documents'
 JOIN core.memberships m ON m.household_id=e.household_id AND m.user_id=e.recipient_user_id
 JOIN core.users u ON u.id=m.user_id WHERE e.household_id=$1 AND e.id=$2 AND e.active
 AND p.state='failed' AND p.status_code IN('local_processing_failed','processing_interrupted','worker_failed')
 AND p.requested_by=e.recipient_user_id AND p.module_generation=e.source_generation
 AND d.archived_at IS NULL AND d.notification_active_since<=e.occurred_at AND ms.state='enabled' AND ms.data_generation=e.source_generation
 AND m.role IN('admin','editor') AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password`, household, resource).
			Scan(&user, &generation, &epoch, &occurred, &activated)
	case "import":
		err = tx.QueryRow(ctx, `SELECT e.recipient_user_id::text,e.source_generation,e.epoch,e.occurred_at,ms.changed_at
 FROM documents.notification_import_events e JOIN documents.integration_sources src ON src.household_id=e.household_id AND src.id=e.id
 JOIN core.module_states ms ON ms.household_id=e.household_id AND ms.module_id='documents'
 JOIN core.memberships m ON m.household_id=e.household_id AND m.user_id=e.recipient_user_id
 JOIN core.users u ON u.id=m.user_id WHERE e.household_id=$1 AND e.id=$2 AND e.active
 AND src.enabled AND src.status_code='input_failed' AND src.failures>0
 AND src.approved_by=e.recipient_user_id AND src.version=e.source_version AND src.module_generation=e.source_generation
 AND ms.state='enabled' AND ms.data_generation=e.source_generation
 AND m.role='admin' AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password`, household, resource).
			Scan(&user, &generation, &epoch, &occurred, &activated)
	default:
		return nil, ErrConflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if occurred.After(now) || !occurred.Add(24*time.Hour).After(now) || activated.After(occurred) {
		return nil, nil
	}
	return &notification.Intent{Schema: notification.Schema, HouseholdID: household, UserID: user,
		Module: "documents", Generation: generation, ResourceType: "document_" + kind, ResourceID: resource,
		Revision: epoch, Category: "documents", Occurrence: "failed/" + strconv.FormatInt(epoch, 10), EventAt: occurred, NotBefore: occurred,
		ExpiresAt: occurred.Add(24 * time.Hour)}, nil
}

func (s *Store) documentNotificationCutoff(ctx context.Context, tx pgx.Tx, i notification.Intent) (time.Time, error) {
	if i.Validate() != nil || i.Category != "documents" || i.Module != "documents" {
		return time.Time{}, nil
	}
	kinds := map[string]string{"document_deadline": "deadline", "document_processing": "processing", "document_import": "import"}
	kind, found := kinds[i.ResourceType]
	if !found {
		return time.Time{}, nil
	}
	var now time.Time
	if err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return time.Time{}, err
	}
	candidate, err := documentNotificationIntent(ctx, tx, i.HouseholdID, kind, i.ResourceID, now)
	if err != nil || candidate == nil {
		return time.Time{}, err
	}
	if candidate.UserID != i.UserID || candidate.Generation != i.Generation || candidate.Revision != i.Revision ||
		candidate.Occurrence != i.Occurrence || !candidate.EventAt.Equal(i.EventAt) ||
		!candidate.NotBefore.Equal(i.NotBefore) || !candidate.ExpiresAt.Equal(i.ExpiresAt) {
		return time.Time{}, nil
	}
	return candidate.EventAt, nil
}
