// SPDX-License-Identifier: BSD-3-Clause

// Command claimward-helper is Claimward's privileged helper for Windows: a
// service running as LocalSystem that owns the WireGuard tunnel (a Wintun
// adapter) and does the server comms on the app's behalf, through
// github.com/claimward/claimward-vpn-client/pkg/helper.
//
//	claimward-helper install [-config PATH]   register the service (elevated)
//	claimward-helper uninstall                stop and remove it (elevated)
//	claimward-helper run [-config PATH]       what the service runs; from a
//	                                          console, runs in the foreground
//	claimward-helper version
//
// wintun.dll must sit beside the executable.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"

	"github.com/claimward/claimward-vpn-client/pkg/helper"
)

const (
	// serviceName is the service's key in the Service Control Manager.
	serviceName = "ClaimwardHelper"
	displayName = "Claimward VPN helper"
	description = "Brings the Claimward VPN tunnel up and down for the Claimward app. " +
		"It enrolls only with the servers its configuration (helper.json) names."
	// defaultConfig is helper.json beside the socket, under ProgramData.
	defaultConfig = `C:\ProgramData\Claimward\helper.json`
	// platform is how the server's administrators see these devices.
	platform = "app-windows"
)

var version = "dev"

// command is a parsed command line.
type command struct {
	name   string // install, uninstall, run, version
	config string
}

var errUsage = errors.New("usage: claimward-helper install|uninstall|run|version [-config PATH]")

func parseArgs(args []string) (command, error) {
	if len(args) == 0 {
		// The Service Control Manager starts the binary with the
		// arguments install gave it; a bare start is a person at a
		// console, who gets the usage.
		return command{}, errUsage
	}
	c := command{name: args[0]}
	fs := flag.NewFlagSet(c.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&c.config, "config", defaultConfig, "the helper's configuration")
	switch c.name {
	case "install", "run":
		if err := fs.Parse(args[1:]); err != nil {
			return command{}, fmt.Errorf("%s: %w", c.name, err)
		}
	case "uninstall", "version":
		if len(args) > 1 {
			return command{}, fmt.Errorf("%s takes no argument", c.name)
		}
	default:
		return command{}, errUsage
	}
	if fs.NArg() > 0 {
		return command{}, fmt.Errorf("%s: unexpected %q", c.name, fs.Arg(0))
	}
	return c, nil
}

// daemon is the helper's life, apart from how Windows starts and stops it.
type daemon struct {
	load   func(path string) (*helper.Config, error)
	listen func(socket, group string) (net.Listener, error)
	log    *slog.Logger
}

// start loads the configuration, listens and serves; stop takes the tunnel
// down and closes the socket.
func (d daemon) start(configPath string) (stop func(), err error) {
	cfg, err := d.load(configPath)
	if err != nil {
		return nil, err
	}
	ln, err := d.listen(cfg.Socket, cfg.Group)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", cfg.Socket, err)
	}
	srv := helper.New(*cfg, "windows", platform, d.log)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := srv.Serve(ln); err != nil {
			d.log.Error("serve", "err", err)
		}
	}()
	d.log.Info("claimward-helper listening", "socket", cfg.Socket, "group", cfg.Group, "servers", cfg.Servers, "version", version)
	return func() {
		srv.Shutdown()
		ln.Close()
		<-done
		d.log.Info("claimward-helper stopped")
	}, nil
}

func main() {
	c, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if c.name == "version" {
		fmt.Println(version)
		return
	}
	if err := runCommand(c); err != nil {
		fmt.Fprintln(os.Stderr, "claimward-helper:", err)
		os.Exit(1)
	}
}
