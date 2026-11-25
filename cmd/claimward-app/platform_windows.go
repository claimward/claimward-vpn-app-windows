// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	procPostMessage   = user32.NewProc("PostMessageW")
	procGetWindowText = user32.NewProc("GetWindowTextW")
	wmClose           = uintptr(0x0010)
	ourPID            = uint32(os.Getpid())
)

// ourWindow is this process's top-level window with the given title. It is
// looked up by process as well as by title, so another program's window of
// the same name is never touched.
func ourWindow(title string) windows.HWND {
	var found windows.HWND
	cb := syscall.NewCallback(func(h windows.HWND, _ uintptr) uintptr {
		var pid uint32
		if _, err := windows.GetWindowThreadProcessId(h, &pid); err != nil || pid != ourPID {
			return 1
		}
		buf := make([]uint16, 256)
		n, _, _ := procGetWindowText.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if windows.UTF16ToString(buf[:n]) == title {
			found = h
			return 0
		}
		return 1
	})
	_ = windows.EnumWindows(cb, nil)
	return found
}

// closeWindow asks the window to close, which ends application.Run and
// lets main tear the tray down. The tunnel is the helper's and stays as it
// is.
func closeWindow(title string) {
	if h := ourWindow(title); h != 0 {
		procPostMessage.Call(uintptr(h), wmClose, 0, 0)
		return
	}
	os.Exit(0)
}
