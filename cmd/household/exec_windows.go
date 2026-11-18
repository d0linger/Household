//go:build windows

package main

import "errors"

// execServe is unavailable on Windows; start is meant for the Linux image.
func execServe([]string) error {
	return errors.New("household start runs in the Linux container image; use migrate and serve separately")
}
