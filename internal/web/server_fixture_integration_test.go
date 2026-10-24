//go:build integration

package web

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"household/internal/auth"
	"household/internal/config"
	"household/internal/database"
	"household/internal/modules"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const integrationPassword = "Integration fixture 1234"

type integrationFixture struct {
	ctx      context.Context
	app      *Server
	pool     *pgxpool.Pool
	owner    *pgxpool.Pool
	server   *httptest.Server
	client   *http.Client
	token    string
	identity auth.Identity
}

// Each fixture owns the complete, explicitly named disposable database. This is
// intentionally serial: separate workflows must never reset one another's data.
func newIntegrationFixture(t *testing.T) *integrationFixture {
	t.Helper()
	ownerURL, appURL := os.Getenv("HOUSEHOLD_TEST_OWNER_URL"), os.Getenv("HOUSEHOLD_TEST_APP_URL")
	if ownerURL == "" || appURL == "" {
		if os.Getenv("HOUSEHOLD_REQUIRE_INTEGRATION") == "1" {
			t.Fatal("integration tests require both HOUSEHOLD_TEST_OWNER_URL and HOUSEHOLD_TEST_APP_URL")
		}
		t.Skip("isolated PostgreSQL test URLs required; set HOUSEHOLD_REQUIRE_INTEGRATION=1 to require execution")
	}
	if err := validateIntegrationURLs(ownerURL, appURL); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	t.Cleanup(cancel)
	owner, err := database.Open(ctx, ownerURL, 3)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	pool, err := database.Open(ctx, appURL, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Validate the actual server-side database and role before destructive setup.
	for _, check := range []struct {
		pool *pgxpool.Pool
		role string
	}{{owner, "household_owner"}, {pool, "household_app"}} {
		var name, role string
		if err = check.pool.QueryRow(ctx, "SELECT current_database(),current_user").Scan(&name, &role); err != nil || name != "household_test" || role != check.role {
			pool.Close()
			t.Fatal("refusing integration setup outside the isolated household_test database and expected roles")
		}
	}
	lock, err := owner.Acquire(ctx)
	if err != nil {
		pool.Close()
		t.Fatal(err)
	}
	var locked bool
	if err = lock.QueryRow(ctx, "SELECT pg_try_advisory_lock(726241999)").Scan(&locked); err != nil || !locked {
		lock.Release()
		pool.Close()
		t.Fatal("isolated database is already in use by another integration fixture")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := lock.Exec(cleanup, "SELECT pg_advisory_unlock(726241999)"); err != nil {
			t.Errorf("release integration lock: %v", err)
		}
		lock.Release()
	})
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if err := resetIntegrationSchemas(cleanup, owner); err != nil {
			t.Errorf("clean integration schemas: %v", err)
		}
	})
	t.Cleanup(pool.Close)
	if err = resetIntegrationSchemas(ctx, owner); err != nil {
		t.Fatal(err)
	}
	if err = database.Migrate(ctx, ownerURL); err != nil {
		t.Fatal(err)
	}
	if err = database.Migrate(ctx, ownerURL); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	cfg := config.Config{
		DatabaseURL: appURL, Origin: "http://localhost:8100",
		CSRFSecret: auth.RandomToken(), EncryptionKey: auth.RandomToken(), RateSecret: auth.RandomToken(),
		AdminUsername: "bootstrap", AdminPassword: integrationPassword, HouseholdName: "Integration",
		SessionIdle: 24 * time.Hour, SessionAbsolute: 30 * 24 * time.Hour,
		UploadDir: t.TempDir(), UploadQuota: 2 << 30,
	}
	app, err := New(ctx, pool, cfg, modules.Registry(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = owner.Exec(ctx, "UPDATE core.users SET must_change_password=false WHERE username=$1", cfg.AdminUsername); err != nil {
		t.Fatal(err)
	}
	token, identity, err := app.Auth.Login(ctx, cfg.AdminUsername, integrationPassword, "", "127.0.0.1", "Integration")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)
	app.Cfg.Origin = server.URL
	client := &http.Client{
		Timeout:       15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	t.Cleanup(client.CloseIdleConnections)
	return &integrationFixture{ctx: ctx, app: app, pool: pool, owner: owner, server: server, client: client, token: token, identity: identity}
}

func validateIntegrationURLs(ownerURL, appURL string) error {
	owner, ownerErr := pgx.ParseConfig(ownerURL)
	app, appErr := pgx.ParseConfig(appURL)
	if ownerErr != nil || appErr != nil {
		return fmt.Errorf("invalid integration database URLs")
	}
	if owner.Database != "household_test" || app.Database != "household_test" || owner.User != "household_owner" || app.User != "household_app" {
		return fmt.Errorf("integration tests require household_test and separate household_owner/household_app roles")
	}
	if owner.Host != app.Host || owner.Port != app.Port {
		return fmt.Errorf("integration owner and app must use the same PostgreSQL endpoint")
	}
	return nil
}

func resetIntegrationSchemas(ctx context.Context, pool *pgxpool.Pool) error {
	names := make([]string, 0, len(database.Schemas))
	for _, schema := range database.Schemas {
		names = append(names, pgx.Identifier{schema}.Sanitize())
	}
	// Identifiers come only from the application's compiled schema allowlist.
	_, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+strings.Join(names, ",")+" CASCADE")
	return err
}

type integrationResponse struct {
	StatusCode int
	Header     http.Header
}

func (r integrationResponse) Cookies() []*http.Cookie {
	return (&http.Response{Header: r.Header}).Cookies()
}

func (f *integrationFixture) request(method, path, token, csrf, contentType string, body io.Reader, headers map[string]string) (*integrationResponse, []byte, error) {
	request, err := http.NewRequestWithContext(f.ctx, method, f.server.URL+path, body)
	if err != nil {
		return nil, nil, err
	}
	if token != "" {
		request.AddCookie(&http.Cookie{Name: f.app.cookieName(), Value: token})
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("X-CSRF-Token", csrf)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := f.client.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	return &integrationResponse{StatusCode: response.StatusCode, Header: response.Header.Clone()}, data, err
}

func TestIntegrationURLGuard(t *testing.T) {
	const owner = "postgres://household_owner:fixture@localhost:55434/household_test?sslmode=disable"
	const app = "postgres://household_app:fixture@localhost:55434/household_test?sslmode=disable"
	for _, tc := range []struct {
		name, owner, app string
		wantError        bool
	}{
		{"isolated", owner, app, false},
		{"production_database", strings.Replace(owner, "/household_test", "/household", 1), app, true},
		{"query_database_override", owner + "&dbname=household", app, true},
		{"different_server", owner, strings.Replace(app, ":55434", ":5432", 1), true},
		{"superuser", strings.Replace(owner, "household_owner:", "postgres:", 1), app, true},
		{"shared_role", owner, strings.Replace(app, "household_app:", "household_owner:", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateIntegrationURLs(tc.owner, tc.app); (err != nil) != tc.wantError {
				t.Fatalf("validation error = %v, want error %t", err, tc.wantError)
			}
		})
	}
}
