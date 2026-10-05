// SPDX-License-Identifier: BSD-3-Clause

//go:build !windows

package main

import "errors"

// runCommand: the helper is a Windows service. The macOS and Linux apps
// have their own.
func runCommand(command) error {
	return errors.New("claimward-helper is a Windows service; build it with GOOS=windows")
}
