package config

import (
	"errors"
	"os"
	"strconv"

	"household/internal/documentinput"
)

func loadDocumentInputs(c *Config) error {
	c.DocumentInputs = documentinput.Config{WatchDir: os.Getenv("HOUSEHOLD_DOCUMENT_WATCH_DIR"), IMAPAddress: os.Getenv("HOUSEHOLD_DOCUMENT_IMAP_ADDRESS"), IMAPUser: os.Getenv("HOUSEHOLD_DOCUMENT_IMAP_USER"), IMAPMailbox: os.Getenv("HOUSEHOLD_DOCUMENT_IMAP_MAILBOX"), PaperlessURL: os.Getenv("HOUSEHOLD_DOCUMENT_PAPERLESS_URL")}
	var err error
	c.DocumentInputs.IMAPPassword, err = secret("HOUSEHOLD_DOCUMENT_IMAP_PASSWORD")
	if err != nil {
		return err
	}
	c.DocumentInputs.PaperlessToken, err = secret("HOUSEHOLD_DOCUMENT_PAPERLESS_TOKEN")
	if err != nil {
		return err
	}
	if raw := os.Getenv("HOUSEHOLD_DOCUMENT_PAPERLESS_TAG"); raw != "" {
		c.DocumentInputs.PaperlessTag, err = strconv.Atoi(raw)
		if err != nil {
			return errors.New("invalid document Paperless tag")
		}
	}
	return c.DocumentInputs.Validate()
}
