# claimward-vpn-app-windows

[![CI](https://github.com/claimward/claimward-vpn-app-windows/actions/workflows/ci.yml/badge.svg)](https://github.com/claimward/claimward-vpn-app-windows/actions/workflows/ci.yml)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)

The **Claimward VPN** client for Windows: a desktop app with a notification-area
(tray) icon that signs you in (GitHub, OpenID Connect or go-authn), lets you
choose a tenant, and brings a WireGuard tunnel up and down through a privileged
helper service.

Everything is Go with `CGO_ENABLED=0`. The window and the tray are drawn by
[go-widgets](https://github.com/go-widgets) — no webview, no browser engine, no
HTTP server in the app. The tunnel is [wireguard-go](https://git.zx2c4.com/wireguard-go)
on a [Wintun](https://www.wintun.net) adapter, configured through the IP Helper
API.

It is the Windows sibling of
[claimward-vpn-app-osx](https://github.com/claimward/claimward-vpn-app-osx);
both are thin shells over the shared core in
[claimward-vpn-client](https://github.com/claimward/claimward-vpn-client)
(`pkg/appcore`, `pkg/helper`, `pkg/wgtun`), which talks to
[claimward-vpn-server](https://github.com/claimward/claimward-vpn-server).

## How it is put together

```
┌──────────── claimward-app.exe (the person, unprivileged) ────────────┐
│ internal/ui/view.go        widgets (go-widgets/toolkit), bound by     │
│                            go-widgets/mvvmtk to ...                   │
│ internal/ui/viewmodel.go   ... the ViewModel: all state, as           │
│                            go-widgets/mvvm observables and commands   │
│ internal/ui/tray_binding   the tray menu, bound to the same ViewModel │
│ internal/ui/host.go        the window's application.Handler           │
│ appcore.Core               sign-in, session, tenant, helper client    │
└──────────────────────────────────┬───────────────────────────────────┘
                                   │ AF_UNIX socket, one JSON request each
                                   │ C:\ProgramData\Claimward\helper.sock
┌──────────────────────────────────▼───────────────────────────────────┐
│ claimward-helper.exe — the ClaimwardHelper service (LocalSystem)      │
│ pkg/helper   enrolls with a server helper.json names, owns the tunnel │
│ pkg/wgtun    wireguard-go on a Wintun adapter "Claimward";            │
│              address, routes, DNS, MTU via winipcfg                   │
└──────────────────────────────────────────────────────────────────────┘
```

**MVVM throughout.** `viewmodel.go` holds every piece of state and imports no
widget; `view.go` builds the widgets and binds each one (labels, buttons to
commands, the tenant and provider drop-downs, the settings fields, the log
list, and the cards that show the device-flow prompt, the tenant choice or the
settings page). Nothing copies state into a widget per frame: the window
repaints when a binding says something changed. The tray menu is rebuilt from
the ViewModel when what it shows changes. CI enforces it with
[mvvmlint](https://github.com/go-widgets/mvvmlint) (no direct widget-state
writes) and [bricolint](https://github.com/go-widgets/bricolint) (no
hand-drawn UI), whose negative control proves the guard still bites.

**Threads.** The window's thread is the only one that touches the ViewModel.
Work that blocks — the helper over its socket, the identity provider — runs on
a goroutine and posts its result back through a queue that the window drains
at the top of each frame. The status is polled every 2 seconds the same way. A
tray click arrives on the tray's thread and is posted likewise.

| Path | What |
|------|------|
| `cmd/claimward-app` | the app: window + tray (`application.Run`), Windows window lookup and the browser opener |
| `cmd/claimward-helper` | the helper service: `install`, `uninstall`, `run`, `version` (golang.org/x/sys/windows/svc) |
| `internal/ui` | ViewModel, View, tray binding, window handler — 100% covered by tests that run on any OS |
| `internal/brand` | the tray icon (from [claimward/brand](https://github.com/claimward/brand)) |
| `scripts/install.ps1`, `scripts/uninstall.ps1` | install / remove on a machine |
| `scripts/package.sh` | builds the release folders and zips, with Wintun |
| `hack/` | the bricolint negative control; `helperping`, which CI uses to ask the installed helper for its status |

### What the app does

The window shows, like the macOS app's: whether you are connected, the address
and interface, who is signed in, the tenant, whether the helper answers; the
**sign-in** device-flow prompt — the code to type and a button that opens the
page in your default browser (only an `https` page is ever handed to the shell);
**Connect / Disconnect / Sign out**; **Choose tenant**, which asks the server
for your tenants (a person in one tenant never chooses: the server uses it; a
person in several who connects without choosing is told to choose, and the
choice appears); the **settings** (server URL, provider, GitHub client id or
OIDC issuer and client id), saved to `%AppData%\Claimward\config.json`; and the
connection log. The tray menu shows the status and offers Connect, Disconnect,
*Open Claimward* and Quit.

Closing the window quits the app. The tunnel belongs to the helper service and
stays up; Disconnect takes it down.

## Install

From a release folder (`scripts/package.sh` builds them), in an **elevated**
PowerShell:

```powershell
Set-ExecutionPolicy -Scope Process Bypass
.\install.ps1 -Server https://vpn.example.org
# optionally, the app's settings for the current user as well:
.\install.ps1 -Server https://vpn.example.org -Provider github -GitHubClientId Iv1.0123456789abcdef
```

It copies the programs and `wintun.dll` to `C:\Program Files\Claimward`,
creates the local group **Claimward Users** and adds you to it, writes
`C:\ProgramData\Claimward\helper.json`, registers and starts the
**ClaimwardHelper** service, and adds a **Claimward VPN** Start Menu shortcut.
Group membership takes effect at your **next sign-in to Windows**; until then
the helper's socket refuses you (the app says the helper is not reachable).
Add other people with `Add-LocalGroupMember -Group 'Claimward Users' -Member <name>`.

`.\uninstall.ps1` removes the service, the shortcut and the programs;
`-RemoveData` also removes `C:\ProgramData\Claimward` and the group.

There is no MSI: a zip and a PowerShell script cover a first release and keep
the build cross-platform; an MSI (WiX) is future work.

### Wintun

`wintun.dll` must sit beside `claimward-helper.exe`. It is **not** in this
repository: `scripts/package.sh` downloads
`https://www.wintun.net/builds/wintun-0.14.1.zip` and refuses it unless its
SHA-256 is `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51`,
then copies the DLL for the right architecture and its licence
(`wintun-LICENSE.txt`) into the package. The DLL is signed by WireGuard LLC;
its licence allows distributing it alongside software that uses it only
through its API.

## Configuration

**The app** reads `%AppData%\Claimward\config.json` (then `CLAIMWARD_*`
environment variables); the settings page writes it:

```json
{
  "server_url": "https://vpn.example.org",
  "provider": "github",
  "github_client_id": "Iv1.0123456789abcdef"
}
```

`provider` is `github` (device flow, the default), `oidc` or `go-authn`; the
last two take `oidc_issuer` and `oidc_client_id` instead of
`github_client_id`. `socket_path` overrides the helper's socket. The session
(tokens and the device's WireGuard key) is kept by `appcore` in
`%AppData%\claimward\session.json`.

**The helper** reads `C:\ProgramData\Claimward\helper.json`:

```json
{
  "servers": ["https://vpn.example.org"],
  "group": "Claimward Users",
  "socket": "C:\\ProgramData\\Claimward\\helper.sock"
}
```

`servers` is required: the helper enrolls with these servers and no other.
`group` (default `Claimward Users`) and `socket` (default as shown) are
optional. The service logs to `C:\ProgramData\Claimward\helper.log`. From an
elevated console, `claimward-helper run` runs it in the foreground, logging
to the console.

## Security notes

- **The helper is pinned to its configured servers.** Any process that can
  reach its socket can ask it to connect, so it enrolls only with a server
  `helper.json` names, and takes no tunnel configuration from a request: the
  tunnel is what that server answered. A local process cannot point it at a
  server of its own and route the machine's traffic there.
- **The socket's directory ACL.** The helper creates (or tightens)
  `C:\ProgramData\Claimward` with a **protected** DACL: SYSTEM and
  Administrators in full control; **Claimward Users** may list and traverse
  it, nothing more; nothing inherited from ProgramData, which lets every user
  create files there. A directory that is a junction or a link is refused. The
  socket itself grants SYSTEM, Administrators and the group read/write. If the
  group does not exist the helper falls back to **INTERACTIVE** (everyone
  logged on at the machine, console or Remote Desktop), and logs that it did.
- **The configuration's ACL.** The helper refuses to start unless
  `helper.json` is owned by SYSTEM or Administrators and nobody else may
  write, append, delete, or change its ACL or owner. `install.ps1` writes it
  that way.
- **The program files.** The service runs `C:\Program Files\Claimward\claimward-helper.exe`,
  which only administrators can replace; so is `wintun.dll` beside it.
- **The browser.** The sign-in page URL comes from the identity provider over
  the network; the app opens it only if it is an `https` URL, since on Windows
  the shell would as well run a program named there.
- The session file is per-user under `%AppData%`; moving it to the Windows
  Credential Manager (DPAPI) is future work.

These rules are implemented in claimward-vpn-client (`pkg/helper/acl.go`,
`listen_windows.go`) and tested there, on every OS for the rules and on a
Windows runner for the ACLs as applied.

## Development

```sh
go test ./...                      # the ViewModel, View and tray tests run on any OS
GOOS=windows go vet ./...
GOOS=windows GOARCH=arm64 go build ./...
go run ./cmd/claimward-app         # the UI runs on macOS and Linux too (no tunnel there)
./scripts/package.sh               # dist/claimward-windows-{amd64,arm64}[-<version>.zip]
```

The gates CI runs:

```sh
test -z "$(gofmt -l .)"
# the MVVM gate is the reusable workflow go-widgets/mvvmlint/.github/workflows/mvvmlint.yml@main;
# locally, its equivalent:
go install github.com/go-widgets/mvvmlint/cmd/mvvmlint@latest
go vet -vettool="$(go env GOPATH)/bin/mvvmlint" ./...
go install github.com/go-widgets/bricolint/cmd/bricolint@v0.1.0
go vet -vettool="$(go env GOPATH)/bin/bricolint" ./...
BRICOLINT="$(go env GOPATH)/bin/bricolint" bash hack/bricolint-negative-control.sh
```

`internal/ui` is held at 100% statement coverage; the window and tray run loop
(`cmd/claimward-app`) and the service glue are exercised on the Windows runner
instead: CI packages the app, runs `install.ps1` on `windows-latest`, checks
that the service runs, the ACLs are as described and the helper answers on its
socket, then runs `uninstall.ps1`.

## License

BSD-3-Clause — see [LICENSE](LICENSE). `wintun.dll`, shipped in the release
packages and not in this repository, is WireGuard LLC's, under its own licence.
