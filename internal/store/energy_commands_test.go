package store

import (
	"encoding/json"
	"testing"
)

func energyRaw(data Record) map[string]json.RawMessage {
	result := map[string]json.RawMessage{}
	for field, value := range data {
		raw, err := json.Marshal(value)
		if err != nil {
			panic(err)
		}
		result[field] = raw
	}
	return result
}

func TestEnergyCommandsRequireExactTypedRegisterVersion(t *testing.T) {
	base := Record{"register_id": "00000000-0000-4000-8000-000000000001", "register_version": "1", "device_id": "00000000-0000-4000-8000-000000000002", "measured_at": "2026-01-01T00:00:00Z", "value": "1.000001", "kind": "actual"}
	for _, tc := range []struct {
		name, field   string
		value         any
		remove, valid bool
	}{
		{"valid", "value", "1.000001", false, true}, {"missing register version", "register_version", nil, true, false},
		{"stale-shaped version", "register_version", "01", false, false}, {"numeric lossy value", "value", 1.1, false, false},
		{"negative", "value", "-1", false, false}, {"unknown field", "sql", "SELECT", false, false},
		{"wrong measurement quality", "kind", "calculated", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := Record{}
			for k, v := range base {
				data[k] = v
			}
			if tc.remove {
				delete(data, tc.field)
			} else {
				data[tc.field] = tc.value
			}
			_, err := energyNormalize(EnergyCommand{Action: "reading.save", Generation: 1, Data: energyRaw(data)})
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
}
