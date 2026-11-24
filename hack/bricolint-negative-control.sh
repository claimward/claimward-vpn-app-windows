#!/usr/bin/env bash
# Copyright (c) 2026 the Claimward authors. All rights reserved.
# Use of this source code is governed by a BSD-3-Clause license.
#
# Negative control for the bricolint hand-drawn-UI guard.
#
# A green `go vet -vettool=bricolint` only proves the guard is SILENT on this
# tree; it does not prove the guard can still BITE. A guard that never fires
# (wrong receiver-type resolution, an analyzer that no-ops, a stale binary)
# would pass CI while protecting nothing. This script proves the guard is
# live by driving it through the full state machine:
#
#   1. clean tree                      -> bricolint exits 0
#   2. inject a raw painter draw call  -> bricolint exits non-zero
#   3. remove the injection            -> bricolint exits 0
#
# The injection is a genuine drawing primitive (`FillRect`) called on the
# real *painter.PixelPainter that scene.go's draw() constructs — the exact
# leak the guard exists to catch — so a pass here means the guard bites on
# THIS app's own render path, not on a synthetic fixture.
#
# Usage:
#   BRICOLINT=/path/to/bricolint bash hack/bricolint-negative-control.sh
# BRICOLINT defaults to "$(go env GOPATH)/bin/bricolint".
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/.." && pwd)"
bricolint="${BRICOLINT:-$(go env GOPATH)/bin/bricolint}"

# The real painter-using render method and the line the app constructs its
# PixelPainter on. The injection is spliced in immediately AFTER this anchor,
# so it lands inside Frame() with `p` in scope and statically typed as
# *painter.PixelPainter — the receiver bricolint resolves to go-widgets/painter.
target="$root/internal/ui/host.go"
anchor='p := painter.NewPixelPainter(v.buf, v.width, v.high)'
marker='//bricolint-negative-control-injection'
inject="	p.FillRect(painter.Rect{}, painter.RGBA{}) ${marker}"

if [ ! -x "$bricolint" ]; then
  echo "error: bricolint binary not found or not executable: $bricolint" >&2
  exit 2
fi
if ! grep -qF "$anchor" "$target"; then
  echo "error: anchor not found in $target; update the negative-control anchor" >&2
  exit 2
fi

# Run the guard and report its exit status without aborting the script
# (set -e would otherwise kill us on the expected non-zero run).
guard() {
  ( cd "$root" && GOWORK=off go vet -vettool="$bricolint" ./... ) >/dev/null 2>&1
  echo $?
}

# Always restore the pristine file, however we leave — but ONLY once a real
# backup has been taken. `restore_armed` guards against a failure before the cp
# below causing the trap to copy an empty temp over $target and wipe it; the
# [ -s ] check refuses to restore an empty backup even then.
backup="$(mktemp)"
restore_armed=0
restore() { [ "$restore_armed" = 1 ] && [ -s "$backup" ] && cp "$backup" "$target"; rm -f "$backup"; return 0; }
trap restore EXIT
cp "$target" "$backup"; restore_armed=1

echo "==> 1/3 clean tree: guard must pass (exit 0)"
rc=$(guard)
if [ "$rc" != "0" ]; then
  echo "FAIL: guard did not pass on the clean tree (exit $rc)" >&2
  exit 1
fi
echo "    ok: exit 0"

echo "==> 2/3 inject a raw painter draw call: guard must bite (exit != 0)"
# awk splice: echo the anchor line, then the injection right after it.
awk -v anchor="$anchor" -v inj="$inject" '
  { print }
  index($0, anchor) { print inj }
' "$backup" > "$target"
if ! grep -qF "$marker" "$target"; then
  echo "FAIL: injection was not applied to $target" >&2
  exit 1
fi
rc=$(guard)
if [ "$rc" = "0" ]; then
  echo "FAIL: guard stayed green with a raw p.FillRect in Frame() — it does not bite" >&2
  exit 1
fi
echo "    ok: guard fired (exit $rc)"

echo "==> 3/3 remove the injection: guard must pass again (exit 0)"
restore
trap - EXIT
rc=$(guard)
if [ "$rc" != "0" ]; then
  echo "FAIL: guard did not return to green after removing the injection (exit $rc)" >&2
  exit 1
fi
echo "    ok: exit 0"

echo "PASS: the bricolint guard bites on a real painter leak and is silent otherwise"
