//go:build integration && testtools

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/expenseedit"
	"household/internal/finance"
	"household/internal/notification"
	"household/internal/secretbox"
	"household/internal/store"
	"household/internal/value"
	"household/internal/webpush"
	"household/internal/worker"
)

func notificationFinanceWebApply(t *testing.T, f *integrationFixture, resource, action, rid string, version int64, input any) store.Record {
	t.Helper()
	kind := resource
	if resource == "budgets" {
		kind = "category_budgets"
	}
	refs, intent := []store.FinanceResourceRef{}, "view"
	if version > 0 {
		refs, intent = []store.FinanceResourceRef{{Kind: kind, ID: rid}}, "edit"
	}
	encoded, err := json.Marshal(refs)
	if err != nil {
		t.Fatal(err)
	}
	gen := f.app.Store.States()["expenses"].Generation
	var stamp string
	err = f.pool.QueryRow(f.ctx, `SELECT core.finance_authority($1,$2,$3,$4,$5,$6,$7::jsonb,$8)`,
		f.identity.HouseholdID, f.identity.UserID, f.identity.MembershipID, f.identity.SessionID, f.identity.AuthVersion, gen, encoded, intent).Scan(&stamp)
	if err != nil {
		t.Fatal("current reviewed Finance command authority", err)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	command := store.FinanceCommand{Resource: resource, Action: action, ID: rid, Version: version, Generation: gen, AccessVersion: stamp, Payload: raw}
	if resource == "budgets" && version == 0 {
		command.InitialAccess = &store.FinanceInitialAccess{AccessMode: "household", ResponsibleMembershipID: &f.identity.MembershipID, Grants: []store.FinanceInitialGrant{}}
	}
	return notificationFinanceWebCommand(t, f, command)
}

func notificationFinanceWebCommand(t *testing.T, f *integrationFixture, command store.FinanceCommand) store.Record {
	t.Helper()
	key := value.UUID()
	preview, err := f.app.Store.FinancePreview(f.ctx, f.identity, key, value.UUID(), command)
	if err != nil {
		t.Fatal("actual Finance preview", command.Resource, command.Action, err)
	}
	command.ReviewHash = store.String(preview["impact_hash"])
	result, err := f.app.Store.FinanceApply(f.ctx, f.identity, key, value.UUID(), command)
	if err != nil {
		t.Fatal("actual reviewed Finance apply", command.Resource, command.Action, err)
	}
	return result
}

func notificationFinanceWebEntry(t *testing.T, f *integrationFixture, category, amount string) store.FinanceEntryInput {
	t.Helper()
	var day string
	if err := f.pool.QueryRow(f.ctx, `SELECT to_char(clock_timestamp() AT TIME ZONE timezone,'YYYY-MM-DD') FROM core.households WHERE id=$1`, f.identity.HouseholdID).Scan(&day); err != nil {
		t.Fatal(err)
	}
	return store.FinanceEntryInput{OccurredOn: day, Description: "PRIVATE-FINANCE invoice", Payee: "PRIVATE-FINANCE recipient", Kind: "expense",
		Money: finance.MoneyInput{Currency: "EUR", Exponent: 2, Minor: amount}, Lines: []store.FinanceEntryLineInput{{ID: value.UUID(), CategoryID: category, OriginalMinor: amount}},
		Baseline: []expenseedit.LineVersion{}, Removed: []expenseedit.LineVersion{}}
}

// Advancing only the disposable scanner's available_at represents a later
// scheduler tick. Source timestamps, observation cutoffs and freshness stay real.
func notificationFinanceWebScan(t *testing.T, f *integrationFixture, service *webpush.Service) {
	t.Helper()
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.jobs SET available_at=clock_timestamp() WHERE household_id=$1 AND kind=$2 AND state='pending'`, f.identity.HouseholdID, store.FinanceNotificationScanJobKind); err != nil {
		t.Fatal(err)
	}
	for range 32 {
		worked, err := service.StageFinanceNotifications(f.ctx, 10)
		if err != nil {
			t.Fatal("actual Finance scan", err)
		}
		if worked == 0 {
			// A scan can defer an unusable registering session without advancing
			// a page. Other users must still receive their real scheduler turns.
			var pending bool
			err = f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM core.notification_preferences p
 JOIN core.module_states m ON m.household_id=p.household_id AND m.module_id='expenses' AND m.state='enabled'
 JOIN core.memberships member ON member.household_id=p.household_id AND member.user_id=p.user_id AND member.inactive_at IS NULL
 JOIN core.users u ON u.id=p.user_id AND u.disabled_at IS NULL AND NOT u.must_change_password
 LEFT JOIN core.jobs j ON j.dedupe_key='finance-scan-v1/'||p.household_id::text||'/'||p.user_id::text||'/'||m.data_generation::text
 WHERE p.household_id=$1 AND p.category='expenses' AND p.enabled
 AND(j.id IS NULL OR(j.kind=$2 AND j.state='pending' AND j.available_at<=clock_timestamp()))
 AND EXISTS(SELECT 1 FROM core.notification_device_preferences d JOIN core.push_subscriptions s ON s.id=d.subscription_id
 WHERE d.household_id=p.household_id AND d.user_id=p.user_id AND d.category='expenses' AND d.enabled AND NOT d.paused AND s.disabled_at IS NULL))`,
				f.identity.HouseholdID, store.FinanceNotificationScanJobKind).Scan(&pending)
			if err != nil {
				t.Fatal("remaining real Finance scheduler turns", err)
			}
			if !pending {
				return
			}
		}
	}
	t.Fatal("bounded Finance fixture scan did not complete")
}

type notificationFinanceBudgetDelivery struct {
	actor                                 auth.Identity
	budget, category, entry, subscription string
	entryVersion                          int64
	device                                webpush.DevicePreference
	delivery                              string
}

func notificationFinanceWebBudget(t *testing.T, f *integrationFixture, service *webpush.Service, suffix string) notificationFinanceBudgetDelivery {
	t.Helper()
	q := notificationFinanceBudgetDelivery{actor: shoppingTestMember(t, f, "finance-"+suffix, "reader"), budget: value.UUID()}
	if _, err := f.app.Store.SaveNotificationPreference(f.ctx, q.actor, store.NotificationPreference{Category: "expenses", Enabled: true, QuietHours: notification.QuietHours{Timezone: "UTC"}}); err != nil {
		t.Fatal(err)
	}
	connection, err := service.Register(f.ctx, q.actor, shoppingPushInput(t, service))
	if err != nil {
		t.Fatal(err)
	}
	q.subscription = connection.ID
	q.device, err = service.SaveDevicePreference(f.ctx, q.actor, q.subscription, webpush.DevicePreference{Category: "expenses", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	category, _ := auditCreate(t, f, "expenses", "categories", store.Record{"name": "PRIVATE-FINANCE " + suffix})
	q.category = store.String(category["id"])
	var month string
	if err = f.pool.QueryRow(f.ctx, `SELECT to_char(clock_timestamp() AT TIME ZONE timezone,'YYYY-MM-01') FROM core.households WHERE id=$1`, f.identity.HouseholdID).Scan(&month); err != nil {
		t.Fatal(err)
	}
	notificationFinanceWebApply(t, f, "budgets", "save", q.budget, 0, store.FinanceBudgetInput{Name: "PRIVATE-FINANCE budget", CategoryID: q.category, StartsOn: month,
		MonthlyMinor: "10000", CarryRule: "none", InitialCarryMinor: "0"})
	notificationFinanceWebScan(t, f, service)
	var healthy bool
	if err = f.pool.QueryRow(f.ctx, `SELECT count(*)=2 AND bool_and(state='healthy') FROM expenses.notification_budget_observations WHERE household_id=$1 AND user_id=$2 AND budget_id=$3`, f.identity.HouseholdID, q.actor.UserID, q.budget).Scan(&healthy); err != nil || !healthy {
		t.Fatal("actual healthy budget baseline missing", healthy, err)
	}
	if suffix == "late-device" {
		q.device.Enabled = false
		q.device, err = service.SaveDevicePreference(f.ctx, q.actor, q.subscription, q.device)
		if err != nil {
			t.Fatal(err)
		}
		q.device.Enabled = true
		q.device, err = service.SaveDevicePreference(f.ctx, q.actor, q.subscription, q.device)
		if err != nil {
			t.Fatal(err)
		}
	}
	q.entry = value.UUID()
	posted := notificationFinanceWebApply(t, f, "entries", "post", q.entry, 0, notificationFinanceWebEntry(t, f, q.category, "8500"))
	q.entryVersion = store.Int(posted["saved_version"])
	if suffix == "worker-fresh" {
		return q
	}
	notificationFinanceWebScan(t, f, service)
	var intent string
	if err = f.pool.QueryRow(f.ctx, `SELECT id::text FROM core.notification_intents WHERE household_id=$1 AND user_id=$2 AND resource_type='budget_thresholds' AND resource_id=$3 AND state='pending'`,
		f.identity.HouseholdID, q.actor.UserID, q.budget).Scan(&intent); err != nil {
		t.Fatal("real authorized budget crossing did not produce intent", err)
	}
	if err = service.ScheduleNotifications(f.ctx); err != nil {
		t.Fatal(err)
	}
	var deliveries []string
	if err = f.pool.QueryRow(f.ctx, `SELECT ARRAY(SELECT id::text FROM core.notification_deliveries WHERE intent_id=$1 AND subscription_id=$2)`, intent, q.subscription).Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	if suffix == "late-device" {
		if len(deliveries) != 0 {
			t.Fatal("late device consent replayed earlier budget history")
		}
		return q
	}
	if len(deliveries) != 1 {
		t.Fatal("current budget recipient did not produce exactly one device delivery", len(deliveries))
	}
	q.delivery = deliveries[0]
	return q
}

func notificationFinanceWebRestrictBudget(t *testing.T, f *integrationFixture, budget string) {
	t.Helper()
	for range 2 {
		id := f.identity
		generation := f.app.Store.States()["expenses"].Generation
		var raw []byte
		// Use the current content-authorized projection, not administrator recovery metadata.
		err := f.pool.QueryRow(f.ctx, `SELECT core.finance_access_projection($1,$2,$3,$4,$5,$6,'category_budgets',$7,false)`,
			id.HouseholdID, id.UserID, id.MembershipID, id.SessionID, id.AuthVersion, generation, budget).Scan(&raw)
		if err != nil {
			t.Fatal("current budget access", err)
		}
		var access store.Record
		if err = json.Unmarshal(raw, &access); err != nil {
			t.Fatal(err)
		}
		var version int64
		if err = f.pool.QueryRow(f.ctx, `SELECT version FROM expenses.category_budgets WHERE household_id=$1 AND id=$2`, id.HouseholdID, budget).Scan(&version); err != nil {
			t.Fatal(err)
		}
		action := "change"
		input := store.Record{"target_kind": "category_budgets", "target_id": budget, "policy_version": access["policy_version"], "access_mode": "selected", "responsible_membership_id": id.MembershipID,
			"grants": []store.Record{{"membership_id": id.MembershipID, "permission": "edit"}}}
		if access["can_assign"] == true {
			action = "assign"
			input = store.Record{"target_kind": "category_budgets", "target_id": budget, "policy_version": access["policy_version"], "responsible_membership_id": id.MembershipID}
		}
		payload, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		notificationFinanceWebCommand(t, f, store.FinanceCommand{Resource: "access", Action: action, ID: budget, Version: version, Generation: generation, AccessVersion: store.String(access["access_version"]), Payload: payload})
		if action == "change" {
			return
		}
	}
	t.Fatal("reviewed budget responsibility did not allow restriction")
}
func TestNotificationFinanceControlledDeliveryAndAuthority(t *testing.T) {
	f := newIntegrationFixture(t)
	service, _ := shoppingPushService(t, f, true)
	var calls atomic.Int32
	if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		wire, err := io.ReadAll(io.LimitReader(req.Body, 8193))
		ttl, ttlErr := strconv.Atoi(req.Header.Get("TTL"))
		if err != nil || ttlErr != nil || ttl < 1 || ttl > 300 || req.Header.Get("Urgency") != "low" || len(wire) < 100 || len(wire) > 8192 ||
			bytes.Contains(wire, []byte("PRIVATE-FINANCE")) || bytes.Contains(wire, []byte("audience_scope")) || bytes.Contains(wire, []byte("spent_minor")) {
			return nil, errors.New("Finance notification lost encrypted bounded generic wire format")
		}
		return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
	})); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"accepted", "latest-total", "logged-out", "permission-withdrawn", "late-device"} {
		t.Run(action, func(t *testing.T) {
			q := notificationFinanceWebBudget(t, f, service, action)
			if q.delivery == "" {
				return
			}
			switch action {
			case "latest-total":
				notificationFinanceWebApply(t, f, "entries", "reverse", value.UUID(), 0, store.Record{"original_entry_id": q.entry,
					"original_version": q.entryVersion, "occurred_on": notificationFinanceWebEntry(t, f, q.category, "1").OccurredOn, "reason": "PRIVATE-FINANCE explicit reversal"})
			case "logged-out":
				if err := f.app.Auth.Logout(f.ctx, q.actor); err != nil {
					t.Fatal(err)
				}
			case "permission-withdrawn":
				notificationFinanceWebRestrictBudget(t, f, q.budget)
			}
			before := calls.Load()
			job, lease := notificationClaimFixture(t, f, q.delivery)
			if err := service.SendNotificationJob(f.ctx, job, lease); err != nil {
				t.Fatal("current registered-session Finance send", err)
			}
			want := int32(0)
			if action == "accepted" {
				want = 1
			}
			if calls.Load()-before != want {
				t.Fatal("Finance current authority or values were ignored", action, calls.Load()-before, want)
			}
		})
	}
	t.Run("permission revocation after transport-slot wait prevents provider request", func(t *testing.T) {
		q := notificationFinanceWebBudget(t, f, service, "after-slot")
		type leased struct{ job, token string }
		var occupied []leased
		for _, name := range []string{"finance-slot-one", "finance-slot-two"} {
			actor := shoppingTestMember(t, f, name, "reader")
			connection, err := service.Register(f.ctx, actor, shoppingPushInput(t, service))
			if err != nil {
				t.Fatal(err)
			}
			delivery, err := service.QueueTest(f.ctx, actor, connection.ID)
			if err != nil {
				t.Fatal(err)
			}
			job, token := notificationClaimFixture(t, f, delivery.ID)
			occupied = append(occupied, leased{job, token})
		}
		entered, release := make(chan struct{}, 3), make(chan struct{})
		var once sync.Once
		unblock := func() { once.Do(func() { close(release) }) }
		var requests atomic.Int32
		if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(req *http.Request) (*http.Response, error) {
			requests.Add(1)
			entered <- struct{}{}
			select {
			case <-release:
				return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		})); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(f.ctx)
		results := make(chan error, 3)
		var work sync.WaitGroup
		defer func() { unblock(); cancel(); work.Wait() }()
		for _, item := range occupied {
			work.Go(func() { results <- service.SendNotificationJob(ctx, item.job, item.token) })
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("controlled slot did not become occupied")
			}
		}
		job, lease := notificationClaimFixture(t, f, q.delivery)
		work.Go(func() { results <- service.SendNotificationJob(ctx, job, lease) })
		until := time.Now().Add(time.Second)
		for {
			var reserved bool
			if err := f.pool.QueryRow(f.ctx, `SELECT last_attempt_at IS NOT NULL FROM core.notification_deliveries WHERE id=$1`, q.delivery).Scan(&reserved); err != nil {
				t.Fatal(err)
			}
			if reserved {
				break
			}
			if time.Now().After(until) {
				t.Fatal("Finance request did not reach transport-slot wait")
			}
			time.Sleep(5 * time.Millisecond)
		}
		notificationFinanceWebRestrictBudget(t, f, q.budget)
		unblock()
		for range 3 {
			if err := <-results; err != nil {
				t.Fatal("controlled Finance final guard", err)
			}
		}
		if requests.Load() != 2 {
			t.Fatal("revoked private Finance authority reached provider", requests.Load())
		}
	})
}

func TestNotificationFinanceActualWorkerRestart(t *testing.T) {
	f := newIntegrationFixture(t)
	service, cfg := shoppingPushService(t, f, true)
	var calls atomic.Int32
	if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
	})); err != nil {
		t.Fatal(err)
	}
	q := notificationFinanceWebBudget(t, f, service, "worker-fresh")
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.jobs SET available_at=clock_timestamp() WHERE household_id=$1 AND kind=$2 AND state='pending'`, f.identity.HouseholdID, store.FinanceNotificationScanJobKind); err != nil {
		t.Fatal(err)
	}
	tick := func() {
		t.Helper()
		background := &worker.Worker{Pool: f.pool, Cfg: cfg, Log: f.app.Log, Push: service}
		if err := background.Tick(f.ctx); err != nil {
			t.Fatal("actual Finance worker tick", err)
		}
		// The shared service remains alive across this test's worker instances.
		background.Push = nil
		if err := background.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		tick()
	}
	var accepted bool
	if err := f.pool.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM core.notification_deliveries d JOIN core.notification_intents i ON i.id=d.intent_id
 WHERE i.resource_type='budget_thresholds' AND i.resource_id=$1 AND i.user_id=$2 AND d.state='accepted')`, q.budget, q.actor.UserID).Scan(&accepted); err != nil || !accepted || calls.Load() != 1 {
		t.Fatal("actual Worker did not produce and deliver fresh budget crossing", accepted, calls.Load(), err)
	}
	// The first recipient can also see the next household budget. Withdraw its
	// device consent through the real API before isolating restart delivery, so
	// a legitimate second recipient cannot be mistaken for a duplicate send.
	q.device.Enabled = false
	if _, err := service.SaveDevicePreference(f.ctx, q.actor, q.subscription, q.device); err != nil {
		t.Fatal("withdraw completed worker fixture device", err)
	}
	q = notificationFinanceWebBudget(t, f, service, "worker-restart")
	// Reclaim a genuinely staged job after a simulated expired lease. A new Worker
	// instance has neither an in-memory cursor nor prior delivery state.
	job, _ := notificationClaimFixture(t, f, q.delivery)
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, job); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		tick()
	}
	if err := f.pool.QueryRow(f.ctx, `SELECT state='accepted' FROM core.notification_deliveries WHERE id=$1`, q.delivery).Scan(&accepted); err != nil || !accepted || calls.Load() != 2 {
		t.Fatal("actual worker recovery duplicated or lost financial delivery", accepted, calls.Load(), err)
	}
}

// This disposable fixture models registration and explicit confirmation before
// the most recent civil09:00 stage. It changes clocks only, repairs the existing
// entry's authenticated current row image, and reseals the real registration.
// No notification, intent, delivery, financial amount or permission is invented.
func notificationFinanceWebHistoricalDate(t *testing.T, f *integrationFixture, entry, subscription string, before time.Time) {
	t.Helper()
	tx, err := f.owner.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	var allowed bool
	if err = tx.QueryRow(f.ctx, `SELECT current_database()='household_test' AND current_user='household_owner'`).Scan(&allowed); err != nil || !allowed {
		t.Fatal("historical clocks require isolated owner fixture", err)
	}
	if _, err = tx.Exec(f.ctx, `ALTER TABLE expenses.notification_payment_reminders DISABLE TRIGGER notification_payment_guard`); err != nil {
		t.Fatal(err)
	}
	tag, err := tx.Exec(f.ctx, `UPDATE expenses.notification_payment_reminders SET created_at=$3::timestamptz-interval '1 minute',updated_at=$3::timestamptz,
 confirmed_at=$3::timestamptz,responsible_since=$3::timestamptz WHERE household_id=$1 AND id=$2 AND state='confirmed'`, f.identity.HouseholdID, entry, before)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatal("existing confirmed reminder historical clocks", err)
	}
	if _, err = tx.Exec(f.ctx, `ALTER TABLE expenses.notification_payment_reminders ENABLE TRIGGER notification_payment_guard`); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`UPDATE expenses.entries SET updated_at=$3::timestamptz WHERE household_id=$1 AND id=$2`,
		`UPDATE core.module_states SET changed_at=$3::timestamptz WHERE household_id=$1 AND module_id='expenses' AND $2::uuid IS NOT NULL`,
		`UPDATE core.notification_financial_epochs SET updated_at=$3::timestamptz WHERE household_id=$1 AND $2::uuid IS NOT NULL`,
		`UPDATE core.notification_preferences SET enabled_since=$3::timestamptz WHERE household_id=$1 AND category='expenses' AND $2::uuid IS NOT NULL`,
		`UPDATE core.notification_device_preferences SET enabled_since=$3::timestamptz WHERE household_id=$1 AND category='expenses' AND $2::uuid IS NOT NULL`,
	} {
		if _, err = tx.Exec(f.ctx, stmt, f.identity.HouseholdID, entry, before); err != nil {
			t.Fatal("isolated historical notification clock", err)
		}
	}
	tag, err = tx.Exec(f.ctx, `UPDATE core.finance_origin_resources o SET row_hash=sha256(convert_to(to_jsonb(e)::text,'UTF8'))
 FROM expenses.entries e WHERE o.household_id=$1 AND o.resource_type='entries' AND o.resource_id=$2 AND o.retired_at IS NULL
 AND e.household_id=o.household_id AND e.id=o.resource_id AND e.version=o.resource_version`, f.identity.HouseholdID, entry)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatal("preserve authenticated historical entry image", err)
	}
	var cipher secretbox.Stored
	if err = tx.QueryRow(f.ctx, `SELECT encrypted_subscription,encryption_format,encryption_key_id FROM core.push_subscriptions WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, subscription).Scan(&cipher.Ciphertext, &cipher.Format, &cipher.KeyID); err != nil {
		t.Fatal(err)
	}
	binding := secretbox.Binding{Purpose: "push-subscription", HouseholdID: f.identity.HouseholdID, UserID: f.identity.UserID, RecordID: subscription}
	raw, err := f.app.Auth.OpenRecord(binding, cipher)
	if err != nil {
		t.Fatal("open synthetic registration")
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil {
		t.Fatal("invalid synthetic registration")
	}
	for key, instant := range map[string]time.Time{"registered_at": before, "consent_at": before, "expires_at": before.Add(30 * 24 * time.Hour)} {
		encoded, encodeErr := json.Marshal(instant)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		envelope[key] = encoded
	}
	raw, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := f.app.Auth.SealRecord(binding, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(f.ctx, `UPDATE core.push_subscriptions SET encrypted_subscription=$3,created_at=$4,encryption_format=$5,encryption_key_id=$6 WHERE household_id=$1 AND id=$2`,
		f.identity.HouseholdID, subscription, sealed.Ciphertext, before, sealed.Format, sealed.KeyID); err != nil {
		t.Fatal(err)
	}
	var coherent bool
	if err = tx.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='expenses.notification_payment_reminders'::regclass
 AND tgname='notification_payment_guard' AND tgenabled='O') AND EXISTS(SELECT 1 FROM core.finance_origin_resources o JOIN expenses.entries e
 ON e.household_id=o.household_id AND e.id=o.resource_id WHERE o.household_id=$1 AND o.resource_type='entries' AND o.resource_id=$2
 AND o.resource_version=e.version AND o.row_hash=sha256(convert_to(to_jsonb(e)::text,'UTF8')))`, f.identity.HouseholdID, entry).Scan(&coherent); err != nil || !coherent {
		t.Fatal("historical fixture did not restore exact guard and source integrity", err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationFinancePaymentBeforeSend(t *testing.T) {
	f := newIntegrationFixture(t)
	service, _ := shoppingPushService(t, f, true)
	var calls atomic.Int32
	if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("paid reminder must not reach external transport")
	})); err != nil {
		t.Fatal(err)
	}
	var now time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	// Keep the real09:00 stage one to two hours old, avoiding a near-midnight
	// freshness race regardless of the machine's wall-clock hour.
	offset := 10 - now.UTC().Hour()
	if offset < -12 {
		offset += 24
	}
	zone := fmt.Sprintf("Etc/GMT%+d", -offset)
	if offset == 0 {
		zone = "UTC"
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, `UPDATE core.households SET timezone=$2 WHERE id=$1`, f.identity.HouseholdID, zone); err != nil {
		t.Fatal(err)
	}
	local := now.In(location)
	event := time.Date(local.Year(), local.Month(), local.Day(), 9, 0, 0, 0, location)
	if _, err := f.app.Store.SaveNotificationPreference(f.ctx, f.identity, store.NotificationPreference{Category: "expenses", Enabled: true, QuietHours: notification.QuietHours{Timezone: "UTC"}}); err != nil {
		t.Fatal(err)
	}
	connection, err := service.Register(f.ctx, f.identity, shoppingPushInput(t, service))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.SaveDevicePreference(f.ctx, f.identity, connection.ID, webpush.DevicePreference{Category: "expenses", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	category, _ := auditCreate(t, f, "expenses", "categories", store.Record{"name": "PRIVATE-FINANCE dated invoice"})
	entry, account := value.UUID(), value.UUID()
	accountResult := notificationFinanceWebApply(t, f, "accounts", "save", account, 0, store.Record{
		"name": "PRIVATE-FINANCE payment account", "account_type": "bank", "currency": "EUR", "exponent": 2, "notes": "synthetic only"})
	input := notificationFinanceWebEntry(t, f, store.String(category["id"]), "1250")
	draft := notificationFinanceWebApply(t, f, "entries", "save", entry, 0, input)
	view, err := f.app.Store.FinancePaymentReminder(f.ctx, f.identity, entry)
	if err != nil {
		t.Fatal(err)
	}
	in := store.FinancePaymentReminderInput{Action: "save", Generation: store.Int(view["generation"]), EntryVersion: store.Int(view["entry_version"]),
		AccessVersion: store.String(view["access_version"]), DueOn: event.Format(time.DateOnly), ResponsibleMemberID: f.identity.MembershipID, RequestID: value.UUID()}
	if _, err = f.app.Store.SaveFinancePaymentReminder(f.ctx, f.identity, entry, in); err != nil {
		t.Fatal(err)
	}
	in.Action, in.Version, in.RequestID = "confirm", 1, value.UUID()
	if _, err = f.app.Store.SaveFinancePaymentReminder(f.ctx, f.identity, entry, in); err != nil {
		t.Fatal(err)
	}
	notificationFinanceWebHistoricalDate(t, f, entry, connection.ID, event.Add(-time.Hour))
	notificationFinanceWebScan(t, f, service)
	if err = service.ScheduleNotifications(f.ctx); err != nil {
		t.Fatal(err)
	}
	var delivery string
	if err = f.pool.QueryRow(f.ctx, `SELECT d.id::text FROM core.notification_deliveries d JOIN core.notification_intents i ON i.id=d.intent_id
 WHERE i.resource_type='payment_reminders' AND i.resource_id=$1 AND d.subscription_id=$2 AND d.state='pending'`, entry, connection.ID).Scan(&delivery); err != nil {
		t.Fatal("real confirmed date source did not stage delivery", err)
	}
	input.Payment = &store.FinancePaymentInput{AccountID: account, AccountVersion: store.Int(accountResult["saved_version"]), NativeMinor: "1250"}
	input.Lines[0].Version = 1
	input.Baseline = []expenseedit.LineVersion{{ID: input.Lines[0].ID, Version: 1}}
	notificationFinanceWebApply(t, f, "entries", "post", entry, store.Int(draft["saved_version"]), input)
	view, err = f.app.Store.FinancePaymentReminder(f.ctx, f.identity, entry)
	if err != nil || view["verified_paid"] != true {
		t.Fatal("actual same-entry movement not recognized as payment", err)
	}
	job, lease := notificationClaimFixture(t, f, delivery)
	if err = service.SendNotificationJob(f.ctx, job, lease); err != nil {
		t.Fatal(err)
	}
	var cancelled bool
	if err = f.pool.QueryRow(f.ctx, `SELECT state='cancelled' FROM core.notification_deliveries WHERE id=$1`, delivery).Scan(&cancelled); err != nil || !cancelled || calls.Load() != 0 {
		t.Fatal("paid reminder was not cancelled before transport", cancelled, calls.Load(), err)
	}
}
