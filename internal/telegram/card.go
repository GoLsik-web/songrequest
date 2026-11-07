package telegram

import (
	"bytes"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"
)

// Card produces a shareable image locally, with Cyrillic support and no browser.
func Card(channel string, lines []string) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, 1080, 1350))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{15, 18, 25, 255}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 0, 1080, 18), image.NewUniform(color.RGBA{200, 247, 81, 255}), image.Point{}, draw.Src)
	ft, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	text := func(s string, x, y, size int, c color.Color) error {
		face, err := opentype.NewFace(ft, &opentype.FaceOptions{Size: float64(size), DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return err
		}
		defer face.Close()
		for font.MeasureString(face, s) > fixed.I(930) && len([]rune(s)) > 1 {
			rr := []rune(strings.TrimSuffix(s, "…"))
			s = string(rr[:len(rr)-1]) + "…"
		}
		d := font.Drawer{Dst: img, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, y)}
		d.DrawString(s)
		return nil
	}
	if err = text("songrequest RECAP", 70, 115, 48, color.RGBA{200, 247, 81, 255}); err != nil {
		return nil, err
	}
	if err = text(channel, 70, 190, 32, color.White); err != nil {
		return nil, err
	}
	y := 285
	for _, line := range lines {
		if y > 1250 {
			break
		}
		if err = text(line, 70, y, 30, color.RGBA{229, 233, 242, 255}); err != nil {
			return nil, err
		}
		y += 72
	}
	var b bytes.Buffer
	err = png.Encode(&b, img)
	return b.Bytes(), err
}
