//go:build integration

package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/database"
	"household/internal/store"
	"household/internal/value"

	"github.com/jackc/pgx/v5/pgconn"
)

func pantryLeftoverFixture(t *testing.T, f *integrationFixture) store.PantryLeftoversInput {
	t.Helper()
	context, err := f.app.Store.PantryLeftoversContext(f.ctx, f.identity, "manual", "")
	if err != nil {
		t.Fatal("actual current leftovers context", err)
	}
	location, _ := auditCreate(t, f, "pantry", "locations", store.Record{"name": "Restekühlschrank"})
	return store.PantryLeftoversInput{Generation: store.Int(context["generation"]), SourceKind: "manual", Name: "Tatsächlich zubereitete Suppe",
		PreparedOn: store.String(context["today"]), PortionsUnknown: true,
		Containers: []store.PantryLeftoverContainer{{ID: value.UUID(), Label: "Erster Behälter", Quantity: "1.250001", LocationID: store.String(location["id"]), LocationVersion: store.Int(location["version"]), ReceivedOn: store.String(context["today"])}}}
}

func pantryLeftoverReview(t *testing.T, f *integrationFixture, in store.PantryLeftoversInput) store.PantryLeftoversInput {
	t.Helper()
	view, err := f.app.Store.ReviewPantryLeftovers(f.ctx, f.identity, in)
	if err != nil {
		t.Fatal("actual leftovers review", err)
	}
	encoded, err := json.Marshal(view["normalized"])
	if err != nil || json.Unmarshal(encoded, &in) != nil || in.Fingerprint == "" {
		t.Fatal("typed normalized input and fingerprint missing", err)
	}
	return in
}

func TestPantryLeftoversSignedHTTPAndPermanentReplay(t *testing.T) {
	f := newIntegrationFixture(t)
	in := pantryLeftoverFixture(t, f)
	key := value.UUID()
	raw, _ := json.Marshal(in)
	headers := map[string]string{"Idempotency-Key": key}
	csrf := f.app.Auth.CSRF(f.token, "session")
	response, body, err := f.request(http.MethodPost, "/api/v1/pantry/leftovers/preview", f.token, csrf, "application/json", bytes.NewReader(raw), headers)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatal("actual signed leftovers preview", response, string(body), err)
	}
	var review struct {
		Review string `json:"review"`
	}
	if json.Unmarshal(body, &review) != nil || review.Review == "" {
		t.Fatal("missing signed review envelope")
	}
	execute, _ := json.Marshal(store.Record{"review": review.Review})
	response, body, err = f.request(http.MethodPost, "/api/v1/pantry/leftovers/execute", f.token, csrf, "application/json", bytes.NewReader(execute), headers)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatal("actual signed leftovers execution", response, string(body), err)
	}
	var receipt store.Record
	if json.Unmarshal(body, &receipt) != nil || receipt["operation_id"] == nil {
		t.Fatal("actual receipt missing", string(body))
	}
	var lots, movements int
	if err = f.owner.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM pantry.prepared_lots),(SELECT count(*) FROM pantry.stock_movements)").Scan(&lots, &movements); err != nil || lots != 1 || movements != 1 {
		t.Fatal("one actual lot and positive journal entry", lots, movements, err)
	}
	if _, err = f.owner.Exec(f.ctx, "DELETE FROM core.idempotency_records"); err != nil {
		t.Fatal("synthetic generic-cache pruning", err)
	}
	response, replay, err := f.request(http.MethodPost, "/api/v1/pantry/leftovers/execute", f.token, csrf, "application/json", bytes.NewReader(execute), headers)
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(body, replay) {
		t.Fatal("permanent same-key replay differs after cache pruning", response, string(replay), err)
	}
	status, err := f.app.Store.PantryLeftoverOperation(f.ctx, f.identity, key, in.Generation)
	if err != nil || status["state"] != "complete" {
		t.Fatal("uncertain-outcome recovery", status, err)
	}
	headers["Idempotency-Key"] = value.UUID()
	response, _, err = f.request(http.MethodPost, "/api/v1/pantry/leftovers/execute", f.token, csrf, "application/json", bytes.NewReader(execute), headers)
	if err != nil || response.StatusCode < 400 {
		t.Fatal("signed key substitution accepted", response, err)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("complete current schema readiness", err)
	}
}

func TestPantryLeftoversAtomicTwentyContainersAndCurrentReader(t *testing.T) {
	f := newIntegrationFixture(t)
	in := pantryLeftoverFixture(t, f)
	line := in.Containers[0]
	in.Containers = nil
	for range 20 {
		line.ID, line.Quantity = value.UUID(), "0.000001"
		in.Containers = append(in.Containers, line)
	}
	in = pantryLeftoverReview(t, f, in)
	stale := in
	stale.Containers = append([]store.PantryLeftoverContainer{}, in.Containers...)
	stale.Containers[19].LocationVersion++
	if _, err := f.app.Store.PostPantryLeftovers(f.ctx, f.identity, value.UUID(), value.UUID(), stale); err == nil {
		t.Fatal("stale twentieth container partially committed")
	}
	var count int
	if err := f.owner.QueryRow(f.ctx, "SELECT count(*) FROM pantry.prepared_lots").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed complete review changed stock", count, err)
	}
	// Owner-only fault injection after the first nineteen real writes. It is
	// removed before the successful retry and is never an application path.
	if _, err := f.owner.Exec(f.ctx, `CREATE FUNCTION pantry.leftover_test_fault() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.label='Injected final container failure' THEN RAISE EXCEPTION 'injected final leftover failure' USING ERRCODE='23514'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER leftover_test_fault BEFORE INSERT ON pantry.prepared_lots FOR EACH ROW EXECUTE FUNCTION pantry.leftover_test_fault()`); err != nil {
		t.Fatal("install explicit failure fixture", err)
	}
	failing := in
	failing.Containers = append([]store.PantryLeftoverContainer{}, in.Containers...)
	failing.Containers[19].Label = "Injected final container failure"
	failing = pantryLeftoverReview(t, f, failing)
	if _, err := f.app.Store.PostPantryLeftovers(f.ctx, f.identity, value.UUID(), value.UUID(), failing); err == nil {
		t.Fatal("twentieth real insert failure unexpectedly committed")
	}
	var products, movements, operations int
	if err := f.owner.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM pantry.prepared_lots),
 (SELECT count(*) FROM pantry.products),(SELECT count(*) FROM pantry.stock_movements),(SELECT count(*) FROM pantry.leftover_operations)`).Scan(&count, &products, &movements, &operations); err != nil || count != 0 || products != 0 || movements != 0 || operations != 0 {
		t.Fatal("real late failure left partial lots/product/journal/fence", count, products, movements, operations, err)
	}
	if _, err := f.owner.Exec(f.ctx, "DROP TRIGGER leftover_test_fault ON pantry.prepared_lots; DROP FUNCTION pantry.leftover_test_fault()"); err != nil {
		t.Fatal("remove exact failure fixture", err)
	}
	result, err := f.app.Store.PostPantryLeftovers(f.ctx, f.identity, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal("actual twenty-container atomic intake", err)
	}
	var total string
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*),sum(initial_portions)::text FROM pantry.prepared_lots").Scan(&count, &total); err != nil || count != 20 || total != "0.000020" {
		t.Fatal("twenty exact committed quantities", count, total, err)
	}
	lines := result["lines"].([]store.Record)
	lot := store.String(lines[0]["lot_id"])
	// Explicit authority fault injection: actual membership is downgraded;
	// the read uses the corresponding current reader identity, not old writer.
	if _, err = f.owner.Exec(f.ctx, "UPDATE core.memberships SET role='reader' WHERE id=$1", f.identity.MembershipID); err != nil {
		t.Fatal(err)
	}
	reader := f.identity
	reader.Role = "reader"
	view, err := f.app.Store.PantryPreparedLot(f.ctx, reader, lot)
	if err != nil || view["unit"] != "portion" {
		t.Fatal("current reader cannot inspect owned preparation metadata", view, err)
	}
	if _, err = f.app.Store.PostPantryLeftovers(f.ctx, reader, value.UUID(), value.UUID(), in); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("reader gained stock authority", err)
	}
	if _, err = f.app.Store.PantryPreparedLot(f.ctx, f.identity, lot); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("stale writer identity retained authority", err)
	}
}

func TestPantryLeftoversCookingCapAndOnceOnlySource(t *testing.T) {
	f := newIntegrationFixture(t)
	_, record, stockInput := cookingStockFixture(t, f)
	in := pantryLeftoverFixture(t, f)
	source, err := f.app.Store.PantryLeftoversContext(f.ctx, f.identity, "cooking", record)
	if err != nil {
		t.Fatal(err)
	}
	in.SourceKind, in.SourceID, in.RecipesGeneration = "cooking", record, store.Int(source["recipes_generation"])
	in.Containers[0].Quantity = "1.000001"
	if _, err = f.app.Store.ReviewPantryLeftovers(f.ctx, f.identity, in); err == nil {
		t.Fatal("known one-portion cooking output was exceeded")
	}
	in.Containers[0].Quantity = "0.5"
	in = pantryLeftoverReview(t, f, in)
	if in.PreparedPortions != "1.000000" || in.PortionsUnknown {
		t.Fatal("real cooking quantity was not the authoritative cap", in)
	}
	key := value.UUID()
	if _, err = f.app.Store.PostPantryLeftovers(f.ctx, f.identity, key, value.UUID(), in); err != nil {
		t.Fatal("actual cooking-derived leftovers", err)
	}
	if _, err = f.app.Store.PostPantryLeftovers(f.ctx, f.identity, value.UUID(), value.UUID(), in); err == nil {
		t.Fatal("new transport key duplicated one cooking source")
	}
	var ingredientStock string
	if err = f.owner.QueryRow(f.ctx, "SELECT quantity::text FROM pantry.lot_balances WHERE household_id=$1 AND lot_id=$2", f.identity.HouseholdID, stockInput.Lots[0].ID).Scan(&ingredientStock); err != nil || ingredientStock != "3.000000" {
		t.Fatal("leftovers consumed ingredients again", ingredientStock, err)
	}
}

func TestPantryLeftoversSQLPendingAndPermanentRefusal(t *testing.T) {
	f := newIntegrationFixture(t)
	in := pantryLeftoverFixture(t, f)
	key := value.UUID()
	status, err := f.app.Store.PantryLeftoverOperation(f.ctx, f.identity, key, in.Generation)
	if err != nil || status["state"] != "unknown" {
		t.Fatal("absence is not a durable refusal", status, err)
	}
	status, err = f.app.Store.AbandonPantryLeftoverOperation(f.ctx, f.identity, key, in.Generation, value.UUID())
	if err != nil || status["state"] != "refused" {
		t.Fatal("explicit abandonment did not fence key", status, err)
	}
	in = pantryLeftoverReview(t, f, in)
	if _, err = f.app.Store.PostPantryLeftovers(f.ctx, f.identity, key, value.UUID(), in); err == nil {
		t.Fatal("late dispatch recreated a refused operation")
	}
	if _, err = f.pool.Exec(f.ctx, "SELECT * FROM pantry.leftover_operations"); err == nil {
		t.Fatal("runtime can inspect private operation proofs")
	}
	ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
	tx, err := f.app.Store.BeginMutation(ctx, f.identity, "pantry", in.Generation)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(f.ctx) }()
	if _, err = tx.Exec(f.ctx, "SELECT pantry.begin_leftovers($1,$2,$3,decode(repeat('00',32),'hex'),'manual','',0,'',$4,$5,$6)", value.UUID(), value.UUID(), in.Generation, f.identity.SessionID, f.identity.AuthVersion, f.identity.MembershipID); err != nil {
		t.Fatal("actual App begin seam", err)
	}
	err = tx.Commit(f.ctx)
	var failure *pgconn.PgError
	if !errors.As(err, &failure) || failure.Code != "23514" {
		t.Fatal("unfinished operation committed without actual effects/receipt", err)
	}
	var pending int
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*) FROM pantry.leftover_operations WHERE state='pending'").Scan(&pending); err != nil || pending != 0 {
		t.Fatal("failed completion left a pending row", pending, err)
	}
}

func TestPantryLeftoversSQLCompletionRejectsMalformedSharedPreparation(t *testing.T) {
	f := newIntegrationFixture(t)
	in := pantryLeftoverFixture(t, f)
	product, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Gemeinsames Portionsprodukt", "unit_code": "portion"})
	cases := []struct {
		name string
		edit func(store.Record)
	}{
		{"null preparation date", func(r store.Record) { r["prepared_on"] = nil }},
		{"different preparation date", func(r store.Record) { r["prepared_on"] = "2000-01-01" }},
		{"different dish name", func(r store.Record) { r["name"] = "Unzutreffendes Gericht" }},
		{"null dish name", func(r store.Record) { r["name"] = nil }},
		{"string generation", func(r store.Record) { r["generation"] = "1" }},
		{"false detail flag", func(r store.Record) { r["details_available"] = false }},
		{"null detail flag", func(r store.Record) { r["details_available"] = nil }},
		{"null accepted time", func(r store.Record) { r["created_at"] = nil }},
		{"future accepted time", func(r store.Record) { r["created_at"] = "9999-12-31T23:59:59Z" }},
		{"number line quantity", func(r store.Record) { r["lines"].([]store.Record)[0]["quantity"] = json.Number("1.250001") }},
		{"actual typed completion", nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
			tx, err := f.app.Store.BeginMutation(ctx, f.identity, "pantry", in.Generation)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(f.ctx) }()
			op, key, lot, movement, line := value.UUID(), value.UUID(), value.UUID(), value.UUID(), value.UUID()
			if _, err = tx.Exec(ctx, "SELECT pantry.begin_leftovers($1,$2,$3,decode(repeat('01',32),'hex'),'manual','',0,'',$4,$5,$6)", op, key, in.Generation, f.identity.SessionID, f.identity.AuthVersion, f.identity.MembershipID); err != nil {
				t.Fatal("actual App pending-operation protocol", err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO pantry.lots(id,household_id,product_id,location_id,received_on,notes,created_by)
 VALUES($1,$2,$3,$4,$5::date,$6,$7)`, lot, f.identity.HouseholdID, product["id"], in.Containers[0].LocationID, in.PreparedOn, in.Containers[0].Label, f.identity.UserID); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO pantry.stock_movements(id,household_id,lot_id,quantity_delta,reason,correlation_id,created_by)
 VALUES($1,$2,$3,1.250001,'receive',$4,$5)`, movement, f.identity.HouseholdID, lot, op, f.identity.UserID); err != nil {
				t.Fatal(err)
			}
			var location string
			if err = tx.QueryRow(ctx, "SELECT name FROM pantry.locations WHERE household_id=$1 AND id=$2", f.identity.HouseholdID, in.Containers[0].LocationID).Scan(&location); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO pantry.prepared_lots(household_id,lot_id,operation_id,line_id,movement_id,prepared_on,
 initial_portions,prepared_portions,portions_unknown,source_kind,dish_name,label,location_id,location_name,received_on)
 VALUES($1,$2,$3,$4,$5,$6::date,1.250001,NULL,true,'manual',$7,$8,$9,$10,$6::date)`, f.identity.HouseholdID, lot, op, line, movement, in.PreparedOn, in.Name, in.Containers[0].Label, in.Containers[0].LocationID, location); err != nil {
				t.Fatal("genuine prepared row and journal evidence", err)
			}
			var recorded time.Time
			if err = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&recorded); err != nil {
				t.Fatal(err)
			}
			result := store.Record{"id": op, "operation_id": op, "request_key": key, "generation": in.Generation,
				"name": in.Name, "prepared_on": in.PreparedOn, "total_portions": "1.250001", "unit": "portion",
				"created_at": recorded.UTC().Format(time.RFC3339Nano), "details_available": true,
				"lines": []store.Record{{"line_id": line, "lot_id": lot, "movement_id": movement, "label": in.Containers[0].Label,
					"quantity": "1.250001", "location_id": in.Containers[0].LocationID, "location": location}}}
			if test.edit != nil {
				test.edit(result)
			}
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(ctx, "SELECT pantry.finish_leftovers($1,$2::jsonb)", op, raw)
			if test.edit != nil {
				var failure *pgconn.PgError
				if !errors.As(err, &failure) || failure.Code != "23514" {
					t.Fatal("malformed receipt sealed despite real effects", err)
				}
				if rollbackErr := tx.Rollback(f.ctx); rollbackErr != nil {
					t.Fatal(rollbackErr)
				}
				var effects int
				if err = f.owner.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM pantry.prepared_lots)+(SELECT count(*) FROM pantry.leftover_operations)+(SELECT count(*) FROM pantry.stock_movements)").Scan(&effects); err != nil || effects != 0 {
					t.Fatal("refused completion retained partial evidence", effects, err)
				}
				return
			}
			if err != nil {
				t.Fatal("actual typed receipt was refused", err)
			}
			if err = tx.Commit(f.ctx); err != nil {
				t.Fatal("actual App protocol completion did not commit", err)
			}
		})
	}
}

func pantryLeftoverTransfer(t *testing.T, f *integrationFixture, source, location, quantity string) string {
	t.Helper()
	m, _ := f.app.Store.Module("pantry")
	e, _ := m.Entity("lots")
	current, err := f.app.Store.Get(f.ctx, f.identity, m, e, source)
	if err != nil {
		t.Fatal("current actual transfer source", err)
	}
	in := auditMutation(t, f, "pantry", store.Record{"location_id": location, "quantity": quantity}, store.Int(current["version"]))
	if _, err = f.app.Store.Action(f.ctx, f.identity, m, e, source, "transfer", value.UUID(), value.UUID(), in); err != nil {
		t.Fatal("actual reviewed stock transfer", err)
	}
	var target string
	if err = f.pool.QueryRow(f.ctx, "SELECT lot_id FROM pantry.prepared_transfers WHERE household_id=$1 AND source_lot_id=$2", f.identity.HouseholdID, source).Scan(&target); err != nil {
		t.Fatal("actual prepared descendant is missing", err)
	}
	return target
}

func TestPantryLeftoversTransfersCorrectionsAndRepeatedHopsPreservePreparation(t *testing.T) {
	f := newIntegrationFixture(t)
	_, cooking, _ := cookingStockFixture(t, f)
	in := pantryLeftoverFixture(t, f)
	context, err := f.app.Store.PantryLeftoversContext(f.ctx, f.identity, "cooking", cooking)
	if err != nil {
		t.Fatal(err)
	}
	in.SourceKind, in.SourceID, in.RecipesGeneration = "cooking", cooking, store.Int(context["recipes_generation"])
	sharedProduct, _ := auditCreate(t, f, "pantry", "products", store.Record{"name": "Allgemeines Portionsprodukt", "unit_code": "portion", "integer_quantities": false})
	in.ProductID, in.ProductVersion = store.String(sharedProduct["id"]), store.Int(sharedProduct["version"])
	in.Name, in.Containers[0].Label = "Karottencremesuppe", "Blaue Dose"
	in.Containers[0].Quantity = "0.75"
	in = pantryLeftoverReview(t, f, in)
	key := value.UUID()
	receipt, err := f.app.Store.PostPantryLeftovers(f.ctx, f.identity, key, value.UUID(), in)
	if err != nil {
		t.Fatal("actual original preparation", err)
	}
	root := store.String(receipt["lines"].([]store.Record)[0]["lot_id"])
	freezer, _ := auditCreate(t, f, "pantry", "locations", store.Record{"name": "Restetiefkühler"})
	travel, _ := auditCreate(t, f, "pantry", "locations", store.Record{"name": "Restetransport"})
	first := pantryLeftoverTransfer(t, f, root, store.String(freezer["id"]), "0.5")
	// An explicit stock correction changes the actual balance, never the
	// original preparation receipt or the transfer's initial quantity.
	auditCreate(t, f, "pantry", "stock_movements", store.Record{"lot_id": first, "reason": "adjust", "quantity_delta": "0.1"})
	second := pantryLeftoverTransfer(t, f, first, store.String(travel["id"]), "0.25")
	// Correcting a transfer uses the existing counter-transfer workflow and
	// creates a descendant, rather than rewriting either immutable pair.
	third := pantryLeftoverTransfer(t, f, second, store.String(freezer["id"]), "0.1")
	for _, expected := range []struct{ lot, initial, stock, location string }{
		{root, "0.750000", "0.250000", in.Containers[0].LocationID},
		{first, "0.500000", "0.350000", store.String(freezer["id"])},
		{second, "0.250000", "0.150000", store.String(travel["id"])},
		{third, "0.100000", "0.100000", store.String(freezer["id"])},
	} {
		view, readErr := f.app.Store.PantryPreparedLot(f.ctx, f.identity, expected.lot)
		if readErr != nil {
			t.Fatal("prepared descendant detail", readErr)
		}
		prepared, source, lot := view["prepared"].(store.Record), view["source"].(store.Record), view["lot"].(store.Record)
		if prepared["prepared_on"] != in.PreparedOn || prepared["root_lot_id"] != root ||
			prepared["dish_name"] != in.Name || lot["product_name"] != "Allgemeines Portionsprodukt" || lot["label"] != "Blaue Dose" ||
			prepared["initial_portions"] != expected.initial || prepared["root_initial_portions"] != "0.750000" ||
			prepared["prepared_portions"] != "1.000000" || source["id"] != cooking || source["available"] != true ||
			lot["stock"] != expected.stock || lot["location_id"] != expected.location {
			t.Fatal("preparation/source or current target balance/location was lost", view)
		}
	}
	var originals, descendants int
	var total string
	if err = f.pool.QueryRow(f.ctx, `SELECT (SELECT count(*) FROM pantry.prepared_lots),
 (SELECT count(*) FROM pantry.prepared_transfers),(SELECT sum(initial_portions)::text FROM pantry.prepared_lots)`).Scan(&originals, &descendants, &total); err != nil || originals != 1 || descendants != 3 || total != "0.750000" {
		t.Fatal("transfer inflated original preparation/portion allocation", originals, descendants, total, err)
	}
	replay, err := f.app.Store.PostPantryLeftovers(f.ctx, f.identity, key, value.UUID(), in)
	before, _ := json.Marshal(receipt)
	after, _ := json.Marshal(replay)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("legitimate transfers/corrections invalidated the original receipt", replay, err)
	}
	if err = database.Ready(f.ctx, f.pool); err != nil {
		t.Fatal("transfer guard/audit/catalog readiness", err)
	}
}

func TestPantryLeftoversTransferLineageRefusesForeignOrUnmatchedEvidence(t *testing.T) {
	f := newIntegrationFixture(t)
	in := pantryLeftoverReview(t, f, pantryLeftoverFixture(t, f))
	receipt, err := f.app.Store.PostPantryLeftovers(f.ctx, f.identity, value.UUID(), value.UUID(), in)
	if err != nil {
		t.Fatal(err)
	}
	root := store.String(receipt["lines"].([]store.Record)[0]["lot_id"])
	destination, _ := auditCreate(t, f, "pantry", "locations", store.Record{"name": "Weiterer Resteort"})
	target := pantryLeftoverTransfer(t, f, root, store.String(destination["id"]), "0.5")
	var before, after string
	snapshot := `SELECT jsonb_build_array((SELECT count(*) FROM pantry.lots),(SELECT count(*) FROM pantry.stock_movements),
 (SELECT count(*) FROM pantry.prepared_lots),(SELECT count(*) FROM pantry.prepared_transfers))::text`
	if err = f.pool.QueryRow(f.ctx, snapshot).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// The runtime role cannot fabricate a lineage row, including a foreign HH
	// or root. Only the narrow actual-session helper owns INSERT authority.
	_, err = f.pool.Exec(f.ctx, `INSERT INTO pantry.prepared_transfers(household_id,lot_id,root_lot_id,source_lot_id,
 movement_out_id,movement_in_id,correlation_id,quantity,created_by) VALUES($1,$2,$3,$3,$4,$5,$6,1,$7)`,
		value.UUID(), target, root, value.UUID(), value.UUID(), value.UUID(), f.identity.UserID)
	var failure *pgconn.PgError
	if !errors.As(err, &failure) || failure.Code != "42501" {
		t.Fatal("runtime manufactured foreign lineage", err)
	}
	for _, test := range []struct{ source, target, correlation, quantity string }{
		{root, target, value.UUID(), "0.5"},
		{root, value.UUID(), value.UUID(), "0.5"},
		{root, target, value.UUID(), "0.5000001"},
	} {
		ctx := audit.WithRequest(audit.WithActor(f.ctx, f.identity.UserID, f.identity.HouseholdID), value.UUID())
		tx, beginErr := f.app.Store.BeginMutation(ctx, f.identity, "pantry", in.Generation)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		_, callErr := tx.Exec(ctx, "SELECT pantry.record_prepared_transfer($1,$2,$3,$4::numeric,$5,$6,$7,$8)",
			test.source, test.target, test.correlation, test.quantity, in.Generation, f.identity.SessionID, f.identity.AuthVersion, f.identity.MembershipID)
		_ = tx.Rollback(f.ctx)
		if !errors.As(callErr, &failure) || failure.Code != "23514" {
			t.Fatal("unmatched or rounded transfer evidence was accepted", callErr)
		}
	}
	foreign := f.identity
	foreign.HouseholdID = value.UUID()
	if _, err = f.app.Store.PantryPreparedLot(f.ctx, foreign, target); !errors.Is(err, store.ErrForbidden) {
		t.Fatal("foreign household read prepared lineage", err)
	}
	m, _ := f.app.Store.Module("pantry")
	e, _ := m.Entity("lots")
	current, err := f.app.Store.Get(f.ctx, f.identity, m, e, root)
	if err != nil {
		t.Fatal(err)
	}
	product := store.String(current["product_id"])
	sources, err := f.app.Store.PantryFoodFactsExportSources(f.ctx, f.identity, "products", []string{product})
	if err != nil || sources[product] != "[]" {
		t.Fatal("reviewed current Pantry source export was unavailable or invented attribution", sources, err)
	}
	input := auditMutation(t, f, "pantry", store.Record{"location_id": destination["id"], "quantity": "0.1"}, store.Int(current["version"]))
	version := database.ExpectedVersion("pantry")
	if _, err = f.owner.Exec(f.ctx, "UPDATE pantry.schema_migrations SET version=$1", version+1); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = f.owner.Exec(f.ctx, "UPDATE pantry.schema_migrations SET version=$1", version) }()
	_, transferErr := f.app.Store.Action(f.ctx, f.identity, m, e, root, "transfer", value.UUID(), value.UUID(), input)
	futureSources, sourceErr := f.app.Store.PantryFoodFactsExportSources(f.ctx, f.identity, "products", []string{product})
	if _, err = f.owner.Exec(f.ctx, "UPDATE pantry.schema_migrations SET version=$1", version); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(transferErr, store.ErrConflict) || !errors.Is(sourceErr, store.ErrConflict) || futureSources != nil {
		t.Fatal("unreviewed future Pantry version accepted a transfer or source export", transferErr, sourceErr, futureSources)
	}
	if _, err = f.owner.Exec(f.ctx, "UPDATE pantry.schema_migrations SET dirty=true"); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = f.owner.Exec(f.ctx, "UPDATE pantry.schema_migrations SET dirty=false") }()
	_, dirtyTransferErr := f.app.Store.Action(f.ctx, f.identity, m, e, root, "transfer", value.UUID(), value.UUID(), input)
	dirtySources, dirtySourceErr := f.app.Store.PantryFoodFactsExportSources(f.ctx, f.identity, "products", []string{product})
	if _, err = f.owner.Exec(f.ctx, "UPDATE pantry.schema_migrations SET dirty=false"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(dirtyTransferErr, store.ErrConflict) || !errors.Is(dirtySourceErr, store.ErrConflict) || dirtySources != nil {
		t.Fatal("dirty current Pantry accepted a transfer or source export", dirtyTransferErr, dirtySourceErr, dirtySources)
	}
	if err = f.pool.QueryRow(f.ctx, snapshot).Scan(&after); err != nil || before != after {
		t.Fatal("refused lineage changed lots/journal/preparation/descendants", before, after, err)
	}
}
