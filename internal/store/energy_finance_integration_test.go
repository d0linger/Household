//go:build integration

package store

import (
	"fmt"
	"testing"
	"time"

	"household/internal/value"
)

func TestEnergyNativeTariffPaymentsAndExplicitConfirmedFinanceHandoff(t *testing.T) {
	f := newEnergyNative(t, "manual")
	day := f.start.Format(time.DateOnly)
	energyNativeWrite(t, f.f, "reading.save", "", 0, f.readingData(t, f.start.Add(2*time.Hour), "20", "actual"))
	energyNativeWrite(t, f.f, "tariff.save", "", 0, Record{"register_id": f.register, "register_version": "1", "starts_on": day, "price_minor_per_unit": "2", "base_minor_per_day": "0", "label": "Actual contract"})
	energyNativeWrite(t, f.f, "payment.save", "", 0, Record{"register_id": f.register, "register_version": "1", "paid_on": day, "amount_minor": "10"})
	energyNativeProject(t, f.f)
	costs, err := f.f.store.EnergySeries(f.f.ctx, f.f.identity, f.query())
	if err != nil || String(costs["known_cost_minor"]) != "40" || String(costs["payments_minor"]) != "10" {
		t.Fatal(costs, err)
	}
	outside := f.start.AddDate(0, 0, -1).Format(time.DateOnly)
	energyNativeWrite(t, f.f, "payment.save", "", 0, Record{"register_id": f.register, "register_version": "1", "paid_on": outside, "amount_minor": "999"})
	payments, err := f.f.store.EnergyManagement(f.f.ctx, f.f.identity, "payments", f.register, f.query())
	if err != nil || len(payments["rows"].([]Record)) != 1 {
		t.Fatal("financial period was ignored", payments, err)
	}
	bill := energyNativeWrite(t, f.f, "bill.save", "", 0, Record{"register_id": f.register, "register_version": "1", "occurred_on": day, "title": "Actual bill", "amount_minor": "12345", "kind": "invoice"})
	module, _ := f.f.store.Module("expenses")
	entity, _ := module.Entity("categories")
	category, err := f.f.store.Write(f.f.ctx, f.f.identity, module, entity, "", value.UUID(), value.UUID(), Mutation{Generation: f.f.store.States()["expenses"].Generation, Data: energyRaw(Record{"name": "Actual energy category"})})
	if err != nil {
		t.Fatal(err)
	}
	command := EnergyCommand{Generation: f.f.store.States()["meters"].Generation, Action: "bill.handoff", ID: String(bill["id"]), Version: 1, Data: energyRaw(Record{"confirmed": true, "category_id": category["id"]}), TargetGenerations: map[string]int64{"expenses": f.f.store.States()["expenses"].Generation}}
	if _, err = f.f.store.EnergyWrite(f.f.ctx, f.f.identity, value.UUID(), value.UUID(), command); err == nil {
		t.Fatal("unconfirmed bill posted money")
	}
	bill = energyNativeWrite(t, f.f, "bill.confirm", String(bill["id"]), 1, Record{"confirmed": true})
	command.Version = Int(bill["version"])
	key := value.UUID()
	transferred, err := f.f.store.EnergyWrite(f.f.ctx, f.f.identity, key, value.UUID(), command)
	if err != nil {
		t.Fatal("actual Core finance handoff", err)
	}
	if _, err = f.f.store.EnergyWrite(f.f.ctx, f.f.identity, key, value.UUID(), command); err != nil {
		t.Fatal("unchanged handoff replay", err)
	}
	var count int
	var amount string
	if err = f.f.owner.QueryRow(f.f.ctx, `SELECT count(*),coalesce(sum(amount_minor),0)::text FROM expenses.entries WHERE id=$1 AND state='posted'`, transferred["expense_id"]).Scan(&count, &amount); err != nil || count != 1 || amount != "12345" {
		t.Fatal("estimate or duplicate was posted", count, amount, err)
	}
	if fmt.Sprint(transferred["expense_id"]) == "" {
		t.Fatal("actual entry identity absent")
	}
}
