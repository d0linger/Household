package config

import (
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"household/internal/meterfeed"
)

func loadMeterFeeds(c *Config) error {
	config := meterfeed.Config{HAURL: strings.TrimSpace(os.Getenv("HOUSEHOLD_METERS_HA_URL")),
		MQTTURL: strings.TrimSpace(os.Getenv("HOUSEHOLD_METERS_MQTT_URL")), MQTTUser: os.Getenv("HOUSEHOLD_METERS_MQTT_USER")}
	var err error
	config.HAToken, err = secret("HOUSEHOLD_METERS_HA_TOKEN")
	if err != nil {
		return err
	}
	config.MQTTPassword, err = secret("HOUSEHOLD_METERS_MQTT_PASSWORD")
	if err != nil {
		return err
	}
	if raw := os.Getenv("HOUSEHOLD_METERS_ALLOW_PLAINTEXT"); raw != "" {
		config.AllowPlaintext, err = strconv.ParseBool(raw)
		if err != nil {
			return errors.New("invalid optional meter adapter transport setting")
		}
	}
	if path := os.Getenv("HOUSEHOLD_METERS_CA_FILE"); path != "" {
		// security: only the operator's deployment environment selects this public CA file.
		file, err := os.Open(path) // #nosec G304 G703 -- trusted operator certificate path
		if err != nil {
			return errors.New("cannot read optional meter adapter CA file")
		}
		defer func() { _ = file.Close() }()
		config.CAPEM, err = io.ReadAll(io.LimitReader(file, (128<<10)+1))
		if err != nil || len(config.CAPEM) > 128<<10 {
			return errors.New("optional meter adapter CA file exceeds its bound")
		}
	}
	if err = meterfeed.Validate(config); err != nil {
		return errors.New("invalid optional meter adapter configuration")
	}
	c.MeterFeeds = config
	return nil
}
