package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

const iconSize = 64

// Colors for each Health, matching the dashboard.
var healthColors = map[Health]color.NRGBA{
	Idle: {0x8a, 0x8a, 0x84, 0xff},
	OK:   {0x1f, 0x8a, 0x4c, 0xff},
	Warn: {0xd9, 0x8e, 0x1c, 0xff},
	Bad:  {0xc0, 0x39, 0x2b, 0xff},
}

// Icon returns the tray icon for h in the format the platform expects: PNG,
// or ICO (wrapping the PNG) on Windows. Drawn in code, so no image assets.
func Icon(h Health, goos string) []byte {
	p := circlePNG(healthColors[h])
	if goos == "windows" {
		return pngToICO(p, iconSize)
	}
	return p
}

// circlePNG draws an anti-aliased filled circle on a transparent square.
func circlePNG(c color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, iconSize, iconSize))
	center := float64(iconSize) / 2
	radius := center - 6 // leave padding so it reads as a dot in the menu bar
	for y := range iconSize {
		for x := range iconSize {
			d := math.Hypot(float64(x)+0.5-center, float64(y)+0.5-center)
			coverage := math.Max(0, math.Min(1, radius-d+0.5))
			if coverage > 0 {
				img.SetNRGBA(x, y, color.NRGBA{c.R, c.G, c.B, uint8(float64(c.A) * coverage)})
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// pngToICO wraps a PNG in a single-image ICO container (supported since
// Windows Vista).
func pngToICO(pngData []byte, size int) []byte {
	var buf bytes.Buffer
	dim := byte(size)
	if size >= 256 {
		dim = 0 // 0 means 256 in ICO
	}
	le := binary.LittleEndian
	_ = binary.Write(&buf, le, [3]uint16{0, 1, 1}) // reserved, type=icon, count
	buf.Write([]byte{dim, dim, 0, 0})              // width, height, palette, reserved
	_ = binary.Write(&buf, le, [2]uint16{1, 32})   // color planes, bits per pixel
	_ = binary.Write(&buf, le, [2]uint32{uint32(len(pngData)), 6 + 16})
	buf.Write(pngData)
	return buf.Bytes()
}
