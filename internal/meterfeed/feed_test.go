package meterfeed

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const feedMessage = `{"measured_at":"2026-01-01T00:00:00Z","value":"10.100000","measurement_kind":"cumulative","unit_code":"kWh","quality":"actual","rollover_confirmed":false}`

func testSource() Source {
	return Source{ID: "00000000-0000-4000-8000-000000000001", Name: "household/energy", Kind: "cumulative", Unit: "kWh", Generation: 1}
}

func TestParseMQTTUsesClosedCanonicalPhysicalFacts(t *testing.T) {
	first, err := ParseMQTT([]byte(feedMessage), testSource())
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseMQTT([]byte(strings.ReplaceAll(strings.ReplaceAll(feedMessage, "2026-01-01T00:00:00Z", "2026-01-01T01:00:00+01:00"), "10.100000", "10.1")), testSource())
	if err != nil {
		t.Fatal(err)
	}
	left, err := first.Key()
	if err != nil {
		t.Fatal(err)
	}
	right, err := second.Key()
	if err != nil || left != right {
		t.Fatal("same physical fact did not deduplicate", err)
	}
	for _, tc := range []struct{ name, raw string }{
		{"duplicate", strings.Replace(feedMessage, `"value":"10.100000"`, `"value":"1","value":"2"`, 1)},
		{"case alias", strings.Replace(feedMessage, `"value"`, `"Value"`, 1)},
		{"unknown", strings.Replace(feedMessage, `"quality"`, `"private_url"`, 1)},
		{"null", strings.Replace(feedMessage, `"actual"`, `null`, 1)},
		{"numeric value", strings.Replace(feedMessage, `"10.100000"`, `10.1`, 1)},
		{"unit confusion", strings.Replace(feedMessage, `"kWh"`, `"kW"`, 1)},
		{"future", strings.Replace(feedMessage, "2026-01-01T00:00:00Z", "2199-01-01T00:00:00Z", 1)},
		{"precision", strings.Replace(feedMessage, "00:00:00Z", "00:00:00.0000001Z", 1)},
		{"oversize", strings.Repeat(" ", 16<<10) + feedMessage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseMQTT([]byte(tc.raw), testSource()); !errors.Is(err, ErrMessage) {
				t.Fatal("invalid message accepted", err)
			}
		})
	}
}

func TestFeedConfigurationRequiresExplicitAuthenticatedOrigins(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config Config
		valid  bool
	}{
		{"off", Config{}, true},
		{"HA TLS", Config{HAURL: "https://ha.example", HAToken: "synthetic-test-token"}, true},
		{"plaintext forbidden", Config{HAURL: "http://127.0.0.1", HAToken: "synthetic-test-token"}, false},
		{"explicit local plaintext", Config{HAURL: "http://127.0.0.1", HAToken: "synthetic-test-token", AllowPlaintext: true}, true},
		{"credentials in URL", Config{HAURL: "https://a:b@ha.example", HAToken: "synthetic-test-token"}, false},
		{"arbitrary path", Config{HAURL: "https://ha.example/api/states", HAToken: "synthetic-test-token"}, false},
		{"query", Config{HAURL: "https://ha.example?token=x", HAToken: "synthetic-test-token"}, false},
		{"anonymous broker", Config{MQTTURL: "mqtts://broker.example"}, false},
		{"MQTT TLS", Config{MQTTURL: "mqtts://broker.example", MQTTUser: "test-user", MQTTPassword: "synthetic-test-password"}, true},
		{"header injection", Config{HAURL: "https://ha.example", HAToken: "synthetic\r\nInjected: test"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(tc.config); (err == nil) != tc.valid {
				t.Fatal(err)
			}
		})
	}
	for _, topic := range []string{"", "household/+", "#", "household/\nsecret", strings.Repeat("x", 801)} {
		if ValidTopic(topic) {
			t.Fatal("wildcard or unbounded topic accepted")
		}
	}
}

func TestFetchHAUsesActualTLSCounterpartAndSourceTimestamp(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/api/states/sensor.energy" || r.Header.Get("Authorization") != "Bearer synthetic-test-token" {
			http.Error(w, "invalid source request", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"entity_id":"sensor.energy","state":"12.345678","last_updated":"2026-01-01T00:00:00Z","attributes":{"unit_of_measurement":"kWh","state_class":"total_increasing"}}`))
	}))
	defer server.Close()
	config := Config{HAURL: server.URL, HAToken: "synthetic-test-token", CAPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})}
	source := testSource()
	source.Name = "sensor.energy"
	sample, err := FetchHA(t.Context(), config, source)
	if err != nil || sample.Value != "12.345678" || sample.MeasuredAt.Format("2006-01-02T15:04:05Z07:00") != "2026-01-01T00:00:00Z" || requests.Load() != 1 {
		t.Fatal(sample, err)
	}
	config.CAPEM = nil
	if _, err = FetchHA(t.Context(), config, source); !errors.Is(err, ErrUnavailable) {
		t.Fatal("untrusted TLS accepted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = FetchHA(ctx, config, source); err == nil {
		t.Fatal("cancelled request reached a successful result")
	}
}

func TestFetchHARefusesRedirectAndOversizedOrMismatchedStates(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"redirect", "", 302}, {"oversize", strings.Repeat("x", (64<<10)+1), 200},
		{"wrong entity", `{"entity_id":"sensor.other","state":"1","last_updated":"2026-01-01T00:00:00Z","attributes":{"unit_of_measurement":"kWh","state_class":"total_increasing"}}`, 200},
		{"unavailable state", `{"entity_id":"sensor.energy","state":"unavailable","last_updated":"2026-01-01T00:00:00Z","attributes":{"unit_of_measurement":"kWh","state_class":"total_increasing"}}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var count atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				w.Header().Set("Location", "/private-other")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			source := testSource()
			source.Name = "sensor.energy"
			_, err := FetchHA(t.Context(), Config{HAURL: server.URL, HAToken: "synthetic-test-token", AllowPlaintext: true}, source)
			if err == nil || count.Load() != 1 {
				t.Fatal("unsafe state or redirect followed", err, count.Load())
			}
		})
	}
}

func FuzzParseMQTTKeepsTypedBounds(f *testing.F) {
	f.Add([]byte(feedMessage))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		sample, err := ParseMQTT(raw, testSource())
		if err == nil && ValidateSample(sample, testSource()) != nil {
			t.Fatal("accepted untyped message")
		}
	})
}
