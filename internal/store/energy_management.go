package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"household/internal/auth"
	"household/internal/meterfeed"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func (s *Store) energySaveManagement(ctx context.Context, tx pgx.Tx, id auth.Identity, rid string, in EnergyCommand, data Record) (Record, error) {
	var result Record
	var err error
	switch in.Action {
	case "meter.save":
		if in.Version == 0 {
			result, err = energyObject(ctx, tx, `INSERT INTO meters.meters AS x(id,household_id,name,unit_code,location_note,created_by)
 VALUES($1,$2,$3,$4,$5,$6) RETURNING jsonb_build_object('id',x.id,'version',x.version)`,
				rid, id.HouseholdID, data["name"], data["unit_code"], String(data["location_note"]), id.UserID)
		} else {
			if err = s.validateDomain(ctx, tx, id, "meters", "meters", rid, data, data); err != nil {
				return nil, err
			}
			result, err = energyObject(ctx, tx, `UPDATE meters.meters AS x SET name=$4,unit_code=$5,location_note=$6,
 archived_at=CASE WHEN $7 THEN coalesce(archived_at,clock_timestamp()) ELSE NULL END,version=version+1,updated_at=clock_timestamp()
 WHERE household_id=$1 AND id=$2 AND version=$3 RETURNING jsonb_build_object('id',x.id,'version',x.version)`,
				id.HouseholdID, rid, in.Version, data["name"], data["unit_code"], String(data["location_note"]), data["archived"] == true)
		}
	case "register.save":
		if _, supplied := data["retention_days"]; supplied && id.Role != "admin" {
			return nil, ErrForbidden
		}
		var gap, minimum int
		gap, err = energyInteger(data, "max_gap_seconds", 1, 31622400)
		if err != nil {
			return nil, err
		}
		minimum, err = energyInteger(data, "min_history", 7, 100)
		if err != nil {
			return nil, err
		}
		var retention any
		if String(data["retention_days"]) != "" {
			retention, err = energyInteger(data, "retention_days", 7, 3650)
			if err != nil {
				return nil, err
			}
		}
		if in.Version == 0 {
			result, err = energyObject(ctx, tx, `INSERT INTO meters.registers AS x(id,household_id,meter_id,name,measurement_kind,unit_code,
 direction,timezone,input_mode,max_gap_seconds,daily_warning,base_load_warning,min_history,retention_days,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING jsonb_build_object('id',x.id,'version',x.version)`,
				rid, id.HouseholdID, data["meter_id"], data["name"], data["measurement_kind"], data["unit_code"],
				data["direction"], data["timezone"], data["input_mode"], gap, energyNullable(data, "daily_warning"),
				energyNullable(data, "base_load_warning"), minimum, retention, id.UserID)
		} else {
			result, err = energyObject(ctx, tx, `UPDATE meters.registers AS x SET name=$4,measurement_kind=$5,unit_code=$6,direction=$7,
 timezone=$8,input_mode=$9,max_gap_seconds=$10,daily_warning=$11,base_load_warning=$12,min_history=$13,retention_days=CASE WHEN $17 THEN $14::integer ELSE retention_days END,
 archived_at=CASE WHEN $15 THEN coalesce(archived_at,clock_timestamp()) ELSE NULL END,updated_at=clock_timestamp(),version=version+1
 WHERE household_id=$1 AND id=$2 AND version=$3 AND meter_id=$16 RETURNING jsonb_build_object('id',x.id,'version',x.version)`,
				id.HouseholdID, rid, in.Version, data["name"], data["measurement_kind"], data["unit_code"], data["direction"],
				data["timezone"], data["input_mode"], gap, energyNullable(data, "daily_warning"), energyNullable(data, "base_load_warning"),
				minimum, retention, data["archived"] == true, data["meter_id"], id.Role == "admin")
		}
	case "device.save":
		if in.Version == 0 {
			return energyCreateDevice(ctx, tx, id, rid, data)
		}
		result, err = energyObject(ctx, tx, `UPDATE meters.devices AS x SET serial_number=$4,version=version+1,updated_at=clock_timestamp()
 WHERE household_id=$1 AND id=$2 AND version=$3 AND meter_id=$5 AND EXISTS(SELECT 1 FROM meters.device_registers d
 WHERE d.household_id=$1 AND d.device_id=x.id AND d.register_id=$6 AND d.started_at=$7::timestamptz
 AND d.initial_value=$8::numeric AND d.rollover_value IS NOT DISTINCT FROM $9::numeric)
 RETURNING jsonb_build_object('id',x.id,'version',x.version)`, id.HouseholdID, rid, in.Version,
			data["serial_number"], data["meter_id"], data["register_id"], data["installed_at"], data["initial_value"], energyNullable(data, "rollover_value"))
	case "plan.save":
		var interval int
		interval, err = energyInteger(data, "interval_days", 1, 366)
		if err != nil {
			return nil, err
		}
		if _, exists := data["actual_only"]; !exists {
			return nil, Invalid{"Messung oder Schätzung als Erfüllung ausdrücklich festlegen"}
		}
		if _, exists := data["active"]; !exists {
			return nil, Invalid{"Ableseplan ausdrücklich aktivieren oder deaktivieren"}
		}
		if in.Version == 0 {
			result, err = energyObject(ctx, tx, `INSERT INTO meters.reading_plans AS x(id,household_id,register_id,anchor_on,interval_days,actual_only,active,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING jsonb_build_object('id',x.id,'version',x.version)`,
				rid, id.HouseholdID, data["register_id"], data["anchor_on"], interval, data["actual_only"], data["active"], id.UserID)
		} else {
			result, err = energyObject(ctx, tx, `UPDATE meters.reading_plans AS x SET anchor_on=$4,interval_days=$5,actual_only=$6,active=$7,
 version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2 AND version=$3 AND register_id=$8
 RETURNING jsonb_build_object('id',x.id,'version',x.version)`,
				id.HouseholdID, rid, in.Version, data["anchor_on"], interval, data["actual_only"], data["active"], data["register_id"])
		}
	case "source.save":
		if data["adapter"] == "mqtt" && !meterfeed.ValidTopic(String(data["source_name"])) {
			return nil, Invalid{"MQTT benötigt ein exaktes UTF-8-Topic ohne Platzhalter oder Steuerzeichen"}
		}
		if data["enabled"] == true {
			var enabled int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM meters.integration_sources WHERE household_id=$1 AND enabled AND id<>$2`,
				id.HouseholdID, rid).Scan(&enabled); err != nil {
				return nil, err
			}
			if enabled >= 16 {
				return nil, Invalid{"Höchstens16gleichzeitig aktive Adapterquellen sind unterstützt"}
			}
		}
		if _, exists := data["enabled"]; !exists || !strings.HasPrefix(String(data["source_name"]), "sensor.") && data["adapter"] == "home_assistant" {
			return nil, Invalid{"Adapter und ausdrücklich freigegebene Messquelle prüfen"}
		}
		var mapped bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM meters.registers r JOIN meters.device_registers d ON d.register_id=r.id
 WHERE r.household_id=$1 AND r.id=$2 AND d.device_id=$3 AND r.measurement_kind=$4 AND r.unit_code=$5 AND r.input_mode='telemetry')`,
			id.HouseholdID, data["register_id"], data["device_id"], data["measurement_kind"], data["unit_code"]).Scan(&mapped)
		if err != nil {
			return nil, err
		}
		if !mapped {
			return nil, Invalid{"Adapter benötigt ein passend zugeordnetes Telemetrieregister und Gerät"}
		}
		if in.Version == 0 {
			result, err = energyObject(ctx, tx, `INSERT INTO meters.integration_sources AS x(id,household_id,register_id,device_id,adapter,source_name,
 measurement_kind,unit_code,enabled,module_generation,created_by,authorized_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$11)
 RETURNING jsonb_build_object('id',x.id,'version',x.version,'status_code',x.status_code)`,
				rid, id.HouseholdID, data["register_id"], data["device_id"], data["adapter"], data["source_name"],
				data["measurement_kind"], data["unit_code"], data["enabled"], in.Generation, id.UserID)
		} else {
			result, err = energyObject(ctx, tx, `UPDATE meters.integration_sources AS x SET enabled=$4,
 status_code=CASE WHEN $4 THEN 'not_run' ELSE 'disabled' END,next_attempt_at=NULL,consecutive_failures=0,
 module_generation=$5,version=version+1,updated_at=clock_timestamp(),authorized_by=$12,
 register_id=$6,device_id=$7,measurement_kind=$10,unit_code=$11
 WHERE household_id=$1 AND id=$2 AND version=$3 AND adapter=$8 AND source_name=$9
 RETURNING jsonb_build_object('id',x.id,'version',x.version,'status_code',x.status_code)`,
				id.HouseholdID, rid, in.Version, data["enabled"], in.Generation, data["register_id"], data["device_id"],
				data["adapter"], data["source_name"], data["measurement_kind"], data["unit_code"], id.UserID)
		}
	}
	if errors.Is(err, ErrNotFound) {
		return nil, ErrConflict
	}
	return result, err
}

func energyCreateDevice(ctx context.Context, tx pgx.Tx, id auth.Identity, rid string, data Record) (Record, error) {
	var zone, register string
	err := tx.QueryRow(ctx, `SELECT timezone,id::text FROM meters.registers WHERE household_id=$1 AND id=$2 AND meter_id=$3 AND archived_at IS NULL`,
		id.HouseholdID, data["register_id"], data["meter_id"]).Scan(&zone, &register)
	if err != nil {
		return nil, energyError(err)
	}
	instant, err := time.Parse(time.RFC3339Nano, String(data["installed_at"]))
	if err != nil {
		return nil, Invalid{"Gerätestart mit UTC-Offset angeben"}
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, err
	}
	out, err := energyObject(ctx, tx, `INSERT INTO meters.devices AS x(id,household_id,meter_id,serial_number,installed_on,initial_reading,rollover_value,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING jsonb_build_object('id',x.id,'version',x.version)`,
		rid, id.HouseholdID, data["meter_id"], data["serial_number"], instant.In(location).Format(time.DateOnly),
		data["initial_value"], energyNullable(data, "rollover_value"), id.UserID)
	if err != nil {
		return nil, err
	}
	// The generic compatibility trigger creates one default mapping. A reviewed
	// workspace command replaces that unused provisional mapping atomically.
	if _, err = tx.Exec(ctx, `DELETE FROM meters.device_registers WHERE household_id=$1 AND device_id=$2`, id.HouseholdID, rid); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO meters.device_registers(household_id,register_id,device_id,started_at,initial_value,rollover_value,created_by)
 VALUES($1,$2,$3,$4,$5,$6,$7)`, id.HouseholdID, register, rid, instant, data["initial_value"], energyNullable(data, "rollover_value"), id.UserID)
	return out, err
}

func energyReplaceDevice(ctx context.Context, tx pgx.Tx, id auth.Identity, key string, data Record) (Record, error) {
	oldVersion, err := energyInteger(data, "old_mapping_version", 1, 2147483647)
	if err != nil {
		return nil, err
	}
	var oldDevice, meter, mapping, mode string
	err = tx.QueryRow(ctx, `SELECT d.device_id::text,r.meter_id::text,d.id::text,r.input_mode FROM meters.device_registers d JOIN meters.registers r ON r.id=d.register_id
 WHERE d.household_id=$1 AND d.id=$2 AND d.register_id=$3 AND d.version=$4 AND d.ended_at IS NULL FOR UPDATE OF d`,
		id.HouseholdID, data["old_mapping_id"], data["register_id"], oldVersion).Scan(&oldDevice, &meter, &mapping, &mode)
	if err != nil {
		return nil, energyError(err)
	}
	if String(data["old_final"]) == "" && data["gap_acknowledged"] != true {
		return nil, Invalid{"Alten Endstand angeben oder die unbekannte Wechselgrenze ausdrücklich bestätigen"}
	}
	if String(data["old_final"]) != "" && mode == "manual" {
		var existing *string
		err = tx.QueryRow(ctx, `SELECT (SELECT value::text FROM meters.readings WHERE household_id=$1 AND register_id=$2 AND device_id=$3 AND measured_at=$4::timestamptz)`,
			id.HouseholdID, data["register_id"], oldDevice, data["changed_at"]).Scan(&existing)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			_, err = energySaveReading(ctx, tx, id, value.UUID(), key+"/final", 0, Record{"register_id": data["register_id"],
				"device_id": oldDevice, "measured_at": data["changed_at"], "value": data["old_final"], "kind": "actual", "note": "Bestätigter Geräte-Endstand"})
		} else {
			left, leftErr := value.Decimal(*existing)
			right, rightErr := value.Decimal(String(data["old_final"]))
			if leftErr != nil || rightErr != nil || left.Cmp(right) != 0 {
				return nil, Invalid{"Vorhandener Stand an der Wechselgrenze stimmt nicht mit dem bestätigten Endstand überein"}
			}
		}
		if err != nil {
			return nil, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE meters.device_registers SET ended_at=$4,final_value=$5,legacy_boundary=false,
 version=version+1,updated_at=clock_timestamp() WHERE household_id=$1 AND id=$2 AND version=$3`,
		id.HouseholdID, mapping, oldVersion, data["changed_at"], energyNullable(data, "old_final"))
	if err != nil {
		return nil, err
	}
	newID := value.UUID()
	_, err = energyCreateDevice(ctx, tx, id, newID, Record{"meter_id": meter, "register_id": data["register_id"],
		"serial_number": data["serial_number"], "installed_at": data["changed_at"], "initial_value": data["new_initial"], "rollover_value": data["rollover_value"]})
	if err != nil {
		return nil, err
	}
	return energyObject(ctx, tx, `INSERT INTO meters.device_changes AS x(household_id,register_id,old_mapping_id,new_mapping_id,changed_at,
 old_final,new_initial,gap_acknowledged,reason,created_by) SELECT $1,$2,$3,d.id,$4,$5,$6,$7,$8,$9
 FROM meters.device_registers d WHERE d.household_id=$1 AND d.register_id=$2 AND d.device_id=$10
 RETURNING jsonb_build_object('id',x.id,'new_device_id',$10::uuid,'gap_acknowledged',x.gap_acknowledged)`,
		id.HouseholdID, data["register_id"], mapping, data["changed_at"], energyNullable(data, "old_final"), data["new_initial"],
		data["gap_acknowledged"] == true, data["reason"], id.UserID, newID)
}
