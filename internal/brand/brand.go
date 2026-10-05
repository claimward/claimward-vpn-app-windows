// SPDX-License-Identifier: BSD-3-Clause

// Package brand carries the Claimward mark the app shows: the tray icon.
package brand

import _ "embed"

// TrayIcon is the Claimward mark, 32x32 PNG, teal on transparent: it reads
// on a light and on a dark taskbar alike. It is favicon/favicon-32.png from
// github.com/claimward/brand, copied unchanged.
//
//go:embed claimward-32.png
var TrayIcon []byte
