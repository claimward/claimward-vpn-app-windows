// SPDX-License-Identifier: BSD-3-Clause

package brand

import (
	"bytes"
	"image/png"
	"testing"
)

func TestTheTrayIconIsA32PixelPNG(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(TrayIcon))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 32 || b.Dy() != 32 {
		t.Errorf("%v", b)
	}
}
