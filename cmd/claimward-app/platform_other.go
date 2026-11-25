// SPDX-License-Identifier: BSD-3-Clause

//go:build !windows

package main

import "os"

func closeWindow(string) { os.Exit(0) }
