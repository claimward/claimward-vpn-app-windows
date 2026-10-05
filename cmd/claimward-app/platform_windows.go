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
	procShowWindow    = user32.NewProc("ShowWindow")
	procSetForeground = user32.NewProc("SetForegroundWindow")
	procPostMessage   = user32.NewProc("PostMessageW")
	procGetWindowText = user32.NewProc("GetWindowTextW")
	procIsIconic      = user32.NewProc("IsIconic")
	swRestore, swShow = uintptr(9), uintptr(5)
	wmClose           = uintptr(0x0010)
	ourPID            = uint32(os.Getpid())
)

// openURL opens an https URL (the ViewModel refuses any other) in the
// default browser through the shell.
func openURL(u string) error {
	return windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(u), nil, nil, windows.SW_SHOWNORMAL)
}

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

// raiseWindow restores the window if minimised and brings it forward.
//
// It is still done by hand. go-widgets/window v0.86.0 has window.Raise
// (go-widgets/window#134), which does the same on Win32, but it needs the
// window's back-end, and go-widgets/application v0.7.0's Run does not hand
// it to the caller. The only other way to it is through the toolkit
// clipboard Run installs, which would lean on an implementation detail of
// application. Once Run exposes its window, this and ourWindow can go.
func raiseWindow(title string) {
	h := ourWindow(title)
	if h == 0 {
		return
	}
	if r, _, _ := procIsIconic.Call(uintptr(h)); r != 0 {
		procShowWindow.Call(uintptr(h), swRestore)
	} else {
		procShowWindow.Call(uintptr(h), swShow)
	}
	procSetForeground.Call(uintptr(h))
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
