//go:build integration

package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	"household/internal/foodfacts"
	"household/internal/store"
	"household/internal/value"
)

// This deterministic provider is test-only. These native tests establish Store
// and HTTP behavior, not real Open Food Facts availability or data accuracy.
type pantryFoodFactsFixtureProvider struct {
	calls    atomic.Int32
	state    string
	started  chan struct{}
	release  chan struct{}
	limited  bool
	longText bool
}

func (provider *pantryFoodFactsFixtureProvider) Configuration() foodfacts.Configuration {
	return foodfacts.Configuration{State: provider.state, Provider: "off-production"}
}

func (provider *pantryFoodFactsFixtureProvider) Lookup(ctx context.Context, code string) (foodfacts.Result, error) {
	provider.calls.Add(1)
	if provider.started != nil {
		select {
		case provider.started <- struct{}{}:
		case <-ctx.Done():
			return foodfacts.Result{}, ctx.Err()
		}
		select {
		case <-provider.release:
		case <-ctx.Done():
			return foodfacts.Result{}, ctx.Err()
		}
	}
	if provider.limited {
		return foodfacts.Result{State: "limited", ErrorCode: "upstream_limited", RetryAfter: 3 * time.Minute}, nil
	}
	if provider.longText {
		return foodfacts.Result{State: "found", PayloadSHA256: strings.Repeat("a", 64), Product: &foodfacts.Product{
			Code: code, Name: strings.Repeat("<", 200), Brand: strings.Repeat("<", 300), PackDescription: strings.Repeat("<", 500)}}, nil
	}
	return foodfacts.Result{State: "found", PayloadSHA256: strings.Repeat("a", 64),
		Product: &foodfacts.Product{Code: code, Name: "SYNTHETIC unverified product", Brand: "SYNTHETIC brand", PackDescription: "3 x 150 g"}}, nil
}

func pantryFoodFactsProvider(f *integrationFixture) *pantryFoodFactsFixtureProvider {
	provider := &pantryFoodFactsFixtureProvider{state: "ready"}
	f.app.Store.FoodFacts = provider
	return provider
}

func pantryFoodFactsInput(t *testing.T, f *integrationFixture, code string) store.PantryFoodFactsInput {
	t.Helper()
	lookup, err := f.app.Store.LookupPantryFoodFacts(f.ctx, f.identity, store.PantryFoodFactsLookupInput{
		Generation: f.app.Store.States()["pantry"].Generation, Code: code, AllowExternal: true})
	if err != nil || lookup.State != "found" || lookup.Source == nil {
		t.Fatal("actual Store cache lookup", lookup, err)
	}
	return store.PantryFoodFactsInput{Generation: lookup.Generation, Code: code, Provider: lookup.Source.Provider,
		CacheFingerprint: lookup.Source.Fingerprint, SelectedFields: []string{"name", "brand", "pack_description"},
		ProductName: "SYNTHETIC explicitly chosen local product", Unit: "g", CreatePack: true, PackLabel: "SYNTHETIC chosen pack", PackQuantity: "0.5", PackUnit: "kg"}
}

func pantryFoodFactsEffects(t *testing.T, f *integrationFixture) string {
	t.Helper()
	var snapshot string
	err := f.pool.QueryRow(f.ctx, `SELECT jsonb_build_array(
 (SELECT count(*) FROM pantry.products WHERE household_id=$1),
 (SELECT count(*) FROM pantry.packs WHERE household_id=$1),
 (SELECT count(*) FROM pantry.barcodes WHERE household_id=$1),
 (SELECT count(*) FROM pantry.foodfacts_operations WHERE household_id=$1),
 (SELECT count(*) FROM pantry.foodfacts_provenance WHERE household_id=$1),
 (SELECT count(*) FROM pantry.lots WHERE household_id=$1),
 (SELECT count(*) FROM pantry.stock_movements WHERE household_id=$1),
 (SELECT count(*) FROM core.audit_log WHERE household_id=$1),
 (SELECT count(*) FROM core.search_documents WHERE household_id=$1),
 (SELECT count(*) FROM core.outbox_events WHERE household_id=$1))::text`, f.identity.HouseholdID).Scan(&snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestPantryFoodFactsLocalFirstConsentAndDisabledConfiguration(t *testing.T) {
	f := newIntegrationFixture(t)
	provider := pantryFoodFactsProvider(f)
	generation := f.app.Store.States()["pantry"].Generation
	lookup := func(code string, external bool) store.PantryFoodFactsLookup {
		t.Helper()
		result, err := f.app.Store.LookupPantryFoodFacts(f.ctx, f.identity, store.PantryFoodFactsLookupInput{Generation: generation, Code: code, AllowExternal: external})
		if err != nil {
			t.Fatal(result, err)
		}
		return result
	}
	if result := lookup("4006381333931", false); result.State != "unqueried" || provider.calls.Load() != 0 {
		t.Fatal("no consent still called provider", result)
	}
	if result := lookup("INTERNAL-001", true); result.State != "unsupported" || provider.calls.Load() != 0 {
		t.Fatal("household code escaped local lookup", result)
	}
	product, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "SYNTHETIC local first", "unit_code": "piece"})
	auditCreate(t, f, "pantry", "barcodes", store.Record{"product_id": product["id"], "code": "4006381333931"})
	provider.state = "disabled"
	if result := lookup("4006381333931", true); result.State != "local" || store.String(result.Local["product_id"]) != store.String(product["id"]) || provider.calls.Load() != 0 {
		t.Fatal("local association did not win while disabled", result)
	}
	if result := lookup("012345678905", true); result.State != "disabled" || provider.calls.Load() != 0 {
		t.Fatal(result)
	}
	provider.state = "not_configured"
	if result := lookup("012345678905", true); result.State != "not_configured" || provider.calls.Load() != 0 {
		t.Fatal(result)
	}
}

func TestPantryFoodFactsAtomicAdoptionPermanentReplayAndRefusal(t *testing.T) {
	f := newIntegrationFixture(t)
	provider := pantryFoodFactsProvider(f)
	input, key := pantryFoodFactsInput(t, f, "4006381333931"), value.UUID()
	review, err := f.app.Store.ReviewPantryFoodFacts(f.ctx, f.identity, key, input)
	if err != nil {
		t.Fatal(review, err)
	}
	receipt, err := f.app.Store.PostPantryFoodFacts(f.ctx, f.identity, key, value.UUID(), review.Input, review.Fingerprint)
	if err != nil || !value.ValidUUID(receipt.ProductID) || !value.ValidUUID(receipt.PackID) || !value.ValidUUID(receipt.BarcodeID) || !value.ValidUUID(receipt.ProvenanceID) {
		t.Fatal(receipt, err)
	}
	var exact bool
	err = f.pool.QueryRow(f.ctx, `SELECT
 EXISTS(SELECT 1 FROM pantry.products WHERE household_id=$1 AND id=$2 AND name=$3 AND unit_code='g')
 AND EXISTS(SELECT 1 FROM pantry.packs WHERE household_id=$1 AND id=$4 AND product_id=$2 AND quantity=0.5 AND unit_code='kg')
 AND EXISTS(SELECT 1 FROM pantry.barcodes WHERE household_id=$1 AND id=$5 AND product_id=$2 AND pack_id=$4 AND code='4006381333931')
 AND EXISTS(SELECT 1 FROM pantry.foodfacts_provenance WHERE household_id=$1 AND id=$6 AND source_snapshot->'adopted'->>'product_name'=$3)
 AND NOT EXISTS(SELECT 1 FROM pantry.lots WHERE household_id=$1)
 AND NOT EXISTS(SELECT 1 FROM pantry.stock_movements WHERE household_id=$1)
 AND(SELECT count(*) FROM core.outbox_events WHERE household_id=$1 AND module_id='pantry' AND event_type='record_changed' AND payload->>'id' IN($2::text,$4::text,$5::text))=3`,
		f.identity.HouseholdID, receipt.ProductID, input.ProductName, receipt.PackID, receipt.BarcodeID, receipt.ProvenanceID).Scan(&exact)
	if err != nil || !exact {
		t.Fatal("missing exact atomic catalog/provenance effects or unwanted stock", exact, err)
	}
	var selectedAfter, sourceAfter string
	var selectedRedacted, sourceRedacted, storedSelectionExact bool
	err = f.owner.QueryRow(f.ctx, `SELECT a.changes->'selected_fields'->>'after',
 (a.changes->'selected_fields'->>'redacted')::boolean,a.changes->'source_snapshot'->>'after',
 (a.changes->'source_snapshot'->>'redacted')::boolean,p.selected_fields=$3::text[]
 FROM core.audit_log a JOIN pantry.foodfacts_provenance p ON p.household_id=a.household_id AND p.id=a.resource_id
 WHERE a.household_id=$1 AND a.module_id='pantry' AND a.resource_type='foodfacts_provenance'
 AND a.resource_id=$2 AND a.action='CREATE'`, f.identity.HouseholdID, receipt.ProvenanceID, receipt.SelectedFields).
		Scan(&selectedAfter, &selectedRedacted, &sourceAfter, &sourceRedacted, &storedSelectionExact)
	if err != nil || selectedAfter != "***REDACTED***" || !selectedRedacted || sourceAfter != "***REDACTED***" || !sourceRedacted || !storedSelectionExact {
		t.Fatal("CREATE audit must redact structured provenance while the actual selected fields remain exact", selectedAfter, selectedRedacted, sourceAfter, sourceRedacted, storedSelectionExact, err)
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM pantry.foodfacts_cache WHERE household_id=$1", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.idempotency_records WHERE household_id=$1 AND module_id='pantry'", f.identity.HouseholdID); err != nil {
		t.Fatal(err)
	}
	before := pantryFoodFactsEffects(t, f)
	again, err := f.app.Store.PostPantryFoodFacts(f.ctx, f.identity, key, value.UUID(), review.Input, review.Fingerprint)
	a, _ := json.Marshal(receipt)
	b, _ := json.Marshal(again)
	if err != nil || !bytes.Equal(a, b) || pantryFoodFactsEffects(t, f) != before || provider.calls.Load() != 1 {
		t.Fatal("permanent replay depended on cache or duplicated catalog effects", again, err)
	}
	changed := review.Input
	changed.ProductName += " changed"
	if _, err = f.app.Store.PostPantryFoodFacts(f.ctx, f.identity, key, value.UUID(), changed, review.Fingerprint); !errors.Is(err, store.ErrConflict) || pantryFoodFactsEffects(t, f) != before {
		t.Fatal("changed body reused permanent key", err)
	}
	status, err := f.app.Store.PantryFoodFactsOperationStatus(f.ctx, f.identity, key, input.Generation)
	if err != nil || status.State != "complete" || status.Receipt == nil || status.Receipt.OperationID != receipt.OperationID {
		t.Fatal(status, err)
	}
	abandon, err := f.app.Store.AbandonPantryFoodFacts(f.ctx, f.identity, key, input.Generation, value.UUID())
	if err != nil || abandon.State != "complete" {
		t.Fatal("accepted operation was replaced by refusal", abandon, err)
	}
	other := value.UUID()
	refused, err := f.app.Store.AbandonPantryFoodFacts(f.ctx, f.identity, other, input.Generation, value.UUID())
	if err != nil || refused.State != "refused" {
		t.Fatal(refused, err)
	}
	before = pantryFoodFactsEffects(t, f)
	if _, err = f.app.Store.PostPantryFoodFacts(f.ctx, f.identity, other, value.UUID(), review.Input, review.Fingerprint); err == nil || pantryFoodFactsEffects(t, f) != before {
		t.Fatal("late dispatch crossed permanent refusal", err)
	}
	// Deliberate owner corruption/pruning fixture, not a simulated real purge.
	// Durable complete intent must never fall back to fresh effects when its
	// optional result payload is unavailable.
	if _, err = f.owner.Exec(f.ctx, "UPDATE pantry.foodfacts_operations SET result=NULL,result_hash=NULL WHERE household_id=$1 AND request_key=$2", f.identity.HouseholdID, key); err != nil {
		t.Fatal(err)
	}
	before = pantryFoodFactsEffects(t, f)
	status, err = f.app.Store.PantryFoodFactsOperationStatus(f.ctx, f.identity, key, input.Generation)
	if err != nil || status.State != "unknown" || status.Receipt != nil {
		t.Fatal("missing result reconstructed success", status, err)
	}
	if _, err = f.app.Store.PostPantryFoodFacts(f.ctx, f.identity, key, value.UUID(), review.Input, review.Fingerprint); err == nil || pantryFoodFactsEffects(t, f) != before {
		t.Fatal("missing permanent result duplicated catalog effects", err)
	}
}

func TestPantryFoodFactsRevalidatesAuthorityAfterProviderWithoutHoldingDomainLocks(t *testing.T) {
	for _, boundary := range []string{"session", "role", "generation", "disabled"} {
		t.Run(boundary, func(t *testing.T) {
			f := newIntegrationFixture(t)
			provider := pantryFoodFactsProvider(f)
			provider.started, provider.release = make(chan struct{}, 1), make(chan struct{})
			var once sync.Once
			finish := func() { once.Do(func() { close(provider.release) }) }
			t.Cleanup(finish)
			ctx, cancel := context.WithTimeout(f.ctx, 6*time.Second)
			defer cancel()
			out := make(chan error, 1)
			generation := f.app.Store.States()["pantry"].Generation
			go func() {
				_, err := f.app.Store.LookupPantryFoodFacts(ctx, f.identity, store.PantryFoodFactsLookupInput{Generation: generation, Code: "4006381333931", AllowExternal: true})
				out <- err
			}()
			select {
			case <-provider.started:
			case <-time.After(2 * time.Second):
				t.Fatal("provider did not start after initial authority check")
			}
			// This update must finish while HTTP is suspended. A live business
			// transaction/session/module lock across the provider call would block it.
			change, stop := context.WithTimeout(f.ctx, time.Second)
			var err error
			switch boundary {
			case "session":
				_, err = f.owner.Exec(change, "UPDATE core.sessions SET revoked_at=clock_timestamp() WHERE id=$1", f.identity.SessionID)
			case "role":
				_, err = f.owner.Exec(change, "UPDATE core.memberships SET role='reader' WHERE id=$1", f.identity.MembershipID)
			case "generation":
				// Deliberate isolated boundary fixture, not operational purge evidence.
				_, err = f.owner.Exec(change, "UPDATE core.module_states SET data_generation=data_generation+1 WHERE household_id=$1 AND module_id='pantry'", f.identity.HouseholdID)
			case "disabled":
				_, err = f.owner.Exec(change, "UPDATE core.module_states SET state='disabled' WHERE household_id=$1 AND module_id='pantry'", f.identity.HouseholdID)
			}
			stop()
			if err != nil {
				finish()
				t.Fatal("domain barrier was held across provider IO", err)
			}
			finish()
			select {
			case err = <-out:
				if err == nil {
					t.Fatal("provider response crossed changed current authority", boundary)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("provider response did not release request/DB resources")
			}
			var cached int
			if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM pantry.foodfacts_cache WHERE household_id=$1", f.identity.HouseholdID).Scan(&cached); err != nil || cached != 0 {
				t.Fatal("unauthorized response persisted/disclosed cache data", cached, err)
			}
		})
	}
}

func pantryFoodFactsGTIN(number int) string {
	prefix, sum, weight := fmt.Sprintf("%012d", number), 0, 3
	for index := len(prefix) - 1; index >= 0; index-- {
		sum += int(prefix[index]-'0') * weight
		weight = 4 - weight
	}
	return prefix + strconv.Itoa((10-sum%10)%10)
}

func TestPantryFoodFactsBudgetPersistsAcrossStoresAndHonorsUpstreamCooldown(t *testing.T) {
	f := newIntegrationFixture(t)
	provider := pantryFoodFactsProvider(f)
	generation := f.app.Store.States()["pantry"].Generation
	for index := 1; index <= 12; index++ {
		out, err := f.app.Store.LookupPantryFoodFacts(f.ctx, f.identity, store.PantryFoodFactsLookupInput{Generation: generation, Code: pantryFoodFactsGTIN(index), AllowExternal: true})
		if err != nil || out.State != "found" {
			t.Fatal(index, out, err)
		}
	}
	other := store.New(f.pool, f.app.Store.Modules)
	other.FoodFacts = provider
	out, err := other.LookupPantryFoodFacts(f.ctx, f.identity, store.PantryFoodFactsLookupInput{Generation: generation, Code: pantryFoodFactsGTIN(13), AllowExternal: true})
	if err != nil || out.State != "limited" || out.RetryAt == "" || provider.calls.Load() != 12 {
		t.Fatal("new Store bypassed persistent installation-wide budget", out, provider.calls.Load(), err)
	}
	// Explicit budget fixture reset in the disposable DB; this is not a purge
	// implementation and does not assert rate rows should be removed by purge.
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.rate_limit_buckets WHERE scope='pantry-off-product-v1'"); err != nil {
		t.Fatal(err)
	}
	provider.limited = true
	out, err = other.LookupPantryFoodFacts(f.ctx, f.identity, store.PantryFoodFactsLookupInput{Generation: generation, Code: pantryFoodFactsGTIN(14), AllowExternal: true})
	if err != nil || out.State != "limited" || provider.calls.Load() != 13 {
		t.Fatal(out, err)
	}
	provider.limited = false
	out, err = other.LookupPantryFoodFacts(f.ctx, f.identity, store.PantryFoodFactsLookupInput{Generation: generation, Code: pantryFoodFactsGTIN(15), AllowExternal: true})
	if err != nil || out.State != "limited" || provider.calls.Load() != 13 {
		t.Fatal("received429 cooldown was not durable", out, err)
	}
}

func pantryFoodFactsHTTP(t *testing.T, f *integrationFixture, path string, body any, key string) (int, store.Record) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(f.ctx, http.MethodPost, f.server.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: f.app.cookieName(), Value: f.token})
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", f.server.URL)
	request.Header.Set("X-CSRF-Token", f.app.Auth.CSRF(f.token, "session"))
	request.Header.Set("Idempotency-Key", key)
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 256<<10+1))
	if err != nil || len(data) > 256<<10 {
		t.Fatal("unbounded extension HTTP response", err)
	}
	result, err := store.DecodeRecord(data)
	if err != nil {
		t.Fatal("extension HTTP returned non-JSON", response.StatusCode, err)
	}
	return response.StatusCode, result
}

func TestPantryFoodFactsHTTPRequiresSignedCurrentReviewAndStableKey(t *testing.T) {
	f := newIntegrationFixture(t)
	pantryFoodFactsProvider(f)
	input, key := pantryFoodFactsInput(t, f, "4006381333931"), value.UUID()
	base := "/api/v1/pantry/extensions/food-facts"
	status, preview := pantryFoodFactsHTTP(t, f, base+"/preview", input, key)
	if status != 200 || store.String(preview["review"]) == "" {
		t.Fatal(status, preview)
	}
	before := pantryFoodFactsEffects(t, f)
	for _, test := range []struct {
		body any
		key  string
	}{{store.Record{"review": preview["review"], "input": store.Record{"product_name": "unsigned replacement"}}, key},
		{store.Record{"review": preview["review"]}, value.UUID()}, {store.Record{"review": store.String(preview["review"]) + "x"}, key}} {
		if code, response := pantryFoodFactsHTTP(t, f, base+"/execute", test.body, test.key); code < 400 || pantryFoodFactsEffects(t, f) != before {
			t.Fatal("unsigned/substituted review changed domain effects", code, response)
		}
	}
	status, receipt := pantryFoodFactsHTTP(t, f, base+"/execute", store.Record{"review": preview["review"]}, key)
	if status != 200 || !value.ValidUUID(store.String(receipt["product_id"])) {
		t.Fatal(status, receipt)
	}
}

func TestPantryFoodFactsChangedInsertedFieldsRollBackReceiptAndAllCatalogEffects(t *testing.T) {
	f := newIntegrationFixture(t)
	pantryFoodFactsProvider(f)
	input, key := pantryFoodFactsInput(t, f, "4006381333931"), value.UUID()
	review, err := f.app.Store.ReviewPantryFoodFacts(f.ctx, f.identity, key, input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.owner.Exec(f.ctx, `CREATE FUNCTION pantry.test_foodfacts_alter_name() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.name='SYNTHETIC unexpected changed field'; RETURN NEW; END$$;
 CREATE TRIGGER test_foodfacts_alter_name BEFORE INSERT ON pantry.products FOR EACH ROW EXECUTE FUNCTION pantry.test_foodfacts_alter_name()`)
	if err != nil {
		t.Fatal(err)
	}
	before := pantryFoodFactsEffects(t, f)
	if _, err = f.app.Store.PostPantryFoodFacts(f.ctx, f.identity, key, value.UUID(), review.Input, review.Fingerprint); err == nil || pantryFoodFactsEffects(t, f) != before {
		t.Fatal("changed actual field was accepted or partial effects escaped transaction", err)
	}
}

func TestPantryFoodFactsProvenanceShowsLatestTwentyAndHonestMore(t *testing.T) {
	f := newIntegrationFixture(t)
	pantryFoodFactsProvider(f)
	product, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "SYNTHETIC many source identities", "unit_code": "g"})
	productID := store.String(product["id"])
	for index := 1; index <= 21; index++ {
		if index == 13 {
			// Fixture-only reset to construct21 reviewed source adoptions without
			// waiting a real minute. The separate budget contract forbids restart
			// bypass; this test does not establish upstream rate-limit behavior.
			if _, err := f.owner.Exec(f.ctx, "DELETE FROM core.rate_limit_buckets WHERE scope='pantry-off-product-v1'"); err != nil {
				t.Fatal(err)
			}
		}
		input, key := pantryFoodFactsInput(t, f, pantryFoodFactsGTIN(index)), value.UUID()
		module, _ := f.app.Store.Module("pantry")
		entity, _ := module.Entity("products")
		current, err := f.app.Store.Get(f.ctx, f.identity, module, entity, productID)
		if err != nil {
			t.Fatal(err)
		}
		input.ProductID, input.ProductVersion = productID, store.Int(current["version"])
		input.ProductName, input.Unit, input.CreatePack = "", "", false
		input.PackLabel, input.PackQuantity, input.PackUnit = "", "", ""
		review, err := f.app.Store.ReviewPantryFoodFacts(f.ctx, f.identity, key, input)
		if err != nil {
			t.Fatal(index, review, err)
		}
		if _, err = f.app.Store.PostPantryFoodFacts(f.ctx, f.identity, key, value.UUID(), review.Input, review.Fingerprint); err != nil {
			t.Fatal(index, err)
		}
		if index == 20 {
			page, err := f.app.Store.PantryFoodFactsProvenance(f.ctx, f.identity, productID, input.Generation)
			if err != nil || len(page.Sources) != 20 || page.More {
				t.Fatal("exact20 boundary was incomplete", page, err)
			}
		}
	}
	page, err := f.app.Store.PantryFoodFactsProvenance(f.ctx, f.identity, productID, f.app.Store.States()["pantry"].Generation)
	if err != nil || len(page.Sources) != 20 || !page.More {
		t.Fatal("sentinel21 was denied or hidden", page, err)
	}
	rows, err := f.pool.Query(f.ctx, "SELECT code FROM pantry.foodfacts_provenance WHERE household_id=$1 AND product_id=$2 ORDER BY created_at DESC,id DESC LIMIT 20", f.identity.HouseholdID, productID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		var code string
		if err = rows.Scan(&code); err != nil || index >= len(page.Sources) || page.Sources[index].Code != code {
			t.Fatal("provenance order differs from complete actual scope", index, code, err)
		}
		index++
	}
	if err = rows.Err(); err != nil || index != 20 {
		t.Fatal(index, err)
	}
}

func TestPantryFoodFactsCacheCapEvictsDeterministicallyAndCorruptionIsNotAdopted(t *testing.T) {
	f := newIntegrationFixture(t)
	provider := pantryFoodFactsProvider(f)
	generation := f.app.Store.States()["pantry"].Generation
	var now time.Time
	if err := f.owner.QueryRow(f.ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	fetched, expired := now.Add(-2*time.Minute), now.Add(-time.Minute)
	rows := make([]store.Record, 0, 1000)
	for number := 1; number <= 1000; number++ {
		code := pantryFoodFactsGTIN(number)
		// An explicit old infrastructure-cache fixture, not an upstream receipt.
		// Construct the version-one cache fingerprint independently from public
		// fixture values; no production helper or fake success record is added.
		body, err := json.Marshal([]any{"pantry-foodfacts-v1/cache", f.identity.HouseholdID, generation, "off-production", code,
			"error", "", "", "", "fixture", "", fetched.UTC().Format(time.RFC3339Nano), expired.UTC().Format(time.RFC3339Nano), ""})
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(body)
		rows = append(rows, store.Record{"code": code, "fingerprint": hex.EncodeToString(hash[:])})
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.owner.Exec(f.ctx, `INSERT INTO pantry.foodfacts_cache(household_id,generation,provider,code,state,error_code,fingerprint,fetched_at,expires_at)
 SELECT $1,$2,'off-production',x.code,'error','fixture',decode(x.fingerprint,'hex'),$4,$5
 FROM jsonb_to_recordset($3::jsonb) AS x(code text,fingerprint text)`, f.identity.HouseholdID, generation, raw, fetched, expired)
	if err != nil {
		t.Fatal(err)
	}
	var oldest string
	if err = f.owner.QueryRow(f.ctx, "SELECT code FROM pantry.foodfacts_cache WHERE household_id=$1 AND generation=$2 ORDER BY fetched_at,provider,code LIMIT 1", f.identity.HouseholdID, generation).Scan(&oldest); err != nil {
		t.Fatal(err)
	}
	input := pantryFoodFactsInput(t, f, "4006381333931")
	var count int
	var evicted bool
	err = f.owner.QueryRow(f.ctx, `SELECT count(*),NOT EXISTS(SELECT 1 FROM pantry.foodfacts_cache
 WHERE household_id=$1 AND generation=$2 AND code=$3) FROM pantry.foodfacts_cache WHERE household_id=$1 AND generation=$2`, f.identity.HouseholdID, generation, oldest).Scan(&count, &evicted)
	if err != nil || count != 1000 || !evicted || provider.calls.Load() != 1 {
		t.Fatal("cache exceeded cap or discarded an arbitrary row", count, evicted, provider.calls.Load(), err)
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE pantry.foodfacts_cache SET fingerprint=decode(repeat('00',32),'hex') WHERE household_id=$1 AND generation=$2 AND code=$3", f.identity.HouseholdID, generation, input.Code); err != nil {
		t.Fatal(err)
	}
	before := pantryFoodFactsEffects(t, f)
	if _, err = f.app.Store.ReviewPantryFoodFacts(f.ctx, f.identity, value.UUID(), input); err == nil || pantryFoodFactsEffects(t, f) != before || provider.calls.Load() != 1 {
		t.Fatal("corrupt cached source was adopted or silently refreshed", err)
	}
}

func TestPantryFoodFactsReadersRetainLicensedAttributionWithoutWriterRightsOrPrivateProofs(t *testing.T) {
	f := newIntegrationFixture(t)
	pantryFoodFactsProvider(f)
	reader := shoppingTestMember(t, f, "foodfacts-origin-reader", "reader")
	input, key := pantryFoodFactsInput(t, f, "4006381333931"), value.UUID()
	review, err := f.app.Store.ReviewPantryFoodFacts(f.ctx, f.identity, key, input)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := f.app.Store.PostPantryFoodFacts(f.ctx, f.identity, key, value.UUID(), review.Input, review.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	const privateNote = "SYNTHETIC PRIVATE NOTE NOT SOURCE METADATA"
	if _, err = f.owner.Exec(f.ctx, "UPDATE pantry.products SET notes=$2 WHERE household_id=$1 AND id=$3", f.identity.HouseholdID, privateNote, receipt.ProductID); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ entity, id string }{{"products", receipt.ProductID}, {"packs", receipt.PackID}, {"barcodes", receipt.BarcodeID}} {
		data, err := f.app.Store.PantryFoodFactsExportSources(f.ctx, reader, row.entity, []string{row.id})
		if err != nil || !json.Valid([]byte(data[row.id])) || !strings.Contains(data[row.id], "ODbL-1.0") || !strings.Contains(data[row.id], "DbCL-1.0") ||
			!strings.Contains(data[row.id], "SYNTHETIC explicitly chosen local product") || !strings.Contains(data[row.id], "SYNTHETIC unverified product") || !strings.Contains(data[row.id], "selected_fields") {
			t.Fatal("reader export lost source/adopted text/license", row, data, err)
		}
		for _, private := range []string{privateNote, key, f.identity.SessionID, `"request_hash"`, `"created_by"`, `"csrf"`, `"result_hash"`} {
			if strings.Contains(data[row.id], private) {
				t.Fatal("private proof/session/notes entered shared attribution", private)
			}
		}
	}
	page, err := f.app.Store.PantryFoodFactsProvenance(f.ctx, reader, receipt.ProductID, input.Generation)
	if err != nil || len(page.Sources) != 1 || page.More {
		t.Fatal("shared reader could not see current product origin", page, err)
	}
	before := pantryFoodFactsEffects(t, f)
	for _, operation := range []func() error{
		func() error {
			_, err := f.app.Store.LookupPantryFoodFacts(f.ctx, reader, store.PantryFoodFactsLookupInput{Generation: input.Generation, Code: input.Code, AllowExternal: true})
			return err
		},
		func() error {
			_, err := f.app.Store.ReviewPantryFoodFacts(f.ctx, reader, value.UUID(), input)
			return err
		},
		func() error {
			_, err := f.app.Store.PostPantryFoodFacts(f.ctx, reader, key, value.UUID(), review.Input, review.Fingerprint)
			return err
		},
		func() error {
			_, err := f.app.Store.PantryFoodFactsOperationStatus(f.ctx, reader, key, input.Generation)
			return err
		},
		func() error {
			_, err := f.app.Store.AbandonPantryFoodFacts(f.ctx, reader, key, input.Generation, value.UUID())
			return err
		},
	} {
		if err = operation(); !errors.Is(err, store.ErrForbidden) || pantryFoodFactsEffects(t, f) != before {
			t.Fatal("origin read accidentally conferred writer authority", err)
		}
	}
	foreign := reader
	foreign.HouseholdID = value.UUID()
	if data, err := f.app.Store.PantryFoodFactsExportSources(f.ctx, foreign, "products", []string{receipt.ProductID}); err == nil || len(data) != 0 {
		t.Fatal("foreign household identity read provenance", data, err)
	}
	for _, test := range []struct {
		entity string
		ids    []string
	}{
		{"lots", []string{receipt.ProductID}}, {"products", []string{"invalid"}},
		{"products", []string{receipt.ProductID, receipt.ProductID}}, {"products", make([]string, 101)},
	} {
		if data, err := f.app.Store.PantryFoodFactsExportSources(f.ctx, reader, test.entity, test.ids); err == nil || len(data) != 0 {
			t.Fatal("export allowlist/ID/page bound bypassed", test, data, err)
		}
	}
	// Current12 must not mask a damaged provenance catalog as a legacy export.
	if _, err = f.owner.Exec(f.ctx, "ALTER TABLE pantry.foodfacts_provenance ADD COLUMN test_unreviewed_export_field text"); err != nil {
		t.Fatal(err)
	}
	if data, err := f.app.Store.PantryFoodFactsExportSources(f.ctx, reader, "products", []string{receipt.ProductID}); err == nil || len(data) != 0 {
		t.Fatal("damaged current provenance schema silently lost attribution", data, err)
	}
}

func TestPantryFoodFactsExportRefusesCompleteRowByteOverflowInsteadOfDroppingLicense(t *testing.T) {
	f := newIntegrationFixture(t)
	provider := pantryFoodFactsProvider(f)
	provider.longText = true
	product, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": strings.Repeat("<", 200), "unit_code": "g"})
	productID := store.String(product["id"])
	for index := 1; index <= 35; index++ {
		if index == 13 || index == 25 {
			// Fixture-only admission reset; the separate durable-budget test
			// owns rate behavior. No actual upstream calls occur in this fixture.
			if _, err := f.owner.Exec(f.ctx, "DELETE FROM core.rate_limit_buckets WHERE scope='pantry-off-product-v1'"); err != nil {
				t.Fatal(err)
			}
		}
		input, key := pantryFoodFactsInput(t, f, pantryFoodFactsGTIN(index)), value.UUID()
		input.ProductID, input.ProductVersion = productID, store.Int(product["version"])
		input.ProductName, input.Unit, input.CreatePack = "", "", false
		input.PackLabel, input.PackQuantity, input.PackUnit = "", "", ""
		review, err := f.app.Store.ReviewPantryFoodFacts(f.ctx, f.identity, key, input)
		if err != nil {
			t.Fatal(index, err)
		}
		if _, err = f.app.Store.PostPantryFoodFacts(f.ctx, f.identity, key, value.UUID(), review.Input, review.Fingerprint); err != nil {
			t.Fatal(index, err)
		}
	}
	// JSON escaping of valid text can exceed the final encoded-row budget even
	// when every persisted source_snapshot individually fits its8KiB constraint.
	data, err := f.app.Store.PantryFoodFactsExportSources(f.ctx, f.identity, "products", []string{productID})
	if err == nil || len(data) != 0 {
		t.Fatal("oversized complete attribution was silently clipped or omitted", data, err)
	}
}

func TestPantryFoodFactsObserved429WaitsForBudgetLockAndRetainsMaximumBackoff(t *testing.T) {
	f := newIntegrationFixture(t)
	provider := pantryFoodFactsProvider(f)
	provider.limited, provider.started, provider.release = true, make(chan struct{}, 1), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(provider.release) }) }
	t.Cleanup(release)
	ctx, cancel := context.WithTimeout(f.ctx, 6*time.Second)
	defer cancel()
	type response struct {
		result store.PantryFoodFactsLookup
		err    error
	}
	done := make(chan response, 1)
	generation := f.app.Store.States()["pantry"].Generation
	go func() {
		result, err := f.app.Store.LookupPantryFoodFacts(ctx, f.identity, store.PantryFoodFactsLookupInput{Generation: generation, Code: "4006381333931", AllowExternal: true})
		done <- response{result, err}
	}()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("initial admission did not finish before provider call")
	}
	held, err := f.owner.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_ = held.Rollback(cleanup)
	}()
	if _, err = held.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended('pantry-foodfacts-budget-v1',0))"); err != nil {
		t.Fatal(err)
	}
	var expected time.Time
	err = held.QueryRow(ctx, `INSERT INTO core.rate_limit_buckets(scope,subject_hash,window_start,window_end,attempt_count)
 VALUES('pantry-off-cooldown-v1',sha256(convert_to('household-installation-foodfacts-v1','UTF8')),'2000-01-01T00:00:00Z',clock_timestamp()+interval '5 minutes',1)
 RETURNING window_end`).Scan(&expected)
	if err != nil {
		t.Fatal(err)
	}
	release()
	waiting := false
	until := time.Now().Add(time.Second)
	for !waiting && time.Now().Before(until) {
		select {
		case early := <-done:
			t.Fatal("observed429 was discarded instead of waiting for durable budget serialization", early)
		default:
		}
		err = f.owner.QueryRow(ctx, `WITH wanted AS(SELECT hashtextextended('pantry-foodfacts-budget-v1',0) AS key)
 SELECT EXISTS(SELECT 1 FROM pg_locks l CROSS JOIN wanted w WHERE l.locktype='advisory' AND NOT l.granted AND l.objsubid=1
 AND l.database=(SELECT oid FROM pg_database WHERE datname=current_database())
 AND l.classid::bigint=((w.key>>32)&4294967295::bigint) AND l.objid::bigint=(w.key&4294967295::bigint))`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if !waiting {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !waiting {
		t.Fatal("cooldown did not acquire its bounded waiting lease")
	}
	if err = held.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case out := <-done:
		retry, parseErr := time.Parse(time.RFC3339Nano, out.result.RetryAt)
		if out.err != nil || out.result.State != "limited" || parseErr != nil || retry.Before(expected) {
			t.Fatal("larger concurrent backoff was shrunk or not exposed", out, parseErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bounded cooldown did not finish after release")
	}
	var stored time.Time
	err = f.owner.QueryRow(f.ctx, "SELECT max(window_end) FROM core.rate_limit_buckets WHERE scope='pantry-off-cooldown-v1'").Scan(&stored)
	if err != nil || stored.Before(expected) {
		t.Fatal("Retry-After was not persisted with maximum semantics", stored, expected, err)
	}
	provider.limited = false
	result, err := f.app.Store.LookupPantryFoodFacts(f.ctx, f.identity, store.PantryFoodFactsLookupInput{Generation: generation, Code: "012345678905", AllowExternal: true})
	if err != nil || result.State != "limited" || provider.calls.Load() != 1 {
		t.Fatal("following admission bypassed observed backoff", result, provider.calls.Load(), err)
	}
}
