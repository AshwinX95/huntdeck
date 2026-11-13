package web

import (
	"image"
	"image/color"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// Drawing for the share images (ogimage.go): shapes anti-aliased with
// x/image/vector, text in the Go fonts.

// kappa places a cubic Bézier's control points for a quarter circle.
const kappa = 0.5523

// fillRoundRect fills r with corners of the given radius.
func fillRoundRect(dst *image.RGBA, r image.Rectangle, radius float32, c color.Color) {
	w, h := float32(r.Dx()), float32(r.Dy())
	radius = min(radius, w/2, h/2)
	k := radius * kappa
	z := vector.NewRasterizer(r.Dx(), r.Dy())
	z.MoveTo(radius, 0)
	z.LineTo(w-radius, 0)
	z.CubeTo(w-radius+k, 0, w, radius-k, w, radius)
	z.LineTo(w, h-radius)
	z.CubeTo(w, h-radius+k, w-radius+k, h, w-radius, h)
	z.LineTo(radius, h)
	z.CubeTo(radius-k, h, 0, h-radius+k, 0, h-radius)
	z.LineTo(0, radius)
	z.CubeTo(0, radius-k, radius-k, 0, radius, 0)
	z.ClosePath()
	z.Draw(dst, r, image.NewUniform(c), image.Point{})
}

// fillCircle fills a circle centred on (cx, cy).
func fillCircle(dst *image.RGBA, cx, cy, radius float32, c color.Color) {
	d := int(2*radius + 0.5)
	x, y := int(cx-radius+0.5), int(cy-radius+0.5)
	fillRoundRect(dst, image.Rect(x, y, x+d, y+d), radius, c) // a square with full corners
}

// fillGradient fills dst from top-left colour a to bottom-right b.
func fillGradient(dst *image.RGBA, a, b color.RGBA) {
	bd := dst.Bounds()
	span := float64(bd.Dx() + bd.Dy())
	for y := bd.Min.Y; y < bd.Max.Y; y++ {
		for x := bd.Min.X; x < bd.Max.X; x++ {
			t := float64(x+y) / span
			dst.SetRGBA(x, y, color.RGBA{lerp(a.R, b.R, t), lerp(a.G, b.G, t), lerp(a.B, b.B, t), 255})
		}
	}
}

func lerp(a, b uint8, t float64) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t + 0.5) }

// ogFonts are the faces a card is drawn with, made from parsed fonts.
type ogFonts struct{ bold, regular *opentype.Font }

func (f ogFonts) face(bold bool, size float64) font.Face {
	fnt := f.regular
	if bold {
		fnt = f.bold
	}
	face, err := opentype.NewFace(fnt, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil { // only for sizes out of range, which the callers' aren't
		panic(err)
	}
	return face
}

// drawText draws s with its baseline's left end at (x, y), and returns its
// width.
func drawText(dst *image.RGBA, face font.Face, x, y int, s string, c color.Color) int {
	d := font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, y)}
	d.DrawString(s)
	return (d.Dot.X - fixed.I(x)).Ceil()
}

func textWidth(face font.Face, s string) int { return font.MeasureString(face, s).Ceil() }

// fitText cuts s with an ellipsis to fit width.
func fitText(face font.Face, s string, width int) string {
	if textWidth(face, s) <= width {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && textWidth(face, strings.TrimSpace(string(r))+"…") > width {
		r = r[:len(r)-1]
	}
	return strings.TrimSpace(string(r)) + "…"
}

// wrapText breaks s into lines at most width wide, at spaces; a word wider
// than a line gets one to itself.
func wrapText(face font.Face, s string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		next := w
		if line != "" {
			next = line + " " + w
		}
		if line != "" && textWidth(face, next) > width {
			lines = append(lines, line)
			next = w
		}
		line = next
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
