package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"household/internal/auth"
)

func foodFactsInputFixture() PantryFoodFactsInput {
	return PantryFoodFactsInput{Generation: 1, Code: "4006381333931", Provider: "off-production", CacheFingerprint: strings.Repeat("a", 64),
		SelectedFields: []string{"pack_description", "name", "brand"}, ProductName: "Explicit local name", Unit: "g",
		CreatePack: true, PackLabel: "Explicit pack", PackQuantity: "0.5", PackUnit: "kg"}
}

func TestFoodFactsNormalizationKeepsExactQuantitiesAndExplicitSelections(t *testing.T) {
	original := foodFactsInputFixture()
	normalized, err := normalizePantryFoodFacts(original)
	if err != nil || normalized.Code != original.Code || normalized.PackQuantity != "0.500000" || strings.Join(normalized.SelectedFields, ",") != "brand,name,pack_description" {
		t.Fatal(normalized, err)
	}
	if original.SelectedFields[0] != "pack_description" {
		t.Fatal("normalization changed the caller's selection slice")
	}
	for _, mutate := range []func(*PantryFoodFactsInput){
		func(in *PantryFoodFactsInput) { in.Generation = 0 },
		func(in *PantryFoodFactsInput) { in.Code = "4006381333932" },
		func(in *PantryFoodFactsInput) { in.Provider = "https://localhost" },
		func(in *PantryFoodFactsInput) { in.CacheFingerprint = "bad" },
		func(in *PantryFoodFactsInput) { in.SelectedFields = nil },
		func(in *PantryFoodFactsInput) { in.SelectedFields = []string{"name", "name"} },
		func(in *PantryFoodFactsInput) { in.SelectedFields = []string{"nutrition"} },
		func(in *PantryFoodFactsInput) { in.ProductName = "bad\x00name" },
		func(in *PantryFoodFactsInput) { in.ProductName = strings.Repeat("n", 201) },
		func(in *PantryFoodFactsInput) { in.Unit = "cup" },
		func(in *PantryFoodFactsInput) { in.PackQuantity = "0" },
		func(in *PantryFoodFactsInput) { in.PackQuantity = "0.0000001" },
		func(in *PantryFoodFactsInput) { in.PackQuantity = "1e5" },
		func(in *PantryFoodFactsInput) { in.CreatePack = false },
		func(in *PantryFoodFactsInput) {
			in.ProductID = "00000000-0000-4000-8000-000000000001"
			in.ProductVersion = 1
		},
	} {
		input := foodFactsInputFixture()
		mutate(&input)
		if _, err := normalizePantryFoodFacts(input); err == nil {
			t.Fatal("unsupported or silently ignored input accepted", input)
		}
	}
	existing := foodFactsInputFixture()
	existing.ProductID, existing.ProductVersion = "00000000-0000-4000-8000-000000000001", 3
	existing.ProductName, existing.Unit = "", ""
	if _, err := normalizePantryFoodFacts(existing); err != nil {
		t.Fatal("explicit existing product rejected", err)
	}
}

func TestFoodFactsRequestIdentityBindsIntentButNotAnExpiredSession(t *testing.T) {
	in, err := normalizePantryFoodFacts(foodFactsInputFixture())
	if err != nil {
		t.Fatal(err)
	}
	id := auth.Identity{HouseholdID: "household", UserID: "actor", SessionID: "old-session"}
	first, err := foodFactsRequestHash(id, "stable-key", in)
	if err != nil {
		t.Fatal(err)
	}
	id.SessionID = "new-current-session"
	second, _ := foodFactsRequestHash(id, "stable-key", in)
	if !bytes.Equal(first, second) {
		t.Fatal("durable intent cannot be recovered after valid reauthentication")
	}
	for _, change := range []struct {
		identity auth.Identity
		key      string
		input    PantryFoodFactsInput
	}{{auth.Identity{HouseholdID: "foreign", UserID: "actor"}, "stable-key", in}, {id, "other-key", in}} {
		changed, _ := foodFactsRequestHash(change.identity, change.key, change.input)
		if bytes.Equal(first, changed) {
			t.Fatal("operation identity omitted actor/household/key")
		}
	}
	changed := in
	changed.PackQuantity = "0.600000"
	other, _ := foodFactsRequestHash(id, "stable-key", changed)
	if bytes.Equal(first, other) {
		t.Fatal("different reviewed quantity reused an old intent")
	}
}

func foodFactsCacheFixture(t *testing.T) foodFactsCache {
	t.Helper()
	cache := foodFactsCache{Household: "00000000-0000-4000-8000-000000000001", Generation: 1, Provider: "off-production", Code: "4006381333931", State: "found",
		Name: "Product", Brand: "Brand", Pack: "3 x 150 g", Payload: bytes.Repeat([]byte{1}, 32),
		FetchedAt: time.Date(2026, 10, 1, 12, 0, 0, 123000000, time.UTC)}
	cache.ExpiresAt = cache.FetchedAt.Add(7 * 24 * time.Hour)
	var err error
	cache.Fingerprint, err = cache.digest()
	if err != nil || !cache.valid() {
		t.Fatal(cache, err)
	}
	return cache
}

func TestFoodFactsCacheFreshStaleAndErrorStatesNeverInferStockOrQuantity(t *testing.T) {
	cache := foodFactsCacheFixture(t)
	fresh := foodFactsCachedReply(1, cache.Code, &cache, cache.FetchedAt.Add(time.Minute))
	if fresh.State != "found" || fresh.Stale || !fresh.FromCache || fresh.Source == nil || fresh.Source.PackDescription != "3 x 150 g" || fresh.Source.DatabaseLicense != "ODbL-1.0" || fresh.Source.ContentsLicense != "DbCL-1.0" {
		t.Fatal(fresh)
	}
	stale := foodFactsCachedReply(1, cache.Code, &cache, cache.ExpiresAt)
	if stale.State != "stale" || !stale.Stale || stale.Source == nil {
		t.Fatal("expiry boundary mislabelled", stale)
	}
	missing := foodFactsCachedReply(1, cache.Code, nil, cache.FetchedAt)
	if missing.State != "unqueried" || missing.Source != nil || missing.FromCache {
		t.Fatal("absence of consent was represented as an actual upstream miss", missing)
	}
	for _, state := range []string{"not_found", "error", "limited"} {
		entry := cache
		entry.State, entry.Name, entry.Brand, entry.Pack = state, "", "", ""
		entry.ExpiresAt = entry.FetchedAt.Add(time.Minute)
		entry.Fingerprint, _ = entry.digest()
		if !entry.valid() {
			t.Fatal(entry)
		}
		reply := foodFactsCachedReply(1, entry.Code, &entry, entry.FetchedAt)
		if reply.State != state || reply.Source != nil {
			t.Fatal("non-product response disclosed product data", reply)
		}
	}
}

func TestFoodFactsCacheDigestAndTTLFailClosedOnChangedMetadata(t *testing.T) {
	original := foodFactsCacheFixture(t)
	for _, mutate := range []func(*foodFactsCache){
		func(cache *foodFactsCache) { cache.Name = "changed" },
		func(cache *foodFactsCache) { cache.Code = "04006381333931" },
		func(cache *foodFactsCache) { cache.Generation++ },
		func(cache *foodFactsCache) { cache.Payload = []byte{1} },
		func(cache *foodFactsCache) {
			cache.ExpiresAt = cache.ExpiresAt.Add(time.Second)
			cache.Fingerprint, _ = cache.digest()
		},
		func(cache *foodFactsCache) { cache.State = "not_found"; cache.Fingerprint, _ = cache.digest() },
	} {
		copy := original
		mutate(&copy)
		if copy.valid() {
			t.Fatal("corrupt/stale-shape cache accepted", copy)
		}
	}
}

func TestFoodFactsPermanentReceiptUsesActualCanonicalBytesAndUnknownIsNeutral(t *testing.T) {
	cache := foodFactsCacheFixture(t)
	receipt := PantryFoodFactsReceipt{OperationID: "00000000-0000-4000-8000-000000000002", Generation: 1,
		AcceptedAt: cache.FetchedAt.Add(time.Minute).Format(time.RFC3339Nano), ProductID: "00000000-0000-4000-8000-000000000003",
		BarcodeID: "00000000-0000-4000-8000-000000000004", ProvenanceID: "00000000-0000-4000-8000-000000000005",
		Source: cache.source(), SelectedFields: []string{"name"}, Message: "Historical adoption; no stock"}
	raw, err := json.MarshalIndent(receipt, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	row := &foodFactsOperationRow{ID: receipt.OperationID, Generation: 1, State: "complete", Request: bytes.Repeat([]byte{1}, 32), Result: raw, Digest: digest[:]}
	if reply := foodFactsOperationReply(row, 1); reply.State != "complete" || reply.Receipt == nil || reply.Receipt.Source.Code != cache.Code {
		t.Fatal("receipt hashed a new Go serialization rather than stored canonical bytes", reply)
	}
	for _, mutate := range []func(*foodFactsOperationRow){
		func(row *foodFactsOperationRow) { row.Result = nil; row.Digest = nil },
		func(row *foodFactsOperationRow) { row.Result = []byte(`{}`) },
		func(row *foodFactsOperationRow) { row.Generation = 2 },
		func(row *foodFactsOperationRow) { row.Digest = bytes.Repeat([]byte{0}, 32) },
	} {
		copy := *row
		mutate(&copy)
		if result := foodFactsOperationReply(&copy, 1); result.State != "unknown" || result.Receipt != nil {
			t.Fatal("missing/corrupt/foreign-generation receipt inferred success", result)
		}
	}
	refused := &foodFactsOperationRow{ID: receipt.OperationID, Generation: 1, State: "refused"}
	if reply := foodFactsOperationReply(refused, 1); reply.State != "refused" || reply.Receipt != nil {
		t.Fatal(reply)
	}
	if reply := foodFactsOperationReply(nil, 1); reply.State != "unknown" {
		t.Fatal(reply)
	}
}
