package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"

	"household/internal/webpush"
)

// runPushKeygen deliberately runs before configuration, bootstrap and database access.
func runPushKeygen(args []string) error {
	flags := flag.NewFlagSet("push-keygen", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	privatePath := flags.String("private-out", "", "new private key file")
	publicPath := flags.String("public-out", "", "new public key file")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *privatePath == "" || *publicPath == "" {
		return errors.New("usage: household push-keygen --private-out NEW_FILE --public-out NEW_FILE")
	}
	privateName, err := filepath.Abs(*privatePath)
	if err != nil {
		return errors.New("invalid private key output path")
	}
	publicName, err := filepath.Abs(*publicPath)
	if err != nil || privateName == publicName {
		return errors.New("key output paths must be distinct")
	}
	private, public, err := webpush.GenerateKeys()
	if err != nil {
		return errors.New("push key generation failed")
	}
	// security: operator-chosen paths are created exclusively; existing files and
	// symlinks are never overwritten, and no key material is printed.
	privateFile, err := os.OpenFile(privateName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) // #nosec G304 G703 -- explicit CLI output, exclusive creation
	if err != nil {
		return errors.New("private key output must be a new writable file")
	}
	privateInfo, statErr := privateFile.Stat()
	defer func() { _ = privateFile.Close() }()
	publicFile, err := os.OpenFile(publicName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) // #nosec G304 G703 -- explicit CLI output, exclusive creation
	if err != nil {
		_ = privateFile.Close()
		removeOwnedKeyFile(privateName, privateInfo)
		return errors.New("public key output must be a new writable file")
	}
	publicInfo, publicStatErr := publicFile.Stat()
	defer func() { _ = publicFile.Close() }()
	complete := false
	defer func() {
		if !complete {
			_ = privateFile.Close()
			_ = publicFile.Close()
			removeOwnedKeyFile(privateName, privateInfo)
			removeOwnedKeyFile(publicName, publicInfo)
		}
	}()
	if statErr != nil || publicStatErr != nil {
		return errors.New("could not inspect newly created key files")
	}
	if _, err = io.WriteString(privateFile, private+"\n"); err != nil {
		return errors.New("could not write private key file")
	}
	if _, err = io.WriteString(publicFile, public+"\n"); err != nil {
		return errors.New("could not write public key file")
	}
	if privateFile.Sync() != nil || publicFile.Sync() != nil {
		return errors.New("could not synchronize key files")
	}
	if privateFile.Close() != nil || publicFile.Close() != nil {
		return errors.New("could not close key files")
	}
	complete = true
	return nil
}

func removeOwnedKeyFile(name string, created os.FileInfo) {
	if created == nil {
		return
	}
	current, err := os.Lstat(name) // #nosec G703 -- explicit operator-selected output path; inspect without following symlinks before comparing the exclusively created file's identity.
	if err == nil && os.SameFile(created, current) && current.Mode().IsRegular() {
		_ = os.Remove(name) // #nosec G703 -- failure cleanup only at the explicit CLI output path after regular-file and SameFile checks; no recursive removal or request-controlled path.
	}
}
