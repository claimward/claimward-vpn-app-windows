// SPDX-License-Identifier: BSD-3-Clause

//go:build !windows

package main

import (
	"os"

	"github.com/claimward/claimward-vpn-client/pkg/browser"
)

func openURL(u string) error { return browser.Open(u) }

// raiseWindow has no portable way to bring a window forward; the window is
// already open, since closing it quits.
func raiseWindow(string) {}

func closeWindow(string) { os.Exit(0) }
