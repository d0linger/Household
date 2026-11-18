package config

import (
	"errors"

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
	ringConfigured := explicitlyConfigured("HOUSEHOLD_ENCRYPTION_KEYRING")
	oldConfigured := explicitlyConfigured("HOUSEHOLD_ENCRYPTION_KEY")
	if ringConfigured && oldConfigured {
		return errors.New("encryption keyring and legacy secret sources are mutually exclusive")
	}
	var err error
	if !ringConfigured && !oldConfigured {
		// Only the secrets folder names a source: choose the keyring when it is
		// present, and refuse both rather than guessing which one is current.
		if ringConfigured, err = configured("HOUSEHOLD_ENCRYPTION_KEYRING"); err != nil {
			return err
		}
		if oldConfigured, err = configured("HOUSEHOLD_ENCRYPTION_KEY"); err != nil {
			return err
		}
		if ringConfigured && oldConfigured {
			return errors.New("encryption keyring and legacy secret sources are mutually exclusive")
		}
	}
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
