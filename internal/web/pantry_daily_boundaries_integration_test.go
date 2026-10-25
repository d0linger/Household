//go:build integration

package web

import (
	"strings"
	"testing"

	"household/internal/store"
	"household/internal/value"
)

func TestPantryDailyConfigurationBoundaries(t *testing.T) {
	f := newIntegrationFixture(t)
	product, lot := pantryFixture(t, f, "bounded configuration")
	other, _ := pantryFixture(t, f, "another product")
	pid := store.String(product["id"])
	m, _ := f.app.Store.Module("pantry")
	write := func(entity, rid string, data store.Record, version int64) (store.Record, error) {
		e, _ := m.Entity(entity)
		return f.app.Store.Write(f.ctx, f.identity, m, e, rid, value.UUID(), value.UUID(), auditMutation(t, f, "pantry", data, version))
	}
	for _, tc := range []struct {
		name, entity string
		data         store.Record
	}{
		{"wrong dimension", "packs", store.Record{"product_id": pid, "label": "Wrong", "quantity": "1", "unit_code": "l"}},
		{"inexact base quantity", "packs", store.Record{"product_id": pid, "label": "Tiny", "quantity": "0.000001", "unit_code": "g"}},
		{"unsupported barcode", "barcodes", store.Record{"product_id": pid, "code": "two codes"}},
		{"unattributed opening", "lots", store.Record{"product_id": pid, "location_id": lot["location_id"], "received_on": "2026-01-01", "after_open_days": 2, "notes": "Missing source"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := write(tc.entity, "", tc.data, 0); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	pack, err := write("packs", "", store.Record{"product_id": pid, "label": "Exact", "quantity": "500", "unit_code": "g"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	barcode, err := write("barcodes", "", store.Record{"product_id": pid, "pack_id": pack["id"], "code": "  00073AbC  "}, 0)
	if err != nil || barcode["code"] != "00073AbC" {
		t.Fatal("canonical barcode identity", barcode, err)
	}
	if _, err = write("barcodes", "", store.Record{"product_id": other["id"], "code": "00073AbC"}, 0); err == nil || !strings.Contains(err.Error(), "bounded configuration") {
		t.Fatal("duplicate barcode did not name existing product", err)
	}
	if _, err = write("barcodes", "", store.Record{"product_id": other["id"], "pack_id": pack["id"], "code": "00074AbC"}, 0); err == nil {
		t.Fatal("foreign-product pack accepted")
	}
	if _, err = write("packs", store.String(pack["id"]), store.Record{"product_id": other["id"]}, store.Int(pack["version"])); err == nil {
		t.Fatal("pack parent was reinterpreted")
	}
	if _, err = f.pool.Exec(f.ctx, `INSERT INTO pantry.packs(household_id,product_id,label,quantity,unit_code,created_by)
 SELECT $1,$2,'Pack '||n,1,'kg',$3 FROM generate_series(2,50) n`, f.identity.HouseholdID, pid, f.identity.UserID); err != nil {
		t.Fatal("50-pack boundary", err)
	}
	if _, err = f.pool.Exec(f.ctx, `INSERT INTO pantry.barcodes(household_id,product_id,code,created_by)
 SELECT $1,$2,'Bounded'||n,$3 FROM generate_series(2,100) n`, f.identity.HouseholdID, pid, f.identity.UserID); err != nil {
		t.Fatal("100-barcode boundary", err)
	}
	for _, tc := range []struct {
		entity string
		data   store.Record
	}{
		{"packs", store.Record{"product_id": pid, "label": "Over capacity", "quantity": "1", "unit_code": "kg"}},
		{"barcodes", store.Record{"product_id": pid, "code": "OverCapacity"}},
	} {
		if _, err = write(tc.entity, "", tc.data, 0); err == nil {
			t.Fatal("application capacity bypass", tc.entity)
		}
	}
	for _, sql := range []string{
		"INSERT INTO pantry.packs(household_id,product_id,label,quantity,unit_code,created_by) VALUES($1,$2,'SQL overflow',1,'kg',$3)",
		"INSERT INTO pantry.barcodes(household_id,product_id,code,created_by) VALUES($1,$2,'SQLOverflow',$3)",
		"UPDATE pantry.products SET barcode='LegacyEdit' WHERE household_id=$1 AND id=$2 AND created_by=$3",
		"UPDATE pantry.products SET unit_code='g' WHERE household_id=$1 AND id=$2 AND created_by=$3",
	} {
		if _, err = f.pool.Exec(f.ctx, sql, f.identity.HouseholdID, pid, f.identity.UserID); err == nil {
			t.Fatal("direct SQL guard bypass", sql)
		}
	}
	view, err := f.app.Store.PantryConsumptionView(f.ctx, f.identity, store.String(lot["id"]))
	if err != nil || len(view["packs"].([]any)) != 50 || len(view["barcodes"].([]any)) != 100 {
		t.Fatal("at-limit review was truncated or rejected", view, err)
	}
	for _, path := range []string{"/m/pantry/priority?product=" + pid, "/m/pantry/consume/" + store.String(lot["id"])} {
		response, raw, err := f.request("GET", path, f.token, "", "", nil, nil)
		if err != nil || response.StatusCode != 200 || !strings.Contains(string(raw), "pantry-") {
			t.Fatal("new page route", response, string(raw), err)
		}
	}
}
