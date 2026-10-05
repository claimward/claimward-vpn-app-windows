// SPDX-License-Identifier: BSD-3-Clause

//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/claimward/claimward-vpn-client/pkg/helper"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func runCommand(c command) error {
	switch c.name {
	case "install":
		return install(c.config)
	case "uninstall":
		return uninstall()
	}
	inService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	log, closeLog := openLog(c.config, inService)
	defer closeLog()
	d := daemon{load: helper.LoadConfig, listen: helper.Listen, log: log}
	if inService {
		return svc.Run(serviceName, &service{d: d, config: c.config})
	}
	// From a console: the foreground, until Ctrl+C.
	stop, err := d.start(c.config)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	<-ctx.Done()
	stop()
	return nil
}

// service answers the Service Control Manager.
type service struct {
	d      daemon
	config string
}

func (s *service) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	stop, err := s.d.start(s.config)
	if err != nil {
		s.d.log.Error("cannot start", "err", err)
		// A service-specific exit code, so that `sc query` says it
		// failed rather than that it stopped.
		return true, 1
	}
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for r := range req {
		switch r.Cmd {
		case svc.Interrogate:
			status <- r.CurrentStatus
		case svc.Stop, svc.Shutdown:
			status <- svc.Status{State: svc.StopPending}
			stop()
			return false, 0
		}
	}
	stop()
	return false, 0
}

// openLog is helper.log beside the configuration, under the directory's
// ACL (SYSTEM and Administrators), started afresh past 10 MB. From a
// console it is stderr.
func openLog(config string, inService bool) (*slog.Logger, func()) {
	if !inService {
		return slog.New(slog.NewTextHandler(os.Stderr, nil)), func() {}
	}
	p := filepath.Join(filepath.Dir(config), "helper.log")
	if fi, err := os.Stat(p); err == nil && fi.Size() > 10<<20 {
		_ = os.Rename(p, p+".1")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return slog.New(slog.NewTextHandler(os.Stderr, nil)), func() {}
	}
	log := slog.New(slog.NewTextHandler(f, nil))
	slog.SetDefault(log) // pkg/helper logs the socket group's fallback through it
	return log, func() { f.Close() }
}

// install registers the service, to run this executable as LocalSystem at
// boot, restarted if it fails.
func install(config string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.Abs(exe); err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the Service Control Manager (run elevated): %w", err)
	}
	defer m.Disconnect()
	if s, err := m.OpenService(serviceName); err == nil {
		s.Close()
		return fmt.Errorf("the service %s already exists; uninstall it first", serviceName)
	}
	s, err := m.CreateService(serviceName, exe, mgr.Config{
		DisplayName:  displayName,
		Description:  description,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
		// ServiceStartName empty: LocalSystem.
	}, "run", "-config", config)
	if err != nil {
		return err
	}
	defer s.Close()
	err = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.NoAction},
	}, uint32((24 * time.Hour).Seconds()))
	if err != nil {
		return fmt.Errorf("set recovery actions: %w", err)
	}
	fmt.Printf("installed %s (%s run -config %s); start it with: Start-Service %s\n", serviceName, exe, config, serviceName)
	return nil
}

// uninstall stops the service, waiting for it, and removes it.
func uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect to the Service Control Manager (run elevated): %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("the service %s is not installed: %w", serviceName, err)
	}
	defer s.Close()
	if st, err := s.Control(svc.Stop); err == nil {
		for deadline := time.Now().Add(20 * time.Second); st.State != svc.Stopped && time.Now().Before(deadline); {
			time.Sleep(300 * time.Millisecond)
			if st, err = s.Query(); err != nil {
				break
			}
		}
	} else if !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		return fmt.Errorf("stop %s: %w", serviceName, err)
	}
	if err := s.Delete(); err != nil {
		return err
	}
	fmt.Printf("removed %s\n", serviceName)
	return nil
}
