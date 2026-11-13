//go:build integration

package maintenance

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/config"
	"household/internal/secretbox"
	"household/internal/value"

	"github.com/jackc/pgx/v5"
)

func maintenanceRotationRing(t *testing.T) (*secretbox.Keyring, *secretbox.Keyring, string) {
	t.Helper()
	oldID, oldSecret, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	newID, newSecret, err := secretbox.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	old, err := secretbox.New(oldSecret)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"version": 1, "active": newID, "legacy": oldID, "keys": []map[string]string{{"id": oldID, "secret": oldSecret}, {"id": newID, "secret": newSecret}}})
	if err != nil {
		t.Fatal(err)
	}
	ring, err := secretbox.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	return old, ring, oldSecret
}

func maintenanceLegacyCipher(t *testing.T, secret string) []byte {
	t.Helper()
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	return aead.Seal(nonce, nonce, []byte("private-maintenance-synthetic-protected-value"), []byte("household-v1"))
}

func seedMaintenanceProtected(t *testing.T, f restoreBarcodeFixture, old *secretbox.Keyring, secret string) (string, string) {
	t.Helper()
	hh, user := value.UUID(), value.UUID()
	ctx := audit.WithRequest(audit.WithSystem(f.ctx, "bootstrap"), value.UUID())
	tx, err := audit.Begin(ctx, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackCrypto(tx)
	if _, err = tx.Exec(ctx, "INSERT INTO core.households(id,name) VALUES($1,'Synthetic crypto maintenance household')", hh); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO core.users(id,username,display_name,password_hash,must_change_password) VALUES($1,'crypto-fixture','Synthetic crypto account','synthetic-non-login-hash',false)", user); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO core.memberships(household_id,user_id,role) VALUES($1,$2,'admin')", hh, user); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "INSERT INTO core.totp_credentials(user_id,encrypted_secret,encryption_format,encryption_key_id,last_accepted_counter,enabled_at) VALUES($1,$2,1,'v1',42,clock_timestamp())", user, maintenanceLegacyCipher(t, secret)); err != nil {
		t.Fatal(err)
	}
	credential := value.UUID()
	if _, err = tx.Exec(ctx, "INSERT INTO core.passkey_credentials(id,user_id,credential_id,encrypted_credential,encryption_format,encryption_key_id,name) VALUES($1,$2,$3,$4,1,'v1','Synthetic passkey')", credential, user, []byte("synthetic-credential-id"), maintenanceLegacyCipher(t, secret)); err != nil {
		t.Fatal(err)
	}
	for _, purpose := range []string{"passkey-login", "passkey-register", "passkey-reauth"} {
		uid := user
		if purpose == "passkey-login" {
			uid = ""
		}
		payload, err := json.Marshal(map[string][]byte{"sealed": maintenanceLegacyCipher(t, secret)})
		if err != nil {
			t.Fatal(err)
		}
		token := sha256.Sum256([]byte(value.UUID()))
		if _, err = tx.Exec(ctx, "INSERT INTO core.auth_challenges(id,user_id,purpose,token_hash,payload,expires_at,encryption_format,encryption_key_id) VALUES($1,nullif($2,'')::uuid,$3,$4,$5,clock_timestamp()+interval '3 minutes',1,'v1')", value.UUID(), uid, purpose, token[:], payload); err != nil {
			t.Fatal(err)
		}
	}
	endpoint := sha256.Sum256([]byte("synthetic-no-provider"))
	if _, err = tx.Exec(ctx, "INSERT INTO core.push_subscriptions(household_id,user_id,endpoint_hash,encrypted_subscription,encryption_format,encryption_key_id) VALUES($1,$2,$3,$4,1,$5)", hh, user, endpoint[:], maintenanceLegacyCipher(t, secret), old.LegacyID()); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return user, credential
}

func maintenanceSemanticFacts(t *testing.T, f restoreBarcodeFixture) string {
	t.Helper()
	var facts string
	err := f.owner.QueryRow(f.ctx, `SELECT jsonb_build_object(
 'totp',(SELECT jsonb_agg(to_jsonb(t)-'encrypted_secret'-'encryption_format'-'encryption_key_id' ORDER BY user_id) FROM core.totp_credentials t),
 'passkeys',(SELECT jsonb_agg(to_jsonb(t)-'encrypted_credential'-'encryption_format'-'encryption_key_id' ORDER BY id) FROM core.passkey_credentials t),
 'challenges',(SELECT jsonb_agg(to_jsonb(t)-'payload'-'encryption_format'-'encryption_key_id' ORDER BY id) FROM core.auth_challenges t),
 'push',(SELECT jsonb_agg(to_jsonb(t)-'encrypted_subscription'-'encryption_format'-'encryption_key_id' ORDER BY id) FROM core.push_subscriptions t))::text`).Scan(&facts)
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func TestCryptoMaintenanceResumableOwnerRotationAndRetirement(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	old, ring, secret := maintenanceRotationRing(t)
	_, credential := seedMaintenanceProtected(t, f, old, secret)
	cfg := config.Config{EncryptionKeys: ring}
	before := maintenanceSemanticFacts(t, f)
	if _, err := Reencrypt(f.ctx, f.app, cfg, 2); err == nil {
		t.Fatal("runtime role rotated protected state")
	}
	lock, err := f.owner.Acquire(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.Exec(f.ctx, "SELECT pg_advisory_lock($1)", cryptoMaintenanceLock); err != nil {
		lock.Release()
		t.Fatal(err)
	}
	_, lockedErr := Reencrypt(f.ctx, f.owner, cfg, 2)
	_, unlockErr := lock.Exec(f.ctx, "SELECT pg_advisory_unlock($1)", cryptoMaintenanceLock)
	lock.Release()
	if lockedErr == nil || unlockErr != nil {
		t.Fatal("singleton rotation ownership failed", lockedErr, unlockErr)
	}
	for i := 0; i < 3; i++ {
		report, err := Reencrypt(f.ctx, f.owner, cfg, 2)
		if err != nil || report.Changed != 2 {
			t.Fatal("bounded rotation", i, report, err)
		}
	}
	if report, err := Reencrypt(f.ctx, f.owner, cfg, 2); err != nil || report.Changed != 0 {
		t.Fatal("completed rotation replay changed state", report, err)
	}
	if after := maintenanceSemanticFacts(t, f); after != before {
		t.Fatal("rotation changed counter, ceremony, consent or lifecycle facts")
	}
	report, err := CheckKeyRetirement(f.ctx, f.owner, cfg, old.LegacyID())
	if err != nil || report.LiveReferences != 0 || report.VerifiedRows != 6 || report.RetirementApproved || !report.BackupRetentionReviewRequired {
		t.Fatal("retirement evidence", report, err)
	}
	if _, err = CheckKeyRetirement(f.ctx, f.owner, config.Config{EncryptionKeys: old}, old.LegacyID()); !errors.Is(err, secretbox.ErrKeyUnavailable) {
		t.Fatal("missing retained key did not fail closed", err)
	}
	var events int
	var evidence string
	if err = f.owner.QueryRow(f.ctx, "SELECT count(*),coalesce(jsonb_agg(details)::text,'[]') FROM core.audit_log WHERE resource_type='crypto_rotation' AND action='record_write'").Scan(&events, &evidence); err != nil || events != 6 || bytes.Contains([]byte(evidence), []byte("private-maintenance")) {
		t.Fatal("redacted atomic rotation audit", events, err)
	}
	var sealed []byte
	if err = f.owner.QueryRow(f.ctx, "SELECT encrypted_credential FROM core.passkey_credentials WHERE id=$1", credential).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err = audit.Exec(audit.WithSystem(f.ctx, "operator"), f.owner, "UPDATE core.passkey_credentials SET encrypted_credential=$2 WHERE id=$1", credential, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err = CheckKeyRetirement(f.ctx, f.owner, cfg, old.LegacyID()); err == nil {
		t.Fatal("corrupt ciphertext accepted as verified inventory")
	}
}

func TestCryptoMaintenanceExportedSnapshotAndFailedBatchRollback(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	old, ring, secret := maintenanceRotationRing(t)
	_, credential := seedMaintenanceProtected(t, f, old, secret)
	cfg := config.Config{EncryptionKeys: ring}
	tx, err := f.owner.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackCrypto(tx)
	first, err := verifyProtected(f.ctx, tx, ring)
	if err != nil || first.Examined != 6 {
		t.Fatal("snapshot inventory", first, err)
	}
	var snapshot string
	if err = tx.QueryRow(f.ctx, "SELECT pg_export_snapshot()").Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	var original []byte
	if err = f.owner.QueryRow(f.ctx, "SELECT encrypted_credential FROM core.passkey_credentials WHERE id=$1", credential).Scan(&original); err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), original...)
	corrupt[len(corrupt)-1] ^= 1
	if _, err = audit.Exec(audit.WithSystem(f.ctx, "operator"), f.owner, "UPDATE core.passkey_credentials SET encrypted_credential=$2 WHERE id=$1", credential, corrupt); err != nil {
		t.Fatal(err)
	}
	// A separate pg_dump-style importer sees exactly the exported inventory,
	// despite an authentication write committing after snapshot acquisition.
	imported, err := f.app.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackCrypto(imported)
	if _, err = imported.Exec(f.ctx, "SET TRANSACTION SNAPSHOT "+quoteSnapshot(t, snapshot)); err != nil {
		t.Fatal(err)
	}
	second, err := verifyProtected(f.ctx, imported, ring)
	if err != nil || second.Examined != first.Examined {
		t.Fatal("imported dump snapshot diverged", second, err)
	}
	if err = imported.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = Reencrypt(f.ctx, f.owner, cfg, 100); err == nil {
		t.Fatal("corrupt batch committed")
	}
	var rotated, audits int
	if err = f.owner.QueryRow(f.ctx, "SELECT (SELECT count(*) FROM core.totp_credentials WHERE encryption_format=2),(SELECT count(*) FROM core.audit_log WHERE resource_type='crypto_rotation')").Scan(&rotated, &audits); err != nil || rotated != 0 || audits != 0 {
		t.Fatal("failed batch left partial rotation or audit", rotated, audits, err)
	}
	cancelled, cancel := context.WithCancel(f.ctx)
	cancel()
	if _, err = Reencrypt(cancelled, f.owner, cfg, 1); err == nil {
		t.Fatal("cancelled rotation ignored context")
	}
	cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if _, err = audit.Exec(audit.WithSystem(cleanup, "operator"), f.owner, "UPDATE core.passkey_credentials SET encrypted_credential=$2 WHERE id=$1", credential, original); err != nil {
		t.Fatal(err)
	}
}

func quoteSnapshot(t *testing.T, value string) string {
	t.Helper()
	// The exported snapshot identifier is server-owned. SET TRANSACTION does
	// not accept placeholders; admit only PostgreSQL's hexadecimal ID alphabet.
	if len(value) < 1 || len(value) > 100 {
		t.Fatal("invalid exported snapshot length")
	}
	for _, r := range value {
		if r != '-' && (r < '0' || r > '9') && (r < 'A' || r > 'F') && (r < 'a' || r > 'f') {
			t.Fatal("invalid exported snapshot alphabet")
		}
	}
	return "'" + value + "'"
}

func TestCryptoMaintenanceRejectsZeroIdentityWithoutSkipping(t *testing.T) {
	f := newRestoreBarcodeFixture(t)
	old, ring, secret := maintenanceRotationRing(t)
	user, _ := seedMaintenanceProtected(t, f, old, secret)
	cfg := config.Config{EncryptionKeys: ring}
	if _, err := audit.Exec(audit.WithSystem(f.ctx, "operator"), f.owner, `INSERT INTO core.passkey_credentials
 (id,user_id,credential_id,encrypted_credential,encryption_format,encryption_key_id,name)
 VALUES('00000000-0000-0000-0000-000000000000',$1,'\x000102',$2,1,'v1','Synthetic zero identity')`,
		user, maintenanceLegacyCipher(t, secret)); err != nil {
		t.Fatal("seed permitted historical zero record identity", err)
	}
	// Backup, restore and retirement share this complete inventory. A valid
	// ciphertext on an invalid record must make verification fail closed.
	if _, err := verifyProtected(f.ctx, f.owner, ring); !errors.Is(err, secretbox.ErrInvalidBinding) {
		t.Fatal("inventory skipped zero record instead of rejecting its binding", err)
	}
	if _, err := CheckKeyRetirement(f.ctx, f.owner, cfg, old.LegacyID()); !errors.Is(err, secretbox.ErrInvalidBinding) {
		t.Fatal("retirement ignored an unreadable protected zero record", err)
	}
	if _, err := Reencrypt(f.ctx, f.owner, cfg, 100); !errors.Is(err, secretbox.ErrInvalidBinding) {
		t.Fatal("rotation skipped zero record instead of rejecting its binding", err)
	}
	var rotated, audits int
	err := f.owner.QueryRow(f.ctx, `SELECT
 (SELECT count(*) FROM core.totp_credentials WHERE encryption_format=2),
 (SELECT count(*) FROM core.audit_log WHERE resource_type='crypto_rotation')`).Scan(&rotated, &audits)
	if err != nil || rotated != 0 || audits != 0 {
		t.Fatal("failed zero-identity rotation committed earlier rows or audit", rotated, audits, err)
	}
}
