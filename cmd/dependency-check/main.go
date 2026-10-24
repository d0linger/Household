// dependency-check inventories exact module versions and reviews permissive licenses.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type dependency struct {
	Path, Version, Dir string
	Main               bool
	Replace            *dependency
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	data, err := exec.Command("go", "list", "-deps", "-json", "./cmd/household").Output()
	if err != nil {
		return errors.New("go module inventory failed")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	modules := []dependency{}
	seen := map[string]bool{}
	for {
		var pkg struct{ Module *dependency }
		err = decoder.Decode(&pkg)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if pkg.Module != nil && !pkg.Module.Main && !seen[pkg.Module.Path] {
			modules = append(modules, *pkg.Module)
			seen[pkg.Module.Path] = true
		}
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Path < modules[j].Path })
	fmt.Println("# Dependency review\n\nExact versions and license-file SHA-256 hashes. Module content must also pass `go mod verify`.\n\n| Module | Version | License | SHA-256 |\n|---|---|---|---|")
	unknown := []string{}
	for _, module := range modules {
		if module.Replace != nil {
			return fmt.Errorf("unreviewed module replacement: %s", module.Path)
		}
		if module.Dir == "" {
			return fmt.Errorf("download module before license review: %s", module.Path)
		}
		entries, err := os.ReadDir(module.Dir)
		if err != nil {
			return err
		}
		kind, digest := "", ""
		for _, entry := range entries {
			name := strings.ToLower(entry.Name())
			if entry.IsDir() || name != "license" && !strings.HasPrefix(name, "license.") && name != "copying" {
				continue
			}
			licensePath := filepath.Join(module.Dir, entry.Name())
			raw, err := os.ReadFile(licensePath) // #nosec G304 -- module directory comes from go list; file is an enumerated license basename
			if err != nil {
				return err
			}
			text := strings.ToLower(string(raw))
			switch {
			case strings.Contains(text, "apache license") && strings.Contains(text, "version 2.0"):
				kind = "Apache-2.0"
			case strings.Contains(text, "permission is hereby granted, free of charge"):
				kind = "MIT"
			case strings.Contains(text, "redistribution and use in source and binary forms"):
				kind = "BSD"
			case strings.Contains(text, "permission to use, copy, modify, and distribute this software"):
				kind = "ISC"
			}
			if kind != "" {
				sum := sha256.Sum256(raw)
				digest = hex.EncodeToString(sum[:])
				break
			}
		}
		if kind == "" {
			kind = "REVIEW REQUIRED"
			unknown = append(unknown, module.Path)
		}
		fmt.Printf("| %s | %s | %s | %s |\n", module.Path, module.Version, kind, digest)
	}
	if len(unknown) > 0 {
		return fmt.Errorf("unreviewed dependency licenses: %s", strings.Join(unknown, ", "))
	}
	return nil
}
