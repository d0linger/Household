package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMeterFeedsRemainOffAndSecretsStayOutOfErrors(t *testing.T) {
	setValidEnvironment(t)
	var config Config
	if err := loadMeterFeeds(&config); err != nil || config.MeterFeeds.HAURL != "" || config.MeterFeeds.MQTTURL != "" {
		t.Fatal("optional feed not off", err)
	}
	path := filepath.Join(t.TempDir(), "ha-token")
	if err := os.WriteFile(path, []byte("synthetic-private-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOUSEHOLD_METERS_HA_URL", "https://ha.example")
	t.Setenv("HOUSEHOLD_METERS_HA_TOKEN_FILE", path)
	if err := loadMeterFeeds(&config); err != nil || config.MeterFeeds.HAToken != "synthetic-private-token" {
		t.Fatal("file secret not loaded", err)
	}
	t.Setenv("HOUSEHOLD_METERS_HA_URL", "http://ha.example")
	if err := loadMeterFeeds(&config); err == nil || strings.Contains(err.Error(), "synthetic-private-token") {
		t.Fatal("invalid configuration or secret leaked", err)
	}
	t.Setenv("HOUSEHOLD_METERS_ALLOW_PLAINTEXT", "true")
	if err := loadMeterFeeds(&config); err != nil {
		t.Fatal("explicit trusted plaintext not honored", err)
	}
	t.Setenv("HOUSEHOLD_METERS_CA_FILE", filepath.Join(t.TempDir(), "missing"))
	if err := loadMeterFeeds(&config); err == nil {
		t.Fatal("missing configured CA ignored")
	}
}
