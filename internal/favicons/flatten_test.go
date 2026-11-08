package favicons

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// halves to obraz w×h: lewa połowa czerwona, prawa niebieska.
func halves(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			c := color.NRGBA{R: 255, A: 255}
			if x >= w/2 {
				c = color.NRGBA{B: 255, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

// Kształt pliku z programu graficznego: obraz w <defs>, <use> z macierzą, viewBox wycina fragment.
func wrapperSVG(viewBox, transform, href string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd">`+
		`<svg width="100%%" height="100%%" viewBox="%s" xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">`+
		`<use xlink:href="#_Image1" x="0" y="0" width="999px" height="999px" transform="%s"/>`+
		`<defs><image id="_Image1" width="200px" height="100px" xlink:href="%s"/></defs></svg>`, viewBox, transform, href)
}

func decoded(t *testing.T, icon Icon) image.Image {
	t.Helper()
	if icon.ContentType != "image/png" {
		t.Fatalf("content type %s", icon.ContentType)
	}
	img, err := png.Decode(bytes.NewReader(icon.Data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestFlattenCropsTheEmbeddedImageToTheViewBox(t *testing.T) {
	href := halves(t, 200, 100)
	for _, c := range []struct {
		transform string
		want      color.NRGBA
	}{
		{"matrix(1,0,0,1,-30,-25)", color.NRGBA{R: 255, A: 255}},
		{"translate(-120 -25)", color.NRGBA{B: 255, A: 255}},
		{"scale(0.5) translate(-100,0)", color.NRGBA{B: 255, A: 255}},
	} {
		icon, err := flattenSVG([]byte(wrapperSVG("0 0 50 50", c.transform, href)))
		if err != nil {
			t.Fatalf("%s: %v", c.transform, err)
		}
		img := decoded(t, icon)
		if b := img.Bounds(); b.Dx() != 64 || b.Dy() != 64 {
			t.Errorf("%s: %v, want 64x64", c.transform, b)
		}
		got := color.NRGBAModel.Convert(img.At(32, 32)).(color.NRGBA)
		if got != c.want {
			t.Errorf("%s: centre is %v, want %v", c.transform, got, c.want)
		}
	}
}

func TestFlattenLeavesWhatLiesOutsideTheImageTransparent(t *testing.T) {
	svg := `<svg viewBox="0 0 40 20" xmlns="http://www.w3.org/2000/svg">` +
		`<g transform="translate(20,0)"><image width="200" height="100" transform="scale(0.1)" href="` + halves(t, 200, 100) + `"/></g></svg>`
	icon, err := flattenSVG([]byte(svg))
	if err != nil {
		t.Fatal(err)
	}
	img := decoded(t, icon)
	if b := img.Bounds(); b.Dx() != 64 || b.Dy() != 32 {
		t.Fatalf("bounds %v, want 64x32 for a 2:1 viewBox", b)
	}
	if _, _, _, a := img.At(8, 16).RGBA(); a != 0 {
		t.Errorf("left half has alpha %d, want transparent", a)
	}
	// Obraz zajmuje x 20–40, y 0–10 viewBoxa; jego czerwona połowa to x 20–30.
	if got := color.NRGBAModel.Convert(img.At(40, 8)).(color.NRGBA); got.R != 255 || got.A != 255 {
		t.Errorf("image area is %v, want red", got)
	}
}

func TestNormalizeFlattensOnlyLargeSVGs(t *testing.T) {
	big := strings.Replace(wrapperSVG("0 0 50 50", "translate(-30,-25)", halves(t, 200, 100)),
		"<use", "<metadata>"+strings.Repeat("x", storedLimit)+"</metadata><use", 1)
	icon, err := normalize([]byte(big))
	if err != nil || icon.ContentType != "image/png" || len(icon.Data) > 4<<10 {
		t.Fatalf("large wrapper: %s, %d bytes, %v", icon.ContentType, len(icon.Data), err)
	}

	small := `<svg viewBox="0 0 1 1" xmlns="http://www.w3.org/2000/svg"><path d="M0 0h1v1z"/></svg>`
	if icon, err := normalize([]byte(small)); err != nil || icon.ContentType != "image/svg+xml" {
		t.Errorf("small SVG: %s, %v", icon.ContentType, err)
	}
}

func TestFlattenRefusesWhatItCannotDraw(t *testing.T) {
	href := halves(t, 20, 10)
	img := `<image id="i" width="20" height="10" href="` + href + `"/>`
	cases := map[string]string{
		"a shape besides the image": `<svg viewBox="0 0 20 10" xmlns="http://www.w3.org/2000/svg">` + img + `<path d="M0 0h1v1z"/></svg>`,
		"two images":                `<svg viewBox="0 0 20 10" xmlns="http://www.w3.org/2000/svg">` + img + img + `</svg>`,
		"a rotation":                `<svg viewBox="0 0 20 10" xmlns="http://www.w3.org/2000/svg"><g transform="rotate(45)">` + img + `</g></svg>`,
		"a clip path":               `<svg viewBox="0 0 20 10" xmlns="http://www.w3.org/2000/svg"><defs><clipPath id="c"/></defs>` + img + `</svg>`,
		"a linked image":            `<svg viewBox="0 0 20 10" xmlns="http://www.w3.org/2000/svg"><image width="20" height="10" href="https://example.com/a.png"/></svg>`,
		"a use of nothing":          `<svg viewBox="0 0 20 10" xmlns="http://www.w3.org/2000/svg"><use href="#missing"/></svg>`,
		"no size":                   `<svg xmlns="http://www.w3.org/2000/svg">` + img + `</svg>`,
	}
	for name, svg := range cases {
		if _, err := flattenSVG([]byte(svg)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseTransformComposesLeftToRight(t *testing.T) {
	m, err := parseTransform("translate(10, 20) scale(2)")
	if err != nil {
		t.Fatal(err)
	}
	// Punkt (1,1): najpierw skala do (2,2), potem przesunięcie do (12,22).
	if x, y := m.a*1+m.c*1+m.e, m.b*1+m.d*1+m.f; x != 12 || y != 22 {
		t.Errorf("(1,1) → (%v,%v), want (12,22)", x, y)
	}
	for _, bad := range []string{"rotate(10)", "skewX(5)", "matrix(1,2)", "translate(", "scale(a)"} {
		if _, err := parseTransform(bad); err == nil {
			t.Errorf("parseTransform(%q) accepted", bad)
		}
	}
}
