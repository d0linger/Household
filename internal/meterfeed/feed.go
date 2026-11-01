// Package meterfeed reads explicitly configured household telemetry endpoints.
package meterfeed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"household/internal/energy"
)

var ErrConfiguration = errors.New("meter adapter configuration is missing or invalid")
var ErrUnavailable = errors.New("meter adapter endpoint is unavailable")
var ErrMessage = errors.New("meter adapter measurement is invalid")

type Config struct {
	HAURL          string
	HAToken        string `json:"-"`
	MQTTURL        string
	MQTTUser       string `json:"-"`
	MQTTPassword   string `json:"-"`
	AllowPlaintext bool
	CAPEM          []byte `json:"-"`
}

type Source struct {
	ID         string
	Name       string
	Kind       string
	Unit       string
	Generation int64
}

type Sample struct {
	MeasuredAt        time.Time  `json:"measured_at"`
	Value             string     `json:"value"`
	MeasurementKind   string     `json:"measurement_kind"`
	Unit              string     `json:"unit_code"`
	Quality           string     `json:"quality"`
	IntervalStartedAt *time.Time `json:"interval_started_at,omitempty"`
	RolloverConfirmed bool       `json:"rollover_confirmed"`
}

func (sample Sample) Key() (string, error) {
	raw, err := json.Marshal(sample)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

var entityPattern = regexp.MustCompile(`^sensor\.[a-z0-9_]{1,150}$`)

func Validate(config Config) error {
	if len(config.CAPEM) > 128<<10 || len(config.HAToken) > 8192 || len(config.MQTTUser) > 1024 || len(config.MQTTPassword) > 8192 {
		return ErrConfiguration
	}
	if len(config.CAPEM) != 0 && !x509.NewCertPool().AppendCertsFromPEM(config.CAPEM) {
		return ErrConfiguration
	}
	for _, secret := range []string{config.HAToken, config.MQTTUser, config.MQTTPassword} {
		if !utf8.ValidString(secret) || strings.ContainsAny(secret, "\r\n\x00") {
			return ErrConfiguration
		}
	}
	if config.HAURL != "" {
		u, err := url.Parse(config.HAURL)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
			u.Path != "" && u.Path != "/" || u.Scheme != "https" && (!config.AllowPlaintext || u.Scheme != "http") || config.HAToken == "" {
			return ErrConfiguration
		}
	} else if config.HAToken != "" {
		return ErrConfiguration
	}
	if config.MQTTURL != "" {
		u, err := url.Parse(config.MQTTURL)
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
			u.Path != "" && u.Path != "/" || u.Scheme != "mqtts" && (!config.AllowPlaintext || u.Scheme != "mqtt") ||
			config.MQTTUser == "" || config.MQTTPassword == "" {
			return ErrConfiguration
		}
	} else if config.MQTTUser != "" || config.MQTTPassword != "" {
		return ErrConfiguration
	}
	return nil
}

func transportTLS(config Config) (*tls.Config, error) {
	result := &tls.Config{MinVersion: tls.VersionTLS12}
	if len(config.CAPEM) != 0 {
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(config.CAPEM) {
			return nil, ErrConfiguration
		}
		result.RootCAs = roots
	}
	return result, nil
}

func validSample(sample Sample, source Source) bool {
	if !energy.ValidType(source.Kind, source.Unit) || sample.MeasurementKind != source.Kind || sample.Unit != source.Unit ||
		!slices.Contains([]string{"actual", "estimated"}, sample.Quality) || sample.MeasuredAt.Year() < 1900 || sample.MeasuredAt.Year() > 2200 ||
		sample.MeasuredAt.Nanosecond()%1000 != 0 || sample.MeasuredAt.After(time.Now()) {
		return false
	}
	if _, err := energy.Decimal(sample.Value); err != nil {
		return false
	}
	if source.Kind == "interval" {
		return sample.IntervalStartedAt != nil && sample.IntervalStartedAt.Year() >= 1900 &&
			sample.IntervalStartedAt.Before(sample.MeasuredAt) && sample.IntervalStartedAt.Nanosecond()%1000 == 0 && !sample.RolloverConfirmed
	}
	return sample.IntervalStartedAt == nil && (!sample.RolloverConfirmed || source.Kind == "cumulative")
}

func ValidateSample(sample Sample, source Source) error {
	if !validSample(sample, source) {
		return ErrMessage
	}
	return nil
}

func uniqueJSON(raw []byte, maximum int) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maximum || !utf8.Valid(raw) {
		return nil, ErrMessage
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	tokens := 1000
	var walk func(int) error
	walk = func(depth int) error {
		tokens--
		if depth > 8 || tokens < 0 {
			return ErrMessage
		}
		token, err := decoder.Token()
		if err != nil {
			return ErrMessage
		}
		if delimiter, ok := token.(json.Delim); ok {
			if delimiter != '{' && delimiter != '[' {
				return ErrMessage
			}
			seen := map[string]bool{}
			for decoder.More() {
				if delimiter == '{' {
					key, err := decoder.Token()
					name, ok := key.(string)
					folded := strings.ToLower(name)
					if err != nil || !ok || seen[folded] {
						return ErrMessage
					}
					seen[folded] = true
				}
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			closing, err := decoder.Token()
			if err != nil || delimiter == '{' && closing != json.Delim('}') || delimiter == '[' && closing != json.Delim(']') {
				return ErrMessage
			}
		}
		return nil
	}
	if err := walk(0); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, ErrMessage
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, ErrMessage
	}
	return object, nil
}

func ParseMQTT(raw []byte, source Source) (Sample, error) {
	members, err := uniqueJSON(raw, 16<<10)
	if err != nil {
		return Sample{}, err
	}
	allowed := []string{"measured_at", "value", "measurement_kind", "unit_code", "quality", "interval_started_at", "rollover_confirmed"}
	for field, raw := range members {
		if !slices.Contains(allowed, field) || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return Sample{}, ErrMessage
		}
	}
	for _, field := range []string{"measured_at", "value", "measurement_kind", "unit_code", "quality", "rollover_confirmed"} {
		if _, exists := members[field]; !exists {
			return Sample{}, ErrMessage
		}
	}
	var sample Sample
	if json.Unmarshal(raw, &sample) != nil || !validSample(sample, source) {
		return Sample{}, ErrMessage
	}
	sample.MeasuredAt = sample.MeasuredAt.UTC()
	if sample.IntervalStartedAt != nil {
		start := sample.IntervalStartedAt.UTC()
		sample.IntervalStartedAt = &start
	}
	quantity, _ := energy.Decimal(sample.Value)
	sample.Value = quantity.FloatString(6)
	return sample, nil
}

func FetchHA(ctx context.Context, config Config, source Source) (Sample, error) {
	if Validate(config) != nil || config.HAURL == "" || !entityPattern.MatchString(source.Name) {
		return Sample{}, ErrConfiguration
	}
	tlsConfig, err := transportTLS(config)
	if err != nil {
		return Sample{}, err
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext,
		TLSClientConfig: tlsConfig, TLSHandshakeTimeout: 3 * time.Second, MaxConnsPerHost: 1, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(config.HAURL, "/")+"/api/states/"+source.Name, nil)
	if err != nil {
		return Sample{}, ErrConfiguration
	}
	request.Header.Set("Authorization", "Bearer "+config.HAToken)
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return Sample{}, ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return Sample{}, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil {
		return Sample{}, ErrUnavailable
	}
	object, err := uniqueJSON(raw, 64<<10)
	if err != nil {
		return Sample{}, err
	}
	var entity, state, updated string
	if json.Unmarshal(object["entity_id"], &entity) != nil || json.Unmarshal(object["state"], &state) != nil ||
		json.Unmarshal(object["last_updated"], &updated) != nil || entity != source.Name {
		return Sample{}, ErrMessage
	}
	attributes, err := uniqueJSON(object["attributes"], 32<<10)
	if err != nil {
		return Sample{}, err
	}
	var unit, stateClass string
	if json.Unmarshal(attributes["unit_of_measurement"], &unit) != nil || json.Unmarshal(attributes["state_class"], &stateClass) != nil {
		return Sample{}, ErrMessage
	}
	if unit == "m³" {
		unit = "m3"
	}
	if source.Kind == "cumulative" && stateClass != "total_increasing" || source.Kind != "cumulative" && stateClass != "measurement" {
		return Sample{}, ErrMessage
	}
	at, err := time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return Sample{}, ErrMessage
	}
	sample := Sample{MeasuredAt: at.UTC(), Value: state, MeasurementKind: source.Kind, Unit: unit, Quality: "actual"}
	if source.Kind == "interval" {
		var start string
		if json.Unmarshal(attributes["interval_started_at"], &start) != nil {
			return Sample{}, ErrMessage
		}
		instant, err := time.Parse(time.RFC3339Nano, start)
		if err != nil {
			return Sample{}, ErrMessage
		}
		instant = instant.UTC()
		sample.IntervalStartedAt = &instant
	}
	if !validSample(sample, source) {
		return Sample{}, ErrMessage
	}
	amount, _ := energy.Decimal(sample.Value)
	sample.Value = amount.FloatString(6)
	return sample, nil
}

func ValidTopic(topic string) bool {
	return topic != "" && len(topic) <= 800 && utf8.ValidString(topic) && !strings.ContainsAny(topic, "+#") &&
		!strings.ContainsFunc(topic, func(character rune) bool { return unicode.IsControl(character) || character == '\uFFFD' })
}
