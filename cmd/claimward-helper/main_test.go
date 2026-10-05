// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/claimward/claimward-vpn-client/pkg/helper"
	"github.com/claimward/claimward-vpn-client/pkg/helperclient"
)

func TestParseArgs(t *testing.T) {
	for _, c := range []struct {
		args []string
		want command
	}{
		{[]string{"run"}, command{"run", defaultConfig}},
		{[]string{"run", "-config", `D:\c\helper.json`}, command{"run", `D:\c\helper.json`}},
		{[]string{"install"}, command{"install", defaultConfig}},
		{[]string{"install", "-config=x.json"}, command{"install", "x.json"}},
		{[]string{"uninstall"}, command{"uninstall", defaultConfig}},
		{[]string{"version"}, command{"version", defaultConfig}},
	} {
		got, err := parseArgs(c.args)
		if err != nil || got != c.want {
			t.Errorf("%q: %+v %v, want %+v", c.args, got, err, c.want)
		}
	}
	for _, bad := range [][]string{nil, {"start"}, {"run", "-socket", "x"}, {"run", "extra"}, {"uninstall", "now"}, {"version", "-v"}} {
		if c, err := parseArgs(bad); err == nil {
			t.Errorf("%q was taken: %+v", bad, c)
		}
	}
	if _, err := parseArgs(nil); !errors.Is(err, errUsage) {
		t.Errorf("a bare start does not print the usage: %v", err)
	}
}

// The daemon listens on what its configuration says and answers the app's
// client; stopping it closes the socket.
func TestDaemonServesAndStops(t *testing.T) {
	dir, err := os.MkdirTemp("", "cwd")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "h.sock")
	var gotPath, gotGroup string
	d := daemon{
		load: func(p string) (*helper.Config, error) {
			gotPath = p
			return &helper.Config{Servers: []string{"https://vpn.example.org"}, Socket: sock, Group: "Claimward Users"}, nil
		},
		listen: func(s, g string) (net.Listener, error) { gotGroup = g; return net.Listen("unix", s) },
		log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	stop, err := d.start("helper.json")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "helper.json" || gotGroup != "Claimward Users" {
		t.Errorf("loaded %q, listened for %q", gotPath, gotGroup)
	}
	c := helperclient.New(sock)
	resp, err := c.Status()
	if err != nil || !resp.OK || resp.Connected {
		t.Fatalf("status %+v %v", resp, err)
	}
	stop()
	if c.Available() {
		t.Error("the socket still answers after stop")
	}
}

func TestDaemonRefusesToStartWithoutItsConfiguration(t *testing.T) {
	d := daemon{
		load: func(string) (*helper.Config, error) { return nil, errors.New("helper.json: owned by a user") },
		listen: func(string, string) (net.Listener, error) {
			t.Fatal("listened without a configuration")
			return nil, nil
		},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if _, err := d.start("x"); err == nil {
		t.Fatal("started")
	}
	d.load = func(string) (*helper.Config, error) { return &helper.Config{Socket: "/nonexistent/dir/h.sock"}, nil }
	d.listen = func(s, _ string) (net.Listener, error) { return net.Listen("unix", s) }
	if _, err := d.start("x"); err == nil {
		t.Fatal("started without a socket")
	}
}
