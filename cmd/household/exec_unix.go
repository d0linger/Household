//go:build !windows

package main

import (
	"errors"
	"os"
	"syscall"
)

// execServe replaces this process with `household serve`, so the setup
// credentials removed from environment do not remain in the process.
func execServe(environment []string) error {
	self, err := os.Executable()
	if err != nil {
		return errors.New("could not locate the household binary")
	}
	// security: re-executes this exact binary with a fixed argument list.
	return syscall.Exec(self, []string{"household", "serve"}, environment) // #nosec G204 -- own binary, fixed arguments
}
