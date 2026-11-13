package store

import (
	"context"
	"errors"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// DocumentNotificationSettings does not grant access: it uses the document
// workspace's current session and household authority.
func (s *Store) DocumentNotificationSettings(ctx context.Context, id auth.Identity, deadline string) (Record, error) {
	if !value.ValidUUID(deadline) {
		return nil, ErrNotFound
	}
	q, err := s.documentRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer q.close()
	settings, err := energyObject(q.ctx, q.tx, `SELECT jsonb_build_object(
 'id',x.id,'document_id',d.id,'document_version',d.version,'deadline_version',x.version,
 'version',coalesce(n.version,0),'responsible_member_id',n.responsible_member_id,'enabled',coalesce(n.enabled,false))
 FROM documents.deadlines x JOIN documents.documents d ON d.household_id=x.household_id AND d.id=x.document_id
 LEFT JOIN documents.notification_settings n ON n.household_id=x.household_id AND n.id=x.id
 WHERE x.household_id=$1 AND x.id=$2`, id.HouseholdID, deadline)
	if err != nil {
		return nil, err
	}
	members, err := energyRows(q.ctx, q.tx, 250, `SELECT jsonb_build_object('id',m.id,'name',u.display_name,'username',u.username)
 FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=$1
 AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password ORDER BY u.username,m.id LIMIT 251`, id.HouseholdID)
	if err != nil {
		return nil, err
	}
	return Record{"generation": q.generation, "settings": settings, "members": members}, nil
}

func saveDocumentNotificationSettings(ctx context.Context, tx pgx.Tx, id auth.Identity, in DocumentCommand) (Record, error) {
	var v struct {
		DeadlineID      string `json:"deadline_id"`
		DeadlineVersion int64  `json:"deadline_version"`
		SettingsVersion int64  `json:"settings_version"`
		Responsible     string `json:"responsible_member_id"`
		Enabled         *bool  `json:"enabled"`
	}
	if err := documentDecode(in.Data, &v); err != nil {
		return nil, err
	}
	if !value.ValidUUID(v.DeadlineID) || v.DeadlineVersion < 1 || v.SettingsVersion < 0 ||
		v.Enabled == nil || v.Responsible != "" && !value.ValidUUID(v.Responsible) {
		return nil, Invalid{"Frist, Zuständigkeit und Benachrichtigungsversion prüfen"}
	}
	if *v.Enabled && v.Responsible == "" {
		return nil, Invalid{"Zuständige Person für Fristhinweise auswählen"}
	}
	var current bool
	err := tx.QueryRow(ctx, `SELECT true FROM documents.deadlines x JOIN documents.documents d
 ON d.household_id=x.household_id AND d.id=x.document_id WHERE x.household_id=$1 AND x.document_id=$2
 AND x.id=$3 AND x.version=$4 AND x.cancelled_at IS NULL AND d.archived_at IS NULL FOR SHARE OF x`,
		id.HouseholdID, in.ID, v.DeadlineID, v.DeadlineVersion).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrConflict
	}
	if err != nil {
		return nil, err
	}
	var result Record
	if v.SettingsVersion == 0 {
		result, err = energyObject(ctx, tx, `INSERT INTO documents.notification_settings AS n
 (id,household_id,responsible_member_id,enabled,created_by) VALUES($1,$2,nullif($3,'')::uuid,$4,$5)
 RETURNING jsonb_build_object('id',n.id,'version',n.version)`, v.DeadlineID, id.HouseholdID, v.Responsible, *v.Enabled, id.UserID)
	} else {
		result, err = energyObject(ctx, tx, `UPDATE documents.notification_settings AS n
 SET responsible_member_id=nullif($4,'')::uuid,enabled=$5,version=version+1,updated_at=clock_timestamp()
 WHERE household_id=$1 AND id=$2 AND version=$3 RETURNING jsonb_build_object('id',n.id,'version',n.version)`,
			id.HouseholdID, v.DeadlineID, v.SettingsVersion, v.Responsible, *v.Enabled)
	}
	if errors.Is(err, ErrNotFound) {
		err = ErrConflict
	}
	return Record{"id": in.ID, "notification_settings": result}, err
}
