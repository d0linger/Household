package store

import (
	"context"
	"errors"

	"household/internal/auth"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

// MeterNotificationSettings uses the same current-session/module authority as
// the measurement workspace. Responsibility never grants access of its own.
func (s *Store) MeterNotificationSettings(ctx context.Context, id auth.Identity, register string) (Record, error) {
	if !value.ValidUUID(register) {
		return nil, Invalid{"Register erneut auswählen"}
	}
	scope, err := s.energyRead(ctx, id)
	if err != nil {
		return nil, err
	}
	defer scope.close()
	settings, err := energyObject(scope.ctx, scope.tx, `SELECT jsonb_build_object(
 'id',r.id,'version',coalesce(n.version,0),'register_version',r.version,
 'responsible_member_id',n.responsible_member_id,'reading_enabled',coalesce(n.reading_enabled,false),
 'anomaly_enabled',coalesce(n.anomaly_enabled,false),'stale_enabled',coalesce(n.stale_enabled,false),
 'stale_after_seconds',coalesce(n.stale_after_seconds,86400)) FROM meters.registers r
 LEFT JOIN meters.notification_settings n ON n.household_id=r.household_id AND n.id=r.id
 WHERE r.household_id=$1 AND r.id=$2`, id.HouseholdID, register)
	if err != nil {
		return nil, err
	}
	members, err := energyRows(scope.ctx, scope.tx, 250, `SELECT jsonb_build_object('id',m.id,'name',u.display_name,'username',u.username)
 FROM core.memberships m JOIN core.users u ON u.id=m.user_id WHERE m.household_id=$1
 AND m.inactive_at IS NULL AND u.disabled_at IS NULL AND NOT u.must_change_password ORDER BY u.username,m.id LIMIT 251`, id.HouseholdID)
	if err != nil {
		return nil, err
	}
	return Record{"generation": scope.generation, "settings": settings, "members": members}, nil
}

func saveMeterNotificationSettings(ctx context.Context, tx pgx.Tx, id auth.Identity, in EnergyCommand, data Record) (Record, error) {
	for _, field := range []string{"reading_enabled", "anomaly_enabled", "stale_enabled"} {
		if _, present := data[field]; !present {
			return nil, Invalid{"Zählerschalter ausdrücklich wählen"}
		}
	}
	seconds, err := energyInteger(data, "stale_after_seconds", 300, 31622400)
	if err != nil {
		return nil, err
	}
	register := String(data["register_id"])
	member := String(data["responsible_member_id"])
	if member == "" && (data["reading_enabled"] == true || data["anomaly_enabled"] == true || data["stale_enabled"] == true) {
		return nil, Invalid{"Zuständige Person für Benachrichtigungen auswählen"}
	}
	if in.ID != "" && in.ID != register {
		return nil, ErrConflict
	}
	var result Record
	if in.Version == 0 {
		result, err = energyObject(ctx, tx, `INSERT INTO meters.notification_settings AS n
 (id,household_id,responsible_member_id,reading_enabled,anomaly_enabled,stale_enabled,stale_after_seconds,created_by)
 VALUES($1,$2,nullif($3,'')::uuid,$4,$5,$6,$7,$8) RETURNING jsonb_build_object('id',n.id,'version',n.version)`,
			register, id.HouseholdID, member, data["reading_enabled"], data["anomaly_enabled"], data["stale_enabled"], seconds, id.UserID)
	} else {
		result, err = energyObject(ctx, tx, `UPDATE meters.notification_settings AS n SET responsible_member_id=nullif($4,'')::uuid,
 reading_enabled=$5,anomaly_enabled=$6,stale_enabled=$7,stale_after_seconds=$8,
 version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2 AND version=$3
 RETURNING jsonb_build_object('id',n.id,'version',n.version)`, id.HouseholdID, register, in.Version,
			member, data["reading_enabled"], data["anomaly_enabled"], data["stale_enabled"], seconds)
	}
	if errors.Is(err, ErrNotFound) {
		err = ErrConflict
	}
	return result, err
}
