package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

const shoppingUnitID = "01234567-89ab-4cde-8fab-0123456789ab"
const shoppingUnitList = "11111111-2222-4333-8444-555555555555"

func TestShoppingExtensionsRevisionClosedBoundaries(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{{"0", true}, {"1", true}, {strconv.FormatUint(math.MaxUint64, 10), true}, {"", false}, {"00", false}, {"01", false}, {"-1", false}, {"+1", false}, {" 1", false}, {"1.0", false}, {"18446744073709551616", false}, {"١", false}} {
		t.Run(tc.in, func(t *testing.T) {
			_, err := shoppingRevision(tc.in)
			if (err == nil) != tc.ok {
				t.Fatalf("revision %q: %v", tc.in, err)
			}
		})
	}
}

func shoppingUnitSettings() shoppingNotifySettings {
	return shoppingNotifySettings{Schema: 1, Generation: 1, Users: map[string]shoppingNotifyUser{shoppingUnitID: {Revision: "1", Watches: map[string]ShoppingPreference{shoppingUnitList: {Enabled: true, EnabledSince: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}}}}}
}

func TestShoppingExtensionsSettingsClosedShapeAndQuotas(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*shoppingNotifySettings)
	}{
		{"schema", func(s *shoppingNotifySettings) { s.Schema = 2 }},
		{"generation", func(s *shoppingNotifySettings) { s.Generation = 2 }},
		{"nil users", func(s *shoppingNotifySettings) { s.Users = nil }},
		{"stale zero fence", func(s *shoppingNotifySettings) {
			u := s.Users[shoppingUnitID]
			u.Revision = "0"
			s.Users[shoppingUnitID] = u
		}},
		{"push without consent", func(s *shoppingNotifySettings) {
			u := s.Users[shoppingUnitID]
			w := u.Watches[shoppingUnitList]
			w.PushEnabled = true
			u.Watches[shoppingUnitList] = w
		}},
		{"disabled persisted watch", func(s *shoppingNotifySettings) {
			u := s.Users[shoppingUnitID]
			w := u.Watches[shoppingUnitList]
			w.Enabled = false
			u.Watches[shoppingUnitList] = w
		}},
		{"uppercase UUID", func(s *shoppingNotifySettings) {
			s.Users[strings.ToUpper(shoppingUnitID)] = s.Users[shoppingUnitID]
			delete(s.Users, shoppingUnitID)
		}},
		{"twenty one", func(s *shoppingNotifySettings) {
			u := s.Users[shoppingUnitID]
			for i := 1; i <= 21; i++ {
				lid := "11111111-2222-4333-8444-" + leftPadShopping(i)
				u.Watches[lid] = ShoppingPreference{Enabled: true, EnabledSince: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := shoppingUnitSettings()
			tc.change(&s)
			if shoppingValidateSettings(s, 1) == nil {
				t.Fatal("invalid settings accepted")
			}
		})
	}
	good := shoppingUnitSettings()
	if err := shoppingValidateSettings(good, 1); err != nil {
		t.Fatal(err)
	}
	u := good.Users[shoppingUnitID]
	u.Watches = map[string]ShoppingPreference{}
	good.Users[shoppingUnitID] = u
	if err := shoppingValidateSettings(good, 1); err != nil {
		t.Fatal("persistent empty revision fence rejected", err)
	}
	for _, raw := range []string{`{"schema":1,"generation":1,"users":{},"other":true}`, `{"schema":1,"generation":1,"users":{}} {}`, `null`} {
		// Each production read decodes into a fresh value. JSON null is a no-op
		// for a reused struct and must not inherit a preceding malformed case.
		var decoded shoppingNotifySettings
		err := shoppingDecodeClosed([]byte(raw), &decoded)
		if err == nil {
			err = shoppingValidateSettings(decoded, 1)
		}
		if err == nil {
			t.Fatalf("closed settings accepted %s", raw)
		}
	}
}

func leftPadShopping(n int) string { v := strconv.Itoa(n); return strings.Repeat("0", 12-len(v)) + v }

func TestShoppingExtensionsProducerMoveAndNoopFraming(t *testing.T) {
	old := Record{"id": shoppingUnitID, "list_id": shoppingUnitList, "name": "<Tea>", "checked_at": nil, "version": json.Number("1")}
	copy := Record{}
	for k, v := range old {
		copy[k] = v
	}
	copy["version"] = json.Number("2")
	copy["updated_at"] = "later"
	ctx := shoppingWithBefore(context.Background(), "shopping", "items", old)
	raw, v, err := shoppingEventPayload(ctx, "shopping", "items", copy)
	if err != nil || v != 1 {
		t.Fatal("noop content event", v, err)
	}
	var legacy map[string]any
	if json.Unmarshal(raw, &legacy) != nil || len(legacy) != 2 || legacy["entity"] != "items" {
		t.Fatal("legacy shape changed", string(raw))
	}
	copy["list_id"] = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	raw, v, err = shoppingEventPayload(ctx, "shopping", "items", copy)
	var event shoppingChangeEvent
	if err != nil || v != 2 || json.Unmarshal(raw, &event) != nil || !shoppingValidChange(event) || len(event.Shopping.ListIDs) != 2 || event.Shopping.ListIDs[0] != shoppingUnitList {
		t.Fatal("move lost actual prior parent", string(raw), err)
	}
	if strings.Contains(string(raw), "Tea") {
		t.Fatal("content disclosed into event")
	}
	copy["list_id"] = shoppingUnitList
	copy["checked_at"] = "2026-10-29T12:00:00Z"
	_, v, err = shoppingEventPayload(ctx, "shopping", "items", copy)
	if err != nil || v != 2 {
		t.Fatal("check transition omitted")
	}
	_, v, err = shoppingEventPayload(context.Background(), "pantry", "items", copy)
	if err != nil || v != 1 {
		t.Fatal("foreign producer changed")
	}
}

func TestShoppingExtensionsSnapshotFingerprintAndExactWire(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	quantity := "9007199254.740993"
	out := ShoppingSnapshot{Schema: 1, Context: ShoppingExtensionContext{Generation: 1}, CapturedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour), Complete: true,
		Lists: []ShoppingSnapshotList{{ID: shoppingUnitList, Version: "9007199254740993", Name: "<>&\u2028Ä", Items: []ShoppingSnapshotItem{{ID: shoppingUnitID, Version: "1", Name: "Tea", Quantity: &quantity}}}}, Totals: ShoppingSnapshotTotals{Lists: 1, Items: 1}}
	if err := shoppingSealSnapshot(&out); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if len(raw) != out.Totals.UTF8Bytes || !strings.Contains(string(raw), `"quantity":"9007199254.740993"`) || !strings.Contains(string(raw), `\u003c`) {
		t.Fatal("wire precision/byte framing changed")
	}
	fingerprint := out.Fingerprint
	out.Fingerprint = ""
	out.Totals.UTF8Bytes = 0
	canonical, _ := json.Marshal(out)
	hash := sha256.Sum256(canonical)
	if fingerprint != hex.EncodeToString(hash[:]) {
		t.Fatal("fingerprint input includes excluded metadata")
	}
	out.Lists[0].Items[0].Note = strings.Repeat("Ä", shoppingSnapshotBytes)
	if shoppingSealSnapshot(&out) == nil {
		t.Fatal("oversized complete envelope accepted")
	}
}

func TestShoppingExtensionsSnapshotSelectionAndNoticeKeyBoundaries(t *testing.T) {
	for _, in := range []ShoppingSnapshotInput{{Generation: 1}, {Generation: 0, ListIDs: []string{shoppingUnitList}}, {Generation: 1, ListIDs: []string{shoppingUnitList, strings.ToUpper(shoppingUnitList)}}, {Generation: 1, ListIDs: []string{"foreign"}}} {
		if _, err := shoppingSnapshotIDs(in); err == nil {
			t.Fatal("invalid selection accepted", in)
		}
	}
	ref := ShoppingNotificationReference{HouseholdID: shoppingUnitID, RecipientID: shoppingUnitID, ListID: shoppingUnitList, Generation: 1, Bucket: 100}
	key := shoppingNoticeKey(ref)
	parsed, err := shoppingParseNoticeKey(key)
	if err != nil || parsed.ListID != ref.ListID {
		t.Fatal(key, err)
	}
	for _, bad := range []string{key + "/extra", strings.Replace(key, "/1/", "/01/", 1), strings.Replace(key, "changed", "unknown", 1), strings.Replace(key, shoppingUnitID, strings.ToUpper(shoppingUnitID), 1), "https://foreign/" + key} {
		if _, err = shoppingParseNoticeKey(bad); err == nil {
			t.Fatal("malformed key accepted", bad)
		}
	}
}

func TestShoppingExtensionsExactEncodedByteBoundaryAndGlobalQuota(t *testing.T) {
	out := ShoppingSnapshot{Schema: 1, Complete: true, Lists: []ShoppingSnapshotList{{ID: shoppingUnitList, Version: "1", Name: "€", Items: []ShoppingSnapshotItem{{ID: shoppingUnitID, Version: "1"}}}}, Totals: ShoppingSnapshotTotals{Lists: 1, Items: 1}}
	if err := shoppingSealSnapshot(&out); err != nil {
		t.Fatal(err)
	}
	out.Totals.UTF8Bytes = shoppingSnapshotBytes
	raw, _ := json.Marshal(out)
	out.Lists[0].Items[0].Note = strings.Repeat("x", shoppingSnapshotBytes-len(raw))
	if err := shoppingSealSnapshot(&out); err != nil || out.Totals.UTF8Bytes != shoppingSnapshotBytes {
		t.Fatal("exact2MiB rejected", out.Totals, err)
	}
	out.Lists[0].Items[0].Note += "x"
	if shoppingSealSnapshot(&out) == nil {
		t.Fatal("2MiB+1 accepted")
	}
	s := shoppingNotifySettings{Schema: 1, Generation: 1, Users: map[string]shoppingNotifyUser{}}
	for i := 0; i < 256; i++ {
		uid := "aaaaaaaa-bbbb-4ccc-8ddd-" + leftPadShopping(i/20+1)
		u, ok := s.Users[uid]
		if !ok {
			u = shoppingNotifyUser{Revision: "1", Watches: map[string]ShoppingPreference{}}
		}
		lid := "11111111-2222-4333-8444-" + leftPadShopping(i%20+1)
		u.Watches[lid] = ShoppingPreference{Enabled: true, EnabledSince: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
		s.Users[uid] = u
	}
	if err := shoppingValidateSettings(s, 1); err != nil {
		t.Fatal("256 global watches", err)
	}
	uid := "aaaaaaaa-bbbb-4ccc-8ddd-" + leftPadShopping(13)
	u := s.Users[uid]
	u.Watches["11111111-2222-4333-8444-"+leftPadShopping(17)] = ShoppingPreference{Enabled: true, EnabledSince: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	if shoppingValidateSettings(s, 1) == nil {
		t.Fatal("257 global watches accepted")
	}
}
