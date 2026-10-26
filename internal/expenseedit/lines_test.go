package expenseedit

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"household/internal/value"
)

const entryFixture = "60000000-0000-4000-8000-000000000001"

func TestLineReconciliationDetectsChildChangesWithoutParentVersion(t *testing.T) {
	t.Parallel()
	baseline := []LineVersion{{ID: categoryFixture, Version: 2}}
	for _, tc := range []struct {
		name    string
		current []LineVersion
	}{
		{"edited child", []LineVersion{{ID: categoryFixture, Version: 3}}},
		{"unseen child", []LineVersion{{ID: categoryFixture, Version: 2}, {ID: memberFixture, Version: 1}}},
		{"deleted child", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ReconcileLines(Draft{EntryID: entryFixture}, baseline, baseline, tc.current)
			if !errors.Is(err, ErrStaleLines) || !reflect.DeepEqual(got, LineChanges{}) {
				t.Fatal("stale complete child set produced usable replacement", got, err)
			}
		})
	}
}

func TestLineReconciliationRequiresDeliberateRemovalAndPreservesSignedRows(t *testing.T) {
	t.Parallel()
	baseline := []LineVersion{{ID: categoryFixture, Version: 3}, {ID: memberFixture, Version: 2}}
	removed := []LineVersion{{ID: strings.ToUpper(memberFixture), Version: 2}}
	draft := Draft{EntryID: entryFixture, Lines: []Line{
		{Key: value.UUID(), ID: categoryFixture, Version: 3, AmountMinor: -1, Position: 0, Note: "Deliberate discount"},
		{Key: value.UUID(), AmountMinor: 900719925474, Position: 1},
	}}
	before := append([]Line(nil), draft.Lines...)
	got, err := ReconcileLines(draft, baseline, removed, baseline)
	if err != nil || len(got.Save) != 2 || got.Save[0].AmountMinor != -1 || got.Save[1].AmountMinor != 900719925474 ||
		!reflect.DeepEqual(got.Delete, []LineVersion{{ID: memberFixture, Version: 2}}) || !reflect.DeepEqual(draft.Lines, before) {
		t.Fatal("replacement lost exact signed values, identity or ownership", got, err)
	}
	got.Save[0].Note = "Changed owned result"
	if draft.Lines[0].Note != "Deliberate discount" {
		t.Fatal("reconciliation aliases caller rows")
	}
	if got, err = ReconcileLines(draft, baseline, nil, baseline); err == nil || !reflect.DeepEqual(got, LineChanges{}) {
		t.Fatal("omitted saved child was silently deleted", got, err)
	}
}

func TestLineReconciliationRejectsAmbiguousIdentityAndIntent(t *testing.T) {
	t.Parallel()
	baseline := []LineVersion{{ID: categoryFixture, Version: 1}}
	key := value.UUID()
	for _, tc := range []struct {
		name    string
		lines   []Line
		removed []LineVersion
	}{
		{"retained and removed", []Line{{Key: key, ID: categoryFixture, Version: 1}}, baseline},
		{"invented stored child", []Line{{Key: key, ID: memberFixture, Version: 1}}, baseline},
		{"stale retained version", []Line{{Key: key, ID: categoryFixture, Version: 2}}, nil},
		{"new row with stored version", []Line{{Key: key, Version: 1}}, baseline},
		{"duplicate stable key", []Line{{Key: key}, {Key: key, Position: 1}}, baseline},
		{"duplicate stored child", []Line{{Key: key, ID: categoryFixture, Version: 1}, {Key: value.UUID(), ID: categoryFixture, Version: 1, Position: 1}}, nil},
		{"implicit reorder", []Line{{Key: key, ID: categoryFixture, Version: 1, Position: 1}}, nil},
		{"duplicate removal", nil, []LineVersion{{ID: categoryFixture, Version: 1}, {ID: strings.ToUpper(categoryFixture), Version: 1}}},
		{"invented removal", nil, []LineVersion{{ID: memberFixture, Version: 1}}},
		{"stale removal", nil, []LineVersion{{ID: categoryFixture, Version: 2}}},
		{"zero removal version", nil, []LineVersion{{ID: categoryFixture}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ReconcileLines(Draft{EntryID: entryFixture, Lines: tc.lines}, baseline, tc.removed, baseline)
			if err == nil || !reflect.DeepEqual(got, LineChanges{}) {
				t.Fatal("ambiguous replacement was usable", got, err)
			}
		})
	}
}

func TestLineReconciliationBoundsWholeReadSetAndDeterministicRemoval(t *testing.T) {
	t.Parallel()
	baseline := make([]LineVersion, LineLimit)
	for index := range baseline {
		baseline[index] = LineVersion{ID: value.UUID(), Version: 1}
	}
	got, err := ReconcileLines(Draft{EntryID: entryFixture}, baseline, baseline, baseline)
	if err != nil || len(got.Delete) != LineLimit || got.Save == nil {
		t.Fatal("explicit full bounded removal rejected", got, err)
	}
	for index := 1; index < len(got.Delete); index++ {
		if got.Delete[index-1].ID >= got.Delete[index].ID {
			t.Fatal("removals do not have deterministic lock order")
		}
	}
	oversized := append(append([]LineVersion{}, baseline...), LineVersion{ID: value.UUID(), Version: 1})
	for _, tc := range []struct {
		name                       string
		baseline, removed, current []LineVersion
	}{
		{"oversized baseline", oversized, baseline, baseline},
		{"oversized removal", baseline, oversized, baseline},
		{"oversized current", baseline, baseline, oversized},
		{"duplicate baseline", append(baseline[:99:99], baseline[0]), baseline, baseline},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ReconcileLines(Draft{EntryID: entryFixture}, tc.baseline, tc.removed, tc.current)
			if err == nil || !reflect.DeepEqual(got, LineChanges{}) {
				t.Fatal("oversized/ambiguous saved set was silently truncated", got, err)
			}
		})
	}
}

func TestLineReconciliationNewDraftCannotClaimSavedChildren(t *testing.T) {
	t.Parallel()
	baseline := []LineVersion{{ID: categoryFixture, Version: 1}}
	if got, err := ReconcileLines(Draft{Lines: []Line{{Key: value.UUID(), AmountMinor: 1}}}, nil, nil, nil); err != nil || len(got.Save) != 1 || got.Delete == nil {
		t.Fatal("fresh bounded draft should be usable", got, err)
	}
	for _, tc := range []struct {
		name                       string
		baseline, removed, current []LineVersion
	}{
		{"baseline", baseline, nil, nil}, {"removal", nil, baseline, nil}, {"current", nil, nil, baseline},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := ReconcileLines(Draft{}, tc.baseline, tc.removed, tc.current); err == nil || !reflect.DeepEqual(got, LineChanges{}) {
				t.Fatal("new entry claimed stored child identities", got, err)
			}
		})
	}
	if _, err := ReconcileLines(Draft{Lines: []Line{{Key: value.UUID(), ID: categoryFixture, Version: 1}}}, nil, nil, nil); err == nil {
		t.Fatal("new entry kept a saved row")
	}
}
