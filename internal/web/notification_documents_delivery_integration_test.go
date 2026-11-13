//go:build integration && testtools

package web

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"household/internal/audit"
	"household/internal/auth"
	"household/internal/notification"
	"household/internal/store"
	"household/internal/value"
	"household/internal/webpush"

	"golang.org/x/crypto/hkdf"
)

type notificationDocumentDelivery struct {
	actor                                      auth.Identity
	document, resource, intent, delivery, kind string
	connection                                 string
	input                                      webpush.RegisterInput
	key                                        *ecdh.PrivateKey
}

func notificationDocumentWebWrite(t *testing.T, f *integrationFixture, actor auth.Identity, action, id string, version int64, data store.Record) store.Record {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.app.Store.DocumentWrite(f.ctx, actor, value.UUID(), value.UUID(), store.DocumentCommand{
		Generation: f.app.Store.States()["documents"].Generation, Action: action, ID: id, Version: version, Data: raw,
	})
	if err != nil {
		t.Fatal("actual document command", action, err)
	}
	return out
}

func notificationDocumentWebScan(t *testing.T, f *integrationFixture) {
	t.Helper()
	// Advance only the isolated scanner's scheduling clock. Source, consent and
	// event timestamps come from actual writes and the server-owned triggers.
	if _, err := f.owner.Exec(f.ctx, `UPDATE core.jobs SET available_at=clock_timestamp() WHERE household_id=$1 AND kind=$2`,
		f.identity.HouseholdID, store.DocumentNotificationScanJobKind); err != nil {
		t.Fatal(err)
	}
	for range 40 {
		n, err := f.app.Store.StageDocumentNotifications(f.ctx, 50)
		if err != nil {
			t.Fatal("actual document scanner", err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("document scanner exceeded bounded work")
}

func notificationDocumentEnroll(t *testing.T, f *integrationFixture, service *webpush.Service, actor auth.Identity) notificationDocumentDelivery {
	t.Helper()
	q := notificationDocumentDelivery{actor: actor}
	var err error
	q.key, err = ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal("generate controlled document receiver")
	}
	authKey := make([]byte, 16)
	if _, err = rand.Read(authKey); err != nil {
		t.Fatal("generate controlled receiver authentication")
	}
	q.input = webpush.RegisterInput{DeviceID: value.UUID(), VAPIDKeyID: *service.Configuration().VAPIDKeyID,
		Subscription: webpush.Receiver{Endpoint: "https://fcm.googleapis.com/controlled-document/" + value.UUID(),
			Keys: webpush.ReceiverKeys{P256DH: base64.RawURLEncoding.EncodeToString(q.key.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString(authKey)}}}
	if _, err = f.app.Store.SaveNotificationPreference(f.ctx, actor, store.NotificationPreference{
		Category: "documents", Enabled: true, QuietHours: notification.QuietHours{Timezone: "UTC"},
	}); err != nil {
		t.Fatal(err)
	}
	connection, err := service.Register(f.ctx, actor, q.input)
	if err != nil {
		t.Fatal(err)
	}
	q.connection = connection.ID
	if _, err = service.SaveDevicePreference(f.ctx, actor, connection.ID, webpush.DevicePreference{Category: "documents", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	return q
}

func notificationDocumentQueue(t *testing.T, f *integrationFixture, service *webpush.Service, kind, suffix string) notificationDocumentDelivery {
	t.Helper()
	role := "editor"
	if kind == "import" {
		role = "admin"
	}
	q := notificationDocumentEnroll(t, f, service, shoppingTestMember(t, f, "document-"+kind+"-"+suffix, role))
	q.kind = kind
	generation := f.app.Store.States()["documents"].Generation
	if kind == "processing" {
		doc := notificationDocumentWebWrite(t, f, q.actor, "save", "", 0, store.Record{
			"title": "PRIVATE-DOCUMENT title " + suffix, "issuer": "PRIVATE-DOCUMENT issuer", "category": "",
			"notes": "PRIVATE-DOCUMENT private contents", "document_type": "other", "fields": store.Record{}, "reviewed": false,
		})
		q.document, q.resource = store.String(doc["id"]), value.UUID()
		job := value.UUID()
		ctx := audit.WithActor(f.ctx, q.actor.UserID, q.actor.HouseholdID)
		tx, err := audit.Begin(ctx, f.pool)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		// This fixture models a terminal local processing result. It writes the
		// typed durable source, never notification events, intents or deliveries.
		_, err = tx.Exec(ctx, `INSERT INTO core.jobs(id,household_id,module_id,module_generation,kind,payload,dedupe_key)
 VALUES($1,$2,'documents',$3,'document_processing',jsonb_build_object('processing_id',$4::text),'document/'||$4::text)`,
			job, q.actor.HouseholdID, generation, q.resource)
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO documents.processing(id,household_id,document_id,operation,input,job_id,module_generation,requested_by)
 VALUES($1,$2,$3,'scan','{"fixture_filename":"PRIVATE-DOCUMENT-file.pdf"}',$4,$5,$6)`,
				q.resource, q.actor.HouseholdID, q.document, job, generation, q.actor.UserID)
		}
		if err == nil {
			err = audit.Bind(audit.WithSystem(ctx, "worker"), tx)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE documents.processing SET state='failed',status_code='local_processing_failed',updated_at=clock_timestamp() WHERE id=$1`, q.resource)
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE core.jobs SET state='failed',finished_at=clock_timestamp(),last_error='local document processing failed' WHERE id=$1`, job)
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		if err != nil {
			t.Fatal("actual typed processing transition", err)
		}
	} else {
		var version int64
		if err := f.pool.QueryRow(f.ctx, `SELECT coalesce((SELECT version FROM documents.integration_sources WHERE household_id=$1 AND adapter='watch'),0)`, q.actor.HouseholdID).Scan(&version); err != nil {
			t.Fatal(err)
		}
		source := notificationDocumentWebWrite(t, f, q.actor, "integration", "", version, store.Record{"adapter": "watch", "enabled": true})
		q.resource = store.String(source["id"])
		if _, err := audit.Exec(audit.WithSystem(f.ctx, "worker"), f.pool, `UPDATE documents.integration_sources SET status_code='input_failed',failures=failures+1 WHERE id=$1`, q.resource); err != nil {
			t.Fatal("actual typed import failure transition", err)
		}
	}
	notificationDocumentWebScan(t, f)
	if err := f.pool.QueryRow(f.ctx, `SELECT id::text FROM core.notification_intents WHERE category='documents' AND resource_type=$1
 AND resource_id=$2 AND user_id=$3 AND state='pending' ORDER BY source_revision DESC LIMIT 1`, "document_"+kind, q.resource, q.actor.UserID).Scan(&q.intent); err != nil {
		t.Fatal("actual failure did not stage personal intent", err)
	}
	if err := service.ScheduleNotifications(f.ctx); err != nil {
		t.Fatal(err)
	}
	var deliveries []string
	var recipientCount int
	if err := f.pool.QueryRow(f.ctx, `SELECT ARRAY(SELECT id::text FROM core.notification_deliveries WHERE intent_id=$1 AND subscription_id=$2),
 (SELECT count(DISTINCT i.user_id) FROM core.notification_intents i WHERE i.category='documents' AND i.resource_type=$3
 AND i.resource_id=$4 AND i.event_at=(SELECT event_at FROM core.notification_intents WHERE id=$1))`,
		q.intent, q.connection, "document_"+kind, q.resource).Scan(&deliveries, &recipientCount); err != nil || len(deliveries) != 1 || recipientCount != 1 {
		t.Fatal("failure was not restricted to the original requester/current approver", len(deliveries), recipientCount, err)
	}
	q.delivery = deliveries[0]
	return q
}

// This controlled receiver independently decrypts the actual sender wire. The
// content check examines plaintext rather than assuming encryption implies privacy.
func notificationDocumentClear(raw []byte, key *ecdh.PrivateKey, authText string) ([]byte, error) {
	if len(raw) < 102 || len(raw) > 8192 || raw[20] != 65 {
		return nil, errors.New("invalid controlled document wire")
	}
	server, err := ecdh.P256().NewPublicKey(raw[21:86])
	if err != nil {
		return nil, err
	}
	shared, err := key.ECDH(server)
	if err != nil {
		return nil, err
	}
	authKey, err := base64.RawURLEncoding.DecodeString(authText)
	if err != nil || len(authKey) != 16 {
		return nil, errors.New("invalid controlled receiver authentication")
	}
	derive := func(secret, salt, info []byte, size int) ([]byte, error) {
		out := make([]byte, size)
		_, err := io.ReadFull(hkdf.New(sha256.New, secret, salt, info), out)
		return out, err
	}
	info := append([]byte("WebPush: info\x00"), key.PublicKey().Bytes()...)
	info = append(info, server.Bytes()...)
	ikm, err := derive(shared, authKey, info, 32)
	if err != nil {
		return nil, err
	}
	contentKey, err := derive(ikm, raw[:16], []byte("Content-Encoding: aes128gcm\x00"), 16)
	if err != nil {
		return nil, err
	}
	nonce, err := derive(ikm, raw[:16], []byte("Content-Encoding: nonce\x00"), 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(contentKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	clear, err := gcm.Open(nil, nonce, raw[86:], nil)
	if err != nil {
		return nil, errors.New("controlled document receiver could not authenticate wire")
	}
	clear = bytes.TrimRight(clear, "\x00")
	if len(clear) == 0 || clear[len(clear)-1] != 2 {
		return nil, errors.New("controlled document record delimiter missing")
	}
	return clear[:len(clear)-1], nil
}

func notificationDocumentWithdraw(t *testing.T, f *integrationFixture, q notificationDocumentDelivery, action string) {
	t.Helper()
	switch action {
	case "role":
		if _, err := f.owner.Exec(f.ctx, `UPDATE core.memberships SET role='reader' WHERE id=$1`, q.actor.MembershipID); err != nil {
			t.Fatal(err)
		}
	case "session":
		if err := f.app.Auth.Logout(f.ctx, q.actor); err != nil {
			t.Fatal(err)
		}
	case "retry", "cancel", "archive", "archive-unarchive":
		current, err := f.app.Store.DocumentDetail(f.ctx, f.identity, q.document)
		if err != nil {
			t.Fatal(err)
		}
		if action == "archive" || action == "archive-unarchive" {
			notificationDocumentWebWrite(t, f, f.identity, "archive", q.document, store.Int(current["version"]), store.Record{"archived": true})
			if action == "archive-unarchive" {
				current, err = f.app.Store.DocumentDetail(f.ctx, f.identity, q.document)
				if err != nil {
					t.Fatal(err)
				}
				notificationDocumentWebWrite(t, f, f.identity, "archive", q.document, store.Int(current["version"]), store.Record{"archived": false})
			}
			return
		}
		notificationDocumentWebWrite(t, f, f.identity, "retry", q.document, store.Int(current["version"]), store.Record{"processing_id": q.resource})
		if action == "cancel" {
			current, err = f.app.Store.DocumentDetail(f.ctx, f.identity, q.document)
			if err != nil {
				t.Fatal(err)
			}
			notificationDocumentWebWrite(t, f, f.identity, "cancel", q.document, store.Int(current["version"]), store.Record{"processing_id": q.resource})
		}
	case "source-version", "disabled":
		var version int64
		if err := f.pool.QueryRow(f.ctx, `SELECT version FROM documents.integration_sources WHERE id=$1`, q.resource).Scan(&version); err != nil {
			t.Fatal(err)
		}
		notificationDocumentWebWrite(t, f, f.identity, "integration", "", version, store.Record{"adapter": "watch", "enabled": action != "disabled"})
	case "recovered":
		if _, err := audit.Exec(audit.WithSystem(f.ctx, "worker"), f.pool, `UPDATE documents.integration_sources SET status_code='ready',failures=0 WHERE id=$1`, q.resource); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("unrecognized document withdrawal fixture")
	}
}

func notificationDocumentPostSlot(t *testing.T, f *integrationFixture, service *webpush.Service, q notificationDocumentDelivery, action string) {
	t.Helper()
	type leased struct{ job, token string }
	occupied := make([]leased, 0, 2)
	for _, suffix := range []string{"a", "b"} {
		actor := shoppingTestMember(t, f, "doc-slot-"+strings.ReplaceAll(value.UUID(), "-", "")[:12]+suffix, "reader")
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
	var calls atomic.Int32
	if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
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
	var wg sync.WaitGroup
	results := make(chan error, 3)
	defer func() { unblock(); cancel(); wg.Wait() }()
	for _, item := range occupied {
		wg.Go(func() { results <- service.SendNotificationJob(ctx, item.job, item.token) })
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("controlled document transport slot not occupied")
		}
	}
	job, token := notificationClaimFixture(t, f, q.delivery)
	wg.Go(func() { results <- service.SendNotificationJob(ctx, job, token) })
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
			t.Fatal("document sender did not complete initial preflight before slot wait")
		}
		time.Sleep(5 * time.Millisecond)
	}
	notificationDocumentWithdraw(t, f, q, action)
	unblock()
	for range 3 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal("controlled document delivery or final guard", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("controlled document sender did not finish")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("withdrawn document source crossed final transport guard", calls.Load())
	}
	var state string
	if err := f.pool.QueryRow(f.ctx, `SELECT state FROM core.notification_deliveries WHERE id=$1`, q.delivery).Scan(&state); err != nil || state != "cancelled" {
		t.Fatal("withdrawn document delivery not cancelled", state, err)
	}
}

func TestNotificationDocumentsControlledDeliveryAndFinalSourceGuards(t *testing.T) {
	f := newIntegrationFixture(t)
	state := f.app.Store.States()["documents"]
	if state.State != "enabled" {
		if err := f.app.Store.SetModule(f.ctx, f.identity, "documents", true, state.Version, value.UUID()); err != nil {
			t.Fatal(err)
		}
	}
	service, _ := shoppingPushService(t, f, true)
	// This fully opted-in administrator is not the source requester/approver.
	// Its presence catches accidental household/admin broadcast expansion.
	notificationDocumentEnroll(t, f, service, f.identity)
	for _, kind := range []string{"processing", "import"} {
		t.Run(kind+" accepted encrypted content-free delivery", func(t *testing.T) {
			q := notificationDocumentQueue(t, f, service, kind, "accepted")
			var calls atomic.Int32
			if err := service.SetHTTPClientForTests(shoppingControlledPushClient(func(req *http.Request) (*http.Response, error) {
				calls.Add(1)
				raw, err := io.ReadAll(io.LimitReader(req.Body, 8193))
				ttl, ttlErr := strconv.Atoi(req.Header.Get("TTL"))
				if err != nil || ttlErr != nil || ttl < 1 || ttl > 300 || req.Header.Get("Urgency") != "low" ||
					req.Header.Get("Content-Encoding") != "aes128gcm" || req.Header.Get("Authorization") == "" || req.URL.String() != q.input.Subscription.Endpoint {
					return nil, errors.New("document request protocol or destination invalid")
				}
				clear, err := notificationDocumentClear(raw, q.key, q.input.Subscription.Keys.Auth)
				if err != nil {
					return nil, err
				}
				var message struct {
					Schema  int    `json:"schema"`
					Kind    string `json:"kind"`
					ID      string `json:"id"`
					Expires int64  `json:"expires_at"`
					Scope   string `json:"audience_scope"`
				}
				decoder := json.NewDecoder(bytes.NewReader(clear))
				decoder.DisallowUnknownFields()
				if decoder.Decode(&message) != nil || decoder.Decode(new(any)) != io.EOF || message.Schema != 2 || message.Kind != "documents" ||
					message.ID != q.delivery || !notification.ValidMessage(message.Schema, message.Kind, message.ID, message.Scope, message.Expires, time.Now().Unix()) ||
					bytes.Contains(clear, []byte("PRIVATE-DOCUMENT")) || bytes.Contains(clear, []byte("local_processing_failed")) || bytes.Contains(clear, []byte("input_failed")) {
					return nil, errors.New("document plaintext leaked content or departed from closed reference contract")
				}
				return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted"))}, nil
			})); err != nil {
				t.Fatal(err)
			}
			job, lease := notificationClaimFixture(t, f, q.delivery)
			if err := service.SendNotificationJob(f.ctx, job, lease); err != nil {
				t.Fatal(err)
			}
			var deliveryState string
			if err := f.pool.QueryRow(f.ctx, `SELECT state FROM core.notification_deliveries WHERE id=$1`, q.delivery).Scan(&deliveryState); err != nil || deliveryState != "accepted" || calls.Load() != 1 {
				t.Fatal("document controlled acceptance", deliveryState, calls.Load(), err)
			}
		})
	}
	for _, scenario := range []struct{ kind, action string }{
		{"processing", "role"}, {"processing", "session"}, {"processing", "retry"}, {"processing", "cancel"}, {"processing", "archive"},
		{"processing", "archive-unarchive"},
		{"import", "role"}, {"import", "source-version"}, {"import", "recovered"}, {"import", "disabled"},
	} {
		t.Run(scenario.kind+" "+scenario.action+" after transport wait", func(t *testing.T) {
			q := notificationDocumentQueue(t, f, service, scenario.kind, scenario.action)
			notificationDocumentPostSlot(t, f, service, q, scenario.action)
		})
	}
}
