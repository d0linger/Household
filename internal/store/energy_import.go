package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"household/internal/auth"
	"household/internal/energy"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func energyImportCommand(ctx context.Context, tx pgx.Tx, id auth.Identity, key, rid string, in EnergyCommand, data Record) (Record, error) {
	if in.Action == "import.apply" {
		return energyApplyImport(ctx, tx, id, rid, in)
	}
	if in.ID != "" || in.Version != 0 {
		return nil, Invalid{"Eine neue geprüfte CSV-Vorschau anlegen"}
	}
	var kind, unit, zone string
	var revision int64
	var registerVersion, mappingVersion int64
	var mappingID string
	err := tx.QueryRow(ctx, `SELECT r.measurement_kind,r.unit_code,r.timezone,r.data_revision,r.version,d.id::text,d.version FROM meters.registers r
 JOIN meters.device_registers d ON d.register_id=r.id WHERE r.household_id=$1 AND r.id=$2 AND d.device_id=$3
 AND r.archived_at IS NULL AND r.input_mode='manual'`, id.HouseholdID, data["register_id"], data["device_id"]).
		Scan(&kind, &unit, &zone, &revision, &registerVersion, &mappingID, &mappingVersion)
	if err != nil {
		return nil, energyError(err)
	}
	if data["unit_code"] != unit || data["timezone"] != zone {
		return nil, Invalid{"Importeinheit und Zeitzone müssen zum ausdrücklich gewählten Register passen"}
	}
	rows, problems, err := energy.ParseCSV(String(data["csv"]), zone, unit, kind, String(data["delimiter"]))
	if err != nil {
		return nil, Invalid{"CSV-Profil prüfen: measured_at,value,kind,unit_code und bei Intervallmengen interval_started_at; höchstens1000Zeilen/512KiB"}
	}
	if len(problems) != 0 {
		return Record{"state": "invalid", "rows": len(rows), "errors": problems, "applied": 0}, nil
	}
	checks, err := energyPreviewExisting(ctx, tx, id, String(data["register_id"]), String(data["device_id"]), rows)
	if err != nil {
		return nil, err
	}
	duplicates := 0
	for _, check := range checks {
		if check["duplicate"] == true {
			duplicates++
		}
		if check["invalid"] == true && len(problems) < 50 {
			problems = append(problems, energy.CSVProblem{Row: int(Int(check["row_number"])) + 1, Message: "Messzeitpunkt liegt außerhalb des Geräts oder widerspricht einem vorhandenen Messwert/Intervall."})
		}
	}
	if len(problems) != 0 {
		return Record{"state": "invalid", "rows": len(rows), "errors": problems, "applied": 0, "duplicates": duplicates}, nil
	}
	digest := sha256.Sum256([]byte(String(data["csv"])))
	_, err = tx.Exec(ctx, `INSERT INTO meters.import_batches(id,household_id,register_id,content_sha256,timezone,unit_code,row_count,
	 module_generation,source_revision,register_version,device_mapping_id,mapping_version,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		rid, id.HouseholdID, data["register_id"], hex.EncodeToString(digest[:]), zone, unit, len(rows), in.Generation, revision,
		registerVersion, mappingID, mappingVersion, id.UserID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		_, err = tx.Exec(ctx, `INSERT INTO meters.import_rows(household_id,batch_id,row_number,device_id,measured_at,value,kind,interval_started_at,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id.HouseholdID, rid, row.Number, data["device_id"], row.At, row.Value, row.Kind, row.StartedAt, id.UserID)
		if err != nil {
			return nil, err
		}
	}
	preview := rows
	if len(preview) > 50 {
		preview = preview[:50]
	}
	return Record{"id": rid, "version": 1, "state": "preview", "rows": len(rows), "preview_rows": preview, "duplicates": duplicates, "new_rows": len(rows) - duplicates,
		"preview_limited": len(rows) > 50, "errors": []energy.CSVProblem{}, "applied": 0}, nil
}

func energyPreviewExisting(ctx context.Context, tx pgx.Tx, id auth.Identity, register, device string, rows []energy.CSVRow) ([]Record, error) {
	at, amount, kind, start, number := []string{}, []string{}, []string{}, []string{}, []string{}
	for _, row := range rows {
		at = append(at, row.At.Format(time.RFC3339Nano))
		amount = append(amount, row.Value)
		kind = append(kind, row.Kind)
		number = append(number, fmt.Sprint(row.Number))
		text := ""
		if row.StartedAt != nil {
			text = row.StartedAt.Format(time.RFC3339Nano)
		}
		start = append(start, text)
	}
	return energyRows(ctx, tx, 1000, `WITH requested AS(SELECT a::timestamptz at,v::numeric value,k kind,nullif(s,'')::timestamptz start,n::integer row_number
 FROM unnest($4::text[],$5::text[],$6::text[],$7::text[],$8::text[]) x(a,v,k,s,n))
 SELECT jsonb_build_object('row_number',q.row_number,'duplicate',existing.id IS NOT NULL AND existing.value=q.value AND existing.kind=q.kind
 AND existing.interval_started_at IS NOT DISTINCT FROM q.start,'invalid',q.at>clock_timestamp() OR q.at<d.started_at
 OR(d.ended_at IS NOT NULL AND q.at>d.ended_at) OR q.start<d.started_at
 OR existing.id IS NOT NULL AND(existing.value<>q.value OR existing.kind<>q.kind OR existing.interval_started_at IS DISTINCT FROM q.start)
 OR q.start IS NOT NULL AND EXISTS(SELECT 1 FROM meters.readings other WHERE other.register_id=$2 AND other.id IS DISTINCT FROM existing.id
  AND tstzrange(other.interval_started_at,other.measured_at,'[)') && tstzrange(q.start,q.at,'[)')))
 FROM requested q JOIN meters.device_registers d ON d.household_id=$1 AND d.register_id=$2 AND d.device_id=$3
 LEFT JOIN meters.readings existing ON existing.register_id=$2 AND existing.device_id=$3 AND existing.measured_at=q.at
 ORDER BY q.row_number LIMIT 1001`, id.HouseholdID, register, device, at, amount, kind, start, number)
}

func energyApplyImport(ctx context.Context, tx pgx.Tx, id auth.Identity, rid string, in EnergyCommand) (Record, error) {
	var register, state string
	var sourceRevision, currentRevision, version int64
	err := tx.QueryRow(ctx, `SELECT b.register_id::text,b.state,b.source_revision,r.data_revision,b.version
 FROM meters.import_batches b JOIN meters.registers r ON r.id=b.register_id WHERE b.household_id=$1 AND b.id=$2
 AND b.module_generation=$3 AND b.register_version=r.version AND EXISTS(SELECT 1 FROM meters.device_registers d
 WHERE d.household_id=b.household_id AND d.id=b.device_mapping_id AND d.version=b.mapping_version)
 FOR UPDATE OF b`, id.HouseholdID, rid, in.Generation).
		Scan(&register, &state, &sourceRevision, &currentRevision, &version)
	if err != nil {
		return nil, energyError(err)
	}
	if version != in.Version || state != "preview" || sourceRevision != currentRevision {
		return nil, ErrConflict
	}
	rows, err := energyRows(ctx, tx, 1000, `SELECT to_jsonb(x)||jsonb_build_object('value',x.value::text)
 FROM meters.import_rows x WHERE household_id=$1 AND batch_id=$2 ORDER BY measured_at,id LIMIT 1001`, id.HouseholdID, rid)
	if err != nil {
		return nil, err
	}
	inserted, duplicate := 0, 0
	for _, row := range rows {
		var existing *string
		var exact bool
		err = tx.QueryRow(ctx, `SELECT (SELECT id::text FROM meters.readings WHERE household_id=$1 AND register_id=$2 AND device_id=$3 AND measured_at=$4::timestamptz),
 EXISTS(SELECT 1 FROM meters.readings WHERE household_id=$1 AND register_id=$2 AND device_id=$3 AND measured_at=$4::timestamptz
 AND value=$5::numeric AND kind=$6 AND interval_started_at IS NOT DISTINCT FROM $7::timestamptz)`,
			id.HouseholdID, register, row["device_id"], row["measured_at"], row["value"], row["kind"], row["interval_started_at"]).Scan(&existing, &exact)
		if err != nil {
			return nil, err
		}
		reading := ""
		if existing != nil {
			if !exact {
				return nil, Invalid{"Import widerspricht einem vorhandenen Messwert; aktuelle Vorschau erstellen"}
			}
			reading = *existing
			duplicate++
		} else {
			reading = value.UUID()
			source, err := json.Marshal([]any{register, row["device_id"], row["measured_at"], row["value"], row["kind"], row["interval_started_at"]})
			if err != nil {
				return nil, err
			}
			digest := sha256.Sum256(source)
			_, err = energySaveReading(ctx, tx, id, reading, "csv/"+hex.EncodeToString(digest[:]), 0, Record{
				"register_id": register, "device_id": row["device_id"], "measured_at": row["measured_at"], "value": row["value"],
				"kind": row["kind"], "interval_started_at": row["interval_started_at"], "note": "Bestätigter CSV-Import"})
			if err != nil {
				return nil, err
			}
			inserted++
		}
		if _, err = tx.Exec(ctx, `UPDATE meters.import_rows SET reading_id=$3 WHERE household_id=$1 AND id=$2 AND reading_id IS NULL`,
			id.HouseholdID, row["id"], reading); err != nil {
			return nil, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE meters.import_batches SET state='applied',version=version+1,updated_at=clock_timestamp()
 WHERE household_id=$1 AND id=$2 AND version=$3`, id.HouseholdID, rid, in.Version)
	return Record{"id": rid, "version": in.Version + 1, "state": "applied", "inserted": inserted, "duplicates": duplicate}, err
}
