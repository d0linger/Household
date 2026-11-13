package config

import (
	"errors"
	"os"

	"household/internal/secretbox"
)

// Keyring returns the loaded immutable ring, or the historical single-secret
// configuration used by explicitly constructed local/integration fixtures.
func (c Config) Keyring() (*secretbox.Keyring, error) {
	if c.EncryptionKeys != nil {
		return c.EncryptionKeys, nil
	}
	return secretbox.New(c.EncryptionKey)
}

func loadEncryption(c *Config) error {
	ringConfigured := os.Getenv("HOUSEHOLD_ENCRYPTION_KEYRING") != "" || os.Getenv("HOUSEHOLD_ENCRYPTION_KEYRING_FILE") != ""
	oldConfigured := os.Getenv("HOUSEHOLD_ENCRYPTION_KEY") != "" || os.Getenv("HOUSEHOLD_ENCRYPTION_KEY_FILE") != ""
	if ringConfigured && oldConfigured {
		return errors.New("encryption keyring and legacy secret sources are mutually exclusive")
	}
	var err error
	if ringConfigured {
		var raw string
		raw, err = boundedSecret("HOUSEHOLD_ENCRYPTION_KEYRING", secretbox.MaxConfigSize)
		if err != nil {
			return err
		}
		c.EncryptionKeys, err = secretbox.ParseConfig([]byte(raw))
		if err != nil {
			return errors.New("invalid encryption keyring configuration")
		}
		return nil
	}
	// Bound the input file before applying the historical trim contract; New
	// separately bounds the resulting secret to4096 bytes, including env spaces.
	c.EncryptionKey, err = secret("HOUSEHOLD_ENCRYPTION_KEY")
	if err != nil {
		return err
	}
	c.EncryptionKeys, err = secretbox.New(c.EncryptionKey)
	if err != nil {
		return errors.New("encryption secret must contain 32 to 4096 non-placeholder bytes")
	}
	return nil
}
