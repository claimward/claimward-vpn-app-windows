// SPDX-License-Identifier: BSD-3-Clause

//go:build !windows

package main

import "testing"

func TestTheHelperIsAWindowsService(t *testing.T) {
	if err := runCommand(command{name: "run"}); err == nil {
		t.Error("ran off Windows")
	}
}
