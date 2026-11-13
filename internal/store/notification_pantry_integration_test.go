//go:build integration

package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"household/internal/value"
)

func notificationPantryWrite(t *testing.T, f *recipeExpansionFixture, entity, rid string, version int64, values Record) Record {
	t.Helper()
	m, _ := f.store.Module("pantry")
	e, _ := m.Entity(entity)
	data := make(map[string]json.RawMessage, len(values))
	for key, v := range values {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		data[key] = raw
	}
	r, err := f.store.Write(f.ctx, f.identity, m, e, rid, value.UUID(), value.UUID(), Mutation{Generation: f.store.States()["pantry"].Generation, Version: version, Data: data})
	if err != nil {
		t.Fatal("write real pantry source", err)
	}
	return r
}

func TestNotificationPantryDailyDigestConsentAndCurrentSources(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	early := f.identity
	late := financeAuthorityMember(t, f, "reader")
	pref := notificationChoreConsent(t, f, early, "pantry", false)
	firstProduct, firstLot := notificationPantryFixture(t, f, "2", "3")
	secondProduct, secondLot := notificationPantryFixture(t, f, "2", "3")
	notificationPantryScan(t, f)
	notificationPantryConsume(t, f, firstLot, "2")
	notificationPantryScan(t, f)
	// This consent follows the last verified healthy observation. A later
	// scanner noticing low stock must not date the crossing after this consent.
	notificationChoreConsent(t, f, late, "pantry", false)
	// The second source is newer than the already-created digest job. Rollover
	// deduplication must cover merged references, not only the first reference.
	notificationPantryConsume(t, f, secondLot, "2")
	notificationPantryScan(t, f)
	var job string
	var raw []byte
	if err := f.app.QueryRow(f.ctx, `SELECT id::text,payload FROM core.jobs
 WHERE kind=$1 AND payload->>'user_id'=$2 AND state='pending'`, PantryNotificationDigestJobKind, early.UserID).Scan(&job, &raw); err != nil {
		t.Fatal("actual daily pantry digest missing", err)
	}
	var d pantryNotificationDigest
	if err := json.Unmarshal(raw, &d); err != nil || len(d.References) != 2 || !validPantryDigest(d) {
		t.Fatal("two private low-stock sources did not form one closed daily group", len(d.References), err)
	}
	seen := map[string]bool{}
	for _, ref := range d.References {
		seen[ref.ID] = true
		if ref.Kind != "low_stock" || ref.Episode != 1 || !ref.ExpiresAt.Equal(ref.EventAt.Add(24*time.Hour)) {
			t.Fatal("digest changed original source deadline", ref.Kind, ref.Episode)
		}
	}
	if !seen[firstProduct] || !seen[secondProduct] {
		t.Fatal("daily group does not reference both real products")
	}
	var lateJobs int
	if err := f.app.QueryRow(f.ctx, `SELECT count(*) FROM core.jobs WHERE kind=$1 AND payload->>'user_id'=$2`, PantryNotificationDigestJobKind, late.UserID).Scan(&lateJobs); err != nil || lateJobs != 0 {
		t.Fatal("late enrollment replayed an unobserved crossing", lateJobs, err)
	}
	lease := value.UUID()
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.jobs SET state='running',lease_token=$2,lease_until=clock_timestamp()+interval '60 seconds',attempts=attempts+1
 WHERE id=$1 AND state='pending' AND available_at<=clock_timestamp()`, job, lease); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { results <- f.store.CompletePantryNotificationDigest(f.ctx, job, lease) })
	}
	wg.Wait()
	close(results)
	succeeded, refused := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrConflict) {
			refused++
		} else {
			t.Fatal("concurrent daily digest completion", err)
		}
	}
	if succeeded != 1 || refused != 1 {
		t.Fatal("same digest lease finalized more than once", succeeded, refused)
	}
	var intents []string
	if err := f.app.QueryRow(f.ctx, `SELECT ARRAY(SELECT id::text FROM core.notification_intents WHERE resource_id=$1 AND category='pantry')`, job).Scan(&intents); err != nil || len(intents) != 1 {
		t.Fatal("daily digest did not create exactly one core intent", len(intents), err)
	}
	i := notificationChoreAudience(t, f, intents[0], true)
	if i.ResourceType != "pantry_digest" || i.Occurrence != "day/"+d.Day || i.Revision != 1 {
		t.Fatal("closed pantry intent contract differs", i.ResourceType, i.Occurrence, i.Revision)
	}
	for _, field := range []string{"user", "generation", "revision", "occurrence", "deadline"} {
		t.Run("closed reference rejects changed "+field, func(t *testing.T) {
			changed := i
			switch field {
			case "user":
				changed.UserID = late.UserID
			case "generation":
				changed.Generation++
			case "revision":
				changed.Revision++
			case "occurrence":
				changed.Occurrence = "day/2099-01-01"
			case "deadline":
				changed.ExpiresAt = changed.ExpiresAt.Add(time.Second)
			}
			tx, err := f.app.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rollback(tx)
			if allowed, err := f.store.NotificationAudience(f.ctx, tx, changed); err != nil || allowed {
				t.Fatal("modified pantry reference retained authority", field, allowed, err)
			}
		})
	}
	t.Run("local date rollover cannot replay a finalized source", func(t *testing.T) {
		var now time.Time
		if err := f.app.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
			t.Fatal(err)
		}
		// Change only the account's real timezone to cross its calendar day;
		// source instants, consent cutoffs and database clocks stay unchanged.
		pref.Timezone = "Pacific/Kiritimati"
		if now.UTC().Hour() < 11 {
			pref.Timezone = "Pacific/Pago_Pago"
		}
		var err error
		pref, err = f.store.SaveNotificationPreference(f.ctx, early, pref)
		if err != nil {
			t.Fatal(err)
		}
		notificationPantryScan(t, f)
		var count int
		if err := f.app.QueryRow(f.ctx, `SELECT count(*) FROM core.jobs WHERE kind=$1 AND payload->>'user_id'=$2`, PantryNotificationDigestJobKind, early.UserID).Scan(&count); err != nil || count != 1 {
			t.Fatal("different local day replayed the same finalized low-stock episode", count, err)
		}
		pref.Timezone = "UTC"
		if _, err := f.store.SaveNotificationPreference(f.ctx, early, pref); err != nil {
			t.Fatal(err)
		}
	})
	// Source currentness is checked live, without waiting for another stock
	// observation. One recovered source leaves the other generic notice valid.
	notificationPantryWrite(t, f, "stock_movements", "", 0, Record{"lot_id": firstLot, "reason": "receive", "quantity_delta": "3"})
	notificationChoreAudience(t, f, intents[0], true)
	notificationPantryWrite(t, f, "stock_movements", "", 0, Record{"lot_id": secondLot, "reason": "receive", "quantity_delta": "3"})
	notificationChoreAudience(t, f, intents[0], false)
	notificationPantryScan(t, f)
	var copies int
	if err := f.app.QueryRow(f.ctx, `SELECT count(*) FROM core.notification_intents WHERE resource_id=$1`, job).Scan(&copies); err != nil || copies != 1 {
		t.Fatal("restart/repeated scan replayed finalized daily digest", copies, err)
	}
}

func notificationPantryFixture(t *testing.T, f *recipeExpansionFixture, threshold, amount string) (string, string) {
	t.Helper()
	suffix := value.UUID()
	p := notificationPantryWrite(t, f, "products", "", 0, Record{"name": "Private pantry product " + suffix, "unit_code": "kg", "low_stock_threshold": threshold})
	l := notificationPantryWrite(t, f, "locations", "", 0, Record{"name": "Private pantry location " + suffix})
	lot := notificationPantryWrite(t, f, "lots", "", 0, Record{"product_id": p["id"], "location_id": l["id"], "received_on": "2026-01-01", "notes": "Private pantry lot"})
	if amount != "0" {
		notificationPantryWrite(t, f, "stock_movements", "", 0, Record{"lot_id": lot["id"], "reason": "receive", "quantity_delta": amount})
	}
	return String(p["id"]), String(lot["id"])
}

func notificationPantryStockInput(t *testing.T, f *recipeExpansionFixture, lot string) PantryStockInput {
	t.Helper()
	r, err := f.store.PantryLotState(f.ctx, f.identity, lot)
	if err != nil {
		t.Fatal(err)
	}
	return PantryStockInput{Generation: f.store.States()["pantry"].Generation, Action: "consume", Mode: "amount", Quantity: "1",
		LotVersion: Int(r["version"]), ProductVersion: Int(r["product_version"]), LocationVersion: Int(r["location_version"]),
		MovementCount: Int(r["movement_count"]), Balance: String(r["stock"])}
}

func notificationPantryConsume(t *testing.T, f *recipeExpansionFixture, lot, amount string) Record {
	t.Helper()
	in := notificationPantryStockInput(t, f, lot)
	in.Quantity = amount
	r, err := f.store.MutatePantryStock(f.ctx, f.identity, lot, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal("consume real pantry stock", err)
	}
	return r
}

type notificationPantryObservation struct {
	State, Quantity string
	Episode         int64
	Observed        time.Time
	Healthy, Cutoff *time.Time
}

func notificationPantryObserve(t *testing.T, f *recipeExpansionFixture, product string) notificationPantryObservation {
	t.Helper()
	var out notificationPantryObservation
	if err := f.app.QueryRow(f.ctx, `SELECT state,observed_quantity::text,episode,observed_at,last_healthy_at,consent_cutoff
 FROM pantry.notification_stock_observations WHERE household_id=$1 AND product_id=$2`, f.identity.HouseholdID, product).
		Scan(&out.State, &out.Quantity, &out.Episode, &out.Observed, &out.Healthy, &out.Cutoff); err != nil {
		t.Fatal("read persisted pantry observation", err)
	}
	return out
}

func notificationPantryScan(t *testing.T, f *recipeExpansionFixture) {
	t.Helper()
	// Each call advances a durable bounded page. Eight calls cover these small
	// fixtures including the >50-product cursor boundary without a worker clock.
	for range 8 {
		if _, err := f.store.StagePantryNotifications(f.ctx, 50); err != nil {
			t.Fatal("stage bounded pantry scan", err)
		}
	}
}

func TestNotificationPantryObservedStockLifecycle(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	notificationChoreConsent(t, f, f.identity, "pantry", false)
	t.Run("first low baseline never invents historical crossing", func(t *testing.T) {
		product, _ := notificationPantryFixture(t, f, "2", "1")
		notificationPantryScan(t, f)
		obs := notificationPantryObserve(t, f, product)
		if obs.State != "baseline" || obs.Episode != 0 || obs.Healthy != nil || obs.Cutoff != nil {
			t.Fatal("first already-low observation invented an episode", obs)
		}
	})
	t.Run("healthy refresh, consumption, undo and waste create distinct real episodes", func(t *testing.T) {
		product, lot := notificationPantryFixture(t, f, "2", "2.000001")
		notificationPantryScan(t, f)
		before := notificationPantryObserve(t, f, product)
		notificationPantryScan(t, f)
		healthy := notificationPantryObserve(t, f, product)
		if healthy.State != "healthy" || healthy.Healthy == nil || !healthy.Healthy.Equal(healthy.Observed) || healthy.Observed.Before(before.Observed) {
			t.Fatal("healthy scan did not refresh consent basis", healthy)
		}
		consumed := notificationPantryConsume(t, f, lot, "0.000002")
		notificationPantryScan(t, f)
		low := notificationPantryObserve(t, f, product)
		if low.State != "low" || low.Quantity != "1.999999" || low.Episode != 1 || low.Cutoff == nil || !low.Cutoff.Equal(healthy.Observed) {
			t.Fatal("exact threshold crossing lost healthy cutoff", low)
		}
		notificationPantryScan(t, f)
		stable := notificationPantryObserve(t, f, product)
		if stable.Episode != low.Episode || stable.Cutoff == nil || !stable.Cutoff.Equal(*low.Cutoff) {
			t.Fatal("persistent low stock created a repeated episode", stable)
		}
		undo := notificationPantryStockInput(t, f, lot)
		undo.Action, undo.MovementID = "undo", String(consumed["movement_id"])
		if _, err := f.store.MutatePantryStock(f.ctx, f.identity, lot, value.UUID(), value.UUID(), undo); err != nil {
			t.Fatal("undo actual consumption", err)
		}
		notificationPantryScan(t, f)
		restored := notificationPantryObserve(t, f, product)
		if restored.State != "healthy" || restored.Quantity != "2.000001" {
			t.Fatal("undo did not restore healthy balance", restored)
		}
		review := notificationPantryStockInput(t, f, lot)
		waste := PantryWasteInput{Generation: review.Generation, Action: "waste", Quantity: "0.000002", Reason: "spoiled",
			LotVersion: review.LotVersion, ProductVersion: review.ProductVersion, LocationVersion: review.LocationVersion,
			MovementCount: review.MovementCount, Balance: review.Balance}
		wasted, err := f.store.MutatePantryWaste(f.ctx, f.identity, lot, value.UUID(), value.UUID(), waste)
		if err != nil {
			t.Fatal("book real waste", err)
		}
		notificationPantryScan(t, f)
		second := notificationPantryObserve(t, f, product)
		if second.State != "low" || second.Episode != 2 || second.Quantity != "1.999999" {
			t.Fatal("new waste crossing did not advance one episode", second)
		}
		review = notificationPantryStockInput(t, f, lot)
		waste = PantryWasteInput{Generation: review.Generation, Action: "undo", MovementID: String(wasted["movement_id"]),
			LotVersion: review.LotVersion, ProductVersion: review.ProductVersion, LocationVersion: review.LocationVersion,
			MovementCount: review.MovementCount, Balance: review.Balance}
		if _, err := f.store.MutatePantryWaste(f.ctx, f.identity, lot, value.UUID(), value.UUID(), waste); err != nil {
			t.Fatal("undo real waste", err)
		}
		notificationPantryScan(t, f)
		if after := notificationPantryObserve(t, f, product); after.State != "healthy" || after.Quantity != "2.000001" || after.Episode != 2 {
			t.Fatal("waste undo reset historical episode or failed recovery", after)
		}
	})
	t.Run("multiple lots retain exact physical stock including expired inventory", func(t *testing.T) {
		product, first := notificationPantryFixture(t, f, "2", "0.999999")
		loc := notificationPantryWrite(t, f, "locations", "", 0, Record{"name": "Second private shelf"})
		second := notificationPantryWrite(t, f, "lots", "", 0, Record{"product_id": product, "location_id": loc["id"],
			"received_on": "2020-01-01", "use_by_on": "2020-01-02", "notes": "Expired inventory remains physical stock"})
		notificationPantryWrite(t, f, "stock_movements", "", 0, Record{"lot_id": second["id"], "reason": "receive", "quantity_delta": "1.000001"})
		notificationPantryScan(t, f)
		if obs := notificationPantryObserve(t, f, product); obs.State != "healthy" || obs.Quantity != "2.000000" {
			t.Fatal("low stock omitted a lot or rounded physical stock", obs)
		}
		notificationPantryConsume(t, f, first, "0.000001")
		notificationPantryScan(t, f)
		if obs := notificationPantryObserve(t, f, product); obs.State != "low" || obs.Quantity != "1.999999" || obs.Episode != 1 {
			t.Fatal("aggregate crossing lost exact micro-unit", obs)
		}
	})
	t.Run("configuration changes require a new healthy baseline", func(t *testing.T) {
		product, lot := notificationPantryFixture(t, f, "2", "3")
		notificationPantryScan(t, f)
		m, _ := f.store.Module("pantry")
		e, _ := m.Entity("products")
		p, err := f.store.Get(f.ctx, f.identity, m, e, product)
		if err != nil {
			t.Fatal(err)
		}
		notificationPantryWrite(t, f, "products", product, Int(p["version"]), Record{"low_stock_threshold": "4"})
		notificationPantryScan(t, f)
		if obs := notificationPantryObserve(t, f, product); obs.State != "baseline" || obs.Episode != 0 || obs.Cutoff != nil {
			t.Fatal("threshold edit fabricated a stock transition", obs)
		}
		notificationPantryWrite(t, f, "stock_movements", "", 0, Record{"lot_id": lot, "reason": "receive", "quantity_delta": "2"})
		notificationPantryScan(t, f)
		notificationPantryConsume(t, f, lot, "2")
		notificationPantryScan(t, f)
		if obs := notificationPantryObserve(t, f, product); obs.State != "low" || obs.Episode != 1 {
			t.Fatal("restock after config reset could not establish a new episode", obs)
		}
		// There is no generic archive command for pantry products. Probe this
		// persisted authority gate explicitly through the isolated fixture owner.
		if _, err := f.owner.Exec(f.ctx, "UPDATE pantry.products SET archived_at=clock_timestamp() WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, product); err != nil {
			t.Fatal("install archived source state", err)
		}
		notificationPantryScan(t, f)
		if obs := notificationPantryObserve(t, f, product); obs.State != "ineligible" || obs.Cutoff != nil || obs.Episode != 1 {
			t.Fatal("archived stock retained active episode authority", obs)
		}
	})
	t.Run("durable scan traverses more than fifty products", func(t *testing.T) {
		for range 51 {
			notificationPantryWrite(t, f, "products", "", 0, Record{"name": "Cursor boundary " + value.UUID(), "unit_code": "kg", "low_stock_threshold": "0"})
		}
		notificationPantryScan(t, f)
		var missing int
		if err := f.app.QueryRow(f.ctx, `SELECT count(*) FROM pantry.products p
 WHERE p.household_id=$1 AND NOT EXISTS(SELECT 1 FROM pantry.notification_stock_observations o
 WHERE o.household_id=p.household_id AND o.product_id=p.id)`, f.identity.HouseholdID).Scan(&missing); err != nil || missing != 0 {
			t.Fatal("bounded scan stranded products behind first page", missing, err)
		}
	})
}

func TestNotificationPantryExpiryDatesAndRemainingStock(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	var now time.Time
	if err := f.app.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	// Keep the real 09:00 deadline about three hours in the past, independent
	// of when CI runs. This avoids crossing an expiry boundary during migration.
	zone := fmt.Sprintf("Etc/GMT%+d", now.UTC().Hour()-12)
	location, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	local := now.In(location)
	due := time.Date(local.Year(), local.Month(), local.Day(), 9, 0, 0, 0, location)
	dueDate, future := due.Format(time.DateOnly), due.AddDate(0, 0, 2).Format(time.DateOnly)
	// Explicit historical fixture consent precedes the real calendar deadline.
	// Production never rewrites consent or scheduled source timestamps.
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.households SET timezone=$2 WHERE id=$1`, f.identity.HouseholdID, zone); err != nil {
		t.Fatal(err)
	}
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.module_states SET changed_at=clock_timestamp()-interval '7 days' WHERE household_id=$1 AND module_id='pantry'`, f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	notificationChoreConsent(t, f, f.identity, "pantry", true)
	p := notificationPantryWrite(t, f, "products", "", 0, Record{"name": "Private dated stock", "unit_code": "kg"})
	l := notificationPantryWrite(t, f, "locations", "", 0, Record{"name": "Private expiry shelf"})
	lots := make(map[string]string)
	for _, tc := range []struct {
		name string
		data Record
	}{
		{"use-by", Record{"use_by_on": dueDate, "best_before_on": future}},
		{"best-before", Record{"best_before_on": dueDate}},
		{"use-by-wins", Record{"use_by_on": future, "best_before_on": dueDate}},
		{"opening-hint", Record{"opened_on": dueDate, "after_open_days": 0, "after_open_source": "user"}},
		{"consumed", Record{"use_by_on": dueDate}},
	} {
		tc.data["product_id"], tc.data["location_id"] = p["id"], l["id"]
		tc.data["received_on"], tc.data["notes"] = "2020-01-01", "Private expiry "+tc.name
		lot := notificationPantryWrite(t, f, "lots", "", 0, tc.data)
		lots[tc.name] = String(lot["id"])
		notificationPantryWrite(t, f, "stock_movements", "", 0, Record{"lot_id": lot["id"], "reason": "receive", "quantity_delta": "1"})
	}
	notificationPantryConsume(t, f, lots["consumed"], "1")
	notificationPantryScan(t, f)
	var job string
	var raw []byte
	if err := f.app.QueryRow(f.ctx, `SELECT id::text,payload FROM core.jobs WHERE kind=$1 AND payload->>'user_id'=$2 AND state='pending'`, PantryNotificationDigestJobKind, f.identity.UserID).Scan(&job, &raw); err != nil {
		t.Fatal("real expiry digest missing", err)
	}
	var d pantryNotificationDigest
	if err := json.Unmarshal(raw, &d); err != nil || len(d.References) != 2 {
		t.Fatal("expiry selected opening hint, exhausted lot or overridden best-before", len(d.References), err)
	}
	selected := map[string]bool{}
	for _, ref := range d.References {
		var lot string
		if err := f.app.QueryRow(f.ctx, "SELECT resource_id::text FROM core.reminders WHERE id=$1", ref.ID).Scan(&lot); err != nil {
			t.Fatal(err)
		}
		selected[lot] = true
		if ref.Kind != "expiry" || !ref.EventAt.Equal(due) || !ref.Cutoff.Equal(due) || !ref.ExpiresAt.Equal(due.Add(24*time.Hour)) {
			t.Fatal("expiry changed the canonical household 09:00 deadline")
		}
	}
	if !selected[lots["use-by"]] || !selected[lots["best-before"]] {
		t.Fatal("canonical expiry sources missing")
	}
	lease := value.UUID()
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.jobs SET state='running',lease_token=$2,lease_until=clock_timestamp()+interval '60 seconds',attempts=attempts+1 WHERE id=$1`, job, lease); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CompletePantryNotificationDigest(f.ctx, job, lease); err != nil {
		t.Fatal(err)
	}
	var intent string
	if err := f.app.QueryRow(f.ctx, "SELECT id::text FROM core.notification_intents WHERE resource_id=$1", job).Scan(&intent); err != nil {
		t.Fatal(err)
	}
	notificationChoreAudience(t, f, intent, true)
	m, _ := f.store.Module("pantry")
	e, _ := m.Entity("lots")
	old, err := f.store.Get(f.ctx, f.identity, m, e, lots["use-by"])
	if err != nil {
		t.Fatal(err)
	}
	notificationPantryWrite(t, f, "lots", lots["use-by"], Int(old["version"]), Record{"use_by_on": future})
	notificationChoreAudience(t, f, intent, true)
	notificationPantryConsume(t, f, lots["best-before"], "1")
	notificationChoreAudience(t, f, intent, false)
}

func TestNotificationPantryPendingDigestReplacesFiftyRecoveredSources(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	notificationChoreConsent(t, f, f.identity, "pantry", false)
	oldLots := make([]string, 0, 50)
	for range 50 {
		_, lot := notificationPantryFixture(t, f, "2", "3")
		oldLots = append(oldLots, lot)
	}
	freshProduct, freshLot := notificationPantryFixture(t, f, "2", "3")
	notificationPantryScan(t, f)
	for _, lot := range oldLots {
		notificationPantryConsume(t, f, lot, "2")
	}
	notificationPantryScan(t, f)
	var job string
	var raw []byte
	if err := f.app.QueryRow(f.ctx, `SELECT id::text,payload FROM core.jobs WHERE kind=$1 AND payload->>'user_id'=$2 AND state='pending'`, PantryNotificationDigestJobKind, f.identity.UserID).Scan(&job, &raw); err != nil {
		t.Fatal(err)
	}
	var before pantryNotificationDigest
	if err := json.Unmarshal(raw, &before); err != nil || len(before.References) != 50 {
		t.Fatal("fixture did not fill the pending bounded digest", len(before.References), err)
	}
	// Keep the real pending job unprocessed while every old source recovers.
	// A new current shortage must replace these stale references before LIMIT50.
	for _, lot := range oldLots {
		notificationPantryWrite(t, f, "stock_movements", "", 0, Record{"lot_id": lot, "reason": "receive", "quantity_delta": "3"})
	}
	notificationPantryConsume(t, f, freshLot, "2")
	notificationPantryScan(t, f)
	if err := f.app.QueryRow(f.ctx, "SELECT payload FROM core.jobs WHERE id=$1", job).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var after pantryNotificationDigest
	if err := json.Unmarshal(raw, &after); err != nil || len(after.References) != 1 || after.References[0].ID != freshProduct {
		t.Fatal("recovered references monopolized the pending daily group", len(after.References), err)
	}
	lease := value.UUID()
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.jobs SET state='running',lease_token=$2,lease_until=clock_timestamp()+interval '60 seconds',attempts=attempts+1 WHERE id=$1`, job, lease); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CompletePantryNotificationDigest(f.ctx, job, lease); err != nil {
		t.Fatal(err)
	}
	var intent string
	if err := f.app.QueryRow(f.ctx, "SELECT id::text FROM core.notification_intents WHERE resource_id=$1", job).Scan(&intent); err != nil {
		t.Fatal("fresh shortage was deferred or lost despite available daily slot", err)
	}
	notificationChoreAudience(t, f, intent, true)
}
