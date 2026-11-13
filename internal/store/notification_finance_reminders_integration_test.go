//go:build integration

package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/expenseedit"
	"household/internal/value"
)

func notificationFinanceReminderInput(t *testing.T, f *recipeExpansionFixture, entry, action, member string) FinancePaymentReminderInput {
	t.Helper()
	view, err := f.store.FinancePaymentReminder(f.ctx, f.identity, entry)
	if err != nil {
		t.Fatal("read reminder", err)
	}
	in := FinancePaymentReminderInput{Action: action, EntryVersion: Int(view["entry_version"]), Generation: Int(view["generation"]),
		AccessVersion: String(view["access_version"]), DueOn: time.Now().UTC().AddDate(0, 0, 10).Format(time.DateOnly), ResponsibleMemberID: member, RequestID: value.UUID()}
	if reminder, ok := view["reminder"].(*financePaymentReminder); ok && reminder != nil {
		in.Version, in.DueOn, in.ResponsibleMemberID = reminder.Version, reminder.DueOn, reminder.ResponsibleMemberID
	}
	return in
}

func notificationFinanceReminderSave(t *testing.T, f *recipeExpansionFixture, entry string, in FinancePaymentReminderInput) Record {
	t.Helper()
	out, err := f.store.SaveFinancePaymentReminder(f.ctx, f.identity, entry, in)
	if err != nil {
		t.Fatal("write reminder", in.Action, err)
	}
	return out
}

func notificationFinanceReminderSource(t *testing.T, f *recipeExpansionFixture, actor auth.Identity, entry string) financePaymentReminderSource {
	t.Helper()
	tx, err := f.app.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollback(tx)
	source, err := readFinancePaymentReminderSource(f.ctx, tx, actor, entry)
	if err != nil {
		t.Fatal("read current reminder authority", err)
	}
	return source
}

func TestNotificationFinancePaymentReminderLifecycleAndAuthority(t *testing.T) {
	f := newRecipeExpansionFixture(t)
	category := financeNativeCategory(t, f)
	reader := financeAuthorityMember(t, f, "reader")
	newEntry := func(t *testing.T, post bool) (string, FinanceEntryInput) {
		t.Helper()
		entry, input := value.UUID(), financeNativeEntry(category, "1250")
		action := "save"
		if post {
			action = "post"
		}
		financeNativeApply(t, f, "entries", action, entry, 0, input)
		return entry, input
	}
	t.Run("explicit draft confirmation completion and durable current replay", func(t *testing.T) {
		entry, _ := newEntry(t, true)
		in := notificationFinanceReminderInput(t, f, entry, "save", reader.MembershipID)
		out := notificationFinanceReminderSave(t, f, entry, in)
		first := out["reminder"].(*financePaymentReminder)
		if first.State != "draft" || first.ConfirmedAt != nil || out["verified_paid"] != false || out["can_confirm"] != true {
			t.Fatal("draft or payment-less posted event was misclassified")
		}
		replay := notificationFinanceReminderSave(t, f, entry, in)["reminder"].(*financePaymentReminder)
		if replay.Version != first.Version {
			t.Fatal("replay mutated reminder")
		}
		changed := in
		changed.DueOn = time.Now().UTC().AddDate(0, 0, 11).Format(time.DateOnly)
		if _, err := f.store.SaveFinancePaymentReminder(f.ctx, f.identity, entry, changed); !errors.Is(err, ErrConflict) {
			t.Fatal("idempotency key reused for different content", err)
		}
		confirm := notificationFinanceReminderInput(t, f, entry, "confirm", reader.MembershipID)
		notificationFinanceReminderSave(t, f, entry, confirm)
		source := notificationFinanceReminderSource(t, f, reader, entry)
		if !source.Eligible || source.CanEdit || source.VerifiedPaid {
			t.Fatal("authorized reader recipient not eligible or gained edit access")
		}
		if notificationFinanceReminderSource(t, f, f.identity, entry).Eligible {
			t.Fatal("creator became implicit recipient")
		}
		if _, err := f.store.SaveFinancePaymentReminder(f.ctx, reader, entry, confirm); !errors.Is(err, ErrForbidden) {
			t.Fatal("reader mutated financial reminder", err)
		}
		notificationFinanceReminderSave(t, f, entry, notificationFinanceReminderInput(t, f, entry, "complete", reader.MembershipID))
		out = notificationFinanceReminderSave(t, f, entry, in)
		if out["reminder"].(*financePaymentReminder).State != "completed" || out["verified_paid"] != false || out["can_confirm"] != false {
			t.Fatal("old replay resurrected draft or completion fabricated payment")
		}
		if notificationFinanceReminderSource(t, f, reader, entry).Eligible {
			t.Fatal("completed reminder remained eligible")
		}
	})
	t.Run("source edit requires renewed explicit confirmation", func(t *testing.T) {
		entry, input := newEntry(t, false)
		notificationFinanceReminderSave(t, f, entry, notificationFinanceReminderInput(t, f, entry, "save", f.identity.MembershipID))
		notificationFinanceReminderSave(t, f, entry, notificationFinanceReminderInput(t, f, entry, "confirm", f.identity.MembershipID))
		source := notificationFinanceReminderSource(t, f, f.identity, entry)
		input.Lines[0].Version = 1
		input.Baseline = []expenseedit.LineVersion{{ID: input.Lines[0].ID, Version: 1}}
		input.Description = "Explicitly revised invoice context"
		financeNativeApply(t, f, "entries", "save", entry, source.EntryVersion, input)
		source = notificationFinanceReminderSource(t, f, f.identity, entry)
		if !source.NeedsReconfirmation || source.Eligible {
			t.Fatal("old confirmation survived entry revision")
		}
		notificationFinanceReminderSave(t, f, entry, notificationFinanceReminderInput(t, f, entry, "confirm", f.identity.MembershipID))
		if !notificationFinanceReminderSource(t, f, f.identity, entry).Eligible {
			t.Fatal("explicit source reconfirmation did not restore eligibility")
		}
	})
	t.Run("verified same-entry payment suppresses reminder without fabricated ledger effects", func(t *testing.T) {
		account, version := financeNativeAccount(t, f, "EUR", 2)
		entry, input := value.UUID(), financeNativeEntry(category, "1250")
		input.Payment = &FinancePaymentInput{AccountID: account, AccountVersion: version, NativeMinor: "1250"}
		financeNativeApply(t, f, "entries", "post", entry, 0, input)
		notificationFinanceReminderSave(t, f, entry, notificationFinanceReminderInput(t, f, entry, "save", f.identity.MembershipID))
		source := notificationFinanceReminderSource(t, f, f.identity, entry)
		if !source.VerifiedPaid || source.Eligible {
			t.Fatal("real payment was not recognized")
		}
		view, err := f.store.FinancePaymentReminder(f.ctx, f.identity, entry)
		if err != nil || view["can_confirm"] != false {
			t.Fatal("paid entry exposed a confirmation action", err)
		}
		if _, err := f.store.SaveFinancePaymentReminder(f.ctx, f.identity, entry, notificationFinanceReminderInput(t, f, entry, "confirm", f.identity.MembershipID)); err == nil {
			t.Fatal("paid entry confirmation accepted")
		}
		var count int
		if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM expenses.cash_events WHERE household_id=$1 AND entry_id=$2`, f.identity.HouseholdID, entry).Scan(&count); err != nil || count != 1 {
			t.Fatal("reminder changed payment ledger", count, err)
		}
	})
	t.Run("reversed entry draft does not expose confirmation", func(t *testing.T) {
		entry, input := newEntry(t, true)
		notificationFinanceReminderSave(t, f, entry, notificationFinanceReminderInput(t, f, entry, "save", f.identity.MembershipID))
		source := notificationFinanceReminderSource(t, f, f.identity, entry)
		financeNativeApply(t, f, "entries", "reverse", value.UUID(), 0, Record{"original_entry_id": entry,
			"original_version": source.EntryVersion, "occurred_on": input.OccurredOn, "reason": "Explicit reversal before reminder confirmation"})
		view, err := f.store.FinancePaymentReminder(f.ctx, f.identity, entry)
		if err != nil || view["can_confirm"] != false {
			t.Fatal("reversed entry exposed a confirmation action", err)
		}
		if _, err = f.store.SaveFinancePaymentReminder(f.ctx, f.identity, entry, notificationFinanceReminderInput(t, f, entry, "confirm", f.identity.MembershipID)); err == nil {
			t.Fatal("reversed entry accepted explicit reminder confirmation")
		}
	})
	t.Run("private account authority is inherited and cannot be granted by assignment", func(t *testing.T) {
		account := financeAuthorityInitialAccount(t, f, f.identity, []FinanceInitialGrant{{MembershipID: f.identity.MembershipID, Permission: "edit"}})
		entry, input := value.UUID(), financeNativeEntry(category, "1250")
		input.Payment = &FinancePaymentInput{AccountID: account, AccountVersion: 1, NativeMinor: "1250"}
		financeNativeApply(t, f, "entries", "save", entry, 0, input)
		if _, err := f.store.FinancePaymentReminder(f.ctx, reader, entry); !errors.Is(err, ErrNotFound) {
			t.Fatal("private account entry reminder disclosed", err)
		}
		in := notificationFinanceReminderInput(t, f, entry, "save", reader.MembershipID)
		if _, err := f.store.SaveFinancePaymentReminder(f.ctx, f.identity, entry, in); err == nil {
			t.Fatal("reminder assignment granted private account visibility")
		}
		var count int
		if err := f.owner.QueryRow(f.ctx, `SELECT count(*) FROM expenses.notification_payment_reminders WHERE household_id=$1 AND id=$2`, f.identity.HouseholdID, entry).Scan(&count); err != nil || count != 0 {
			t.Fatal("failed assignment did not roll back", count, err)
		}
		other := financeAuthorityMember(t, f, "editor")
		barrier, err := f.owner.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rollback(barrier)
		if _, err = barrier.Exec(f.ctx, `SELECT id FROM expenses.entries WHERE household_id=$1 AND id=$2 FOR UPDATE`, f.identity.HouseholdID, entry); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(f.ctx, time.Second)
		defer cancel()
		if _, err = f.store.SaveFinancePaymentReminder(ctx, other, entry, in); !errors.Is(err, ErrNotFound) {
			t.Fatal("unauthorized editor waited on a private entry row lock instead of rejecting first", err)
		}
	})
	t.Run("assignment does not require recipient login and cancellation fences future delivery", func(t *testing.T) {
		recipient := financeAuthorityMember(t, f, "reader")
		if _, err := f.owner.Exec(f.ctx, `UPDATE core.sessions SET revoked_at=clock_timestamp() WHERE id=$1`, recipient.SessionID); err != nil {
			t.Fatal(err)
		}
		entry, _ := newEntry(t, true)
		notificationFinanceReminderSave(t, f, entry, notificationFinanceReminderInput(t, f, entry, "save", recipient.MembershipID))
		notificationFinanceReminderSave(t, f, entry, notificationFinanceReminderInput(t, f, entry, "confirm", recipient.MembershipID))
		out := notificationFinanceReminderSave(t, f, entry, notificationFinanceReminderInput(t, f, entry, "cancel", recipient.MembershipID))
		if out["reminder"].(*financePaymentReminder).State != "cancelled" || out["verified_paid"] != false {
			t.Fatal("cancelled reminder changed paid status")
		}
	})
	t.Run("concurrent saved revisions have one winner and one conflict", func(t *testing.T) {
		entry, _ := newEntry(t, true)
		notificationFinanceReminderSave(t, f, entry, notificationFinanceReminderInput(t, f, entry, "save", f.identity.MembershipID))
		first := notificationFinanceReminderInput(t, f, entry, "save", f.identity.MembershipID)
		second := first
		second.RequestID = value.UUID()
		second.DueOn = time.Now().UTC().AddDate(0, 0, 12).Format(time.DateOnly)
		start := make(chan struct{})
		results := make(chan error, 2)
		var workers sync.WaitGroup
		for _, in := range []FinancePaymentReminderInput{first, second} {
			workers.Go(func() {
				<-start
				_, err := f.store.SaveFinancePaymentReminder(f.ctx, f.identity, entry, in)
				results <- err
			})
		}
		close(start)
		workers.Wait()
		close(results)
		accepted, conflicts := 0, 0
		for err := range results {
			if err == nil {
				accepted++
			} else if errors.Is(err, ErrConflict) {
				conflicts++
			} else {
				t.Fatal("unexpected concurrent reminder result", err)
			}
		}
		if accepted != 1 || conflicts != 1 {
			t.Fatal("lost update was not rejected", accepted, conflicts)
		}
	})
}
