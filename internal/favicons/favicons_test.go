package favicons

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"strings"
	"testing"
)

func TestSiteOfCanonicalizesHostAndDropsDefaultPorts(t *testing.T) {
	cases := []struct {
		in, key, origin string
	}{
		{"https://Example.COM./path?q=1", "example.com", "https://example.com"},
		{"http://example.com:80/", "example.com", "http://example.com"},
		{"https://example.com:443", "example.com", "https://example.com"},
		{"https://example.com:8443/x", "example.com:8443", "https://example.com:8443"},
		{"http://[2001:db8::1]/", "[2001:db8::1]", "http://[2001:db8::1]"},
		{"http://[2001:db8::1]:8080/", "[2001:db8::1]:8080", "http://[2001:db8::1]:8080"},
	}
	for _, c := range cases {
		site, ok := SiteOf(c.in)
		if !ok || site.Key != c.key || site.Origin != c.origin {
			t.Errorf("SiteOf(%q) = %+v, %v; want key %q origin %q", c.in, site, ok, c.key, c.origin)
		}
	}
	for _, bad := range []string{"", "ftp://example.com", "example.com", "https://", "javascript:alert(1)"} {
		if _, ok := SiteOf(bad); ok {
			t.Errorf("SiteOf(%q) accepted", bad)
		}
	}
}

func TestIconsForSharesOneIconAcrossSchemes(t *testing.T) {
	icons := Icons{"example.com": "abc"}
	for _, target := range []string{"https://example.com/a", "http://EXAMPLE.com"} {
		if got := icons.For(target); got == nil || *got != "abc" {
			t.Errorf("For(%q) = %v", target, got)
		}
	}
	if got := icons.For("https://other.com"); got != nil {
		t.Errorf("For(other) = %v, want nil", *got)
	}
	var none Icons
	if got := none.For("https://example.com"); got != nil {
		t.Errorf("nil Icons.For = %v", *got)
	}
}

func TestValidHash(t *testing.T) {
	if !ValidHash(strings.Repeat("a1", 32)) {
		t.Error("valid hash rejected")
	}
	for _, bad := range []string{"", strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 63), "../" + strings.Repeat("a", 61)} {
		if ValidHash(bad) {
			t.Errorf("ValidHash(%q) accepted", bad)
		}
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func urls(cs []candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		if c.URL == "" {
			out[i] = "data"
		} else {
			out[i] = c.URL
		}
	}
	return out
}

func TestDiscoverOrdersCandidatesForA16pxSlot(t *testing.T) {
	page := `<!doctype html><html><head>
		<link rel="apple-touch-icon" href="/apple.png">
		<link rel="icon" href="/16.png" sizes="16x16">
		<link rel="icon" href="/big.png" sizes="192x192">
		<link rel="mask-icon" href="/mask.svg">
		<link rel="icon" href="/plain.ico">
		<link rel="ICON" type="image/svg+xml" href="/vector.svg">
		<link rel="shortcut icon" href="/48.png" sizes="16x16 48x48">
		<link rel="icon" href="/32.png" sizes="32x32">
		<link rel="stylesheet" href="/site.css">
	</head><body><link rel="icon" href="/in-body.png"></body></html>`

	got := urls(discover([]byte(page), mustURL(t, "https://example.com/")))
	want := []string{
		"https://example.com/32.png",
		"https://example.com/48.png",
		"https://example.com/vector.svg",
		"https://example.com/plain.ico",
		"https://example.com/big.png",
		"https://example.com/16.png",
		"https://example.com/apple.png",
		"https://example.com/favicon.ico",
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("discover order\n got  %v\n want %v", got, want)
	}
}

func TestDiscoverHonoursBaseAndInlineIcons(t *testing.T) {
	page := `<head><base href="https://cdn.example.net/assets/"><base href="/ignored/">
		<link rel=icon href="fav.png">
		<link rel=icon href="data:image/png;base64,iVBORw0KGgo=">
		<link rel=icon href="javascript:alert(1)">
		<link rel=icon href="/favicon.ico"></head>`

	got := discover([]byte(page), mustURL(t, "https://example.com/page"))
	if want := "https://cdn.example.net/assets/fav.png data https://cdn.example.net/favicon.ico"; strings.Join(urls(got), " ") != want {
		t.Fatalf("discover = %v, want %s", urls(got), want)
	}
	if !bytes.HasPrefix(got[1].data, pngSignature) {
		t.Errorf("inline icon not decoded: %q", got[1].data)
	}
}

func TestDiscoverOnAnEmptyPageFallsBackToFaviconICO(t *testing.T) {
	got := urls(discover(nil, mustURL(t, "https://example.com/")))
	if len(got) != 1 || got[0] != "https://example.com/favicon.ico" {
		t.Errorf("discover(nil) = %v", got)
	}
}

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 200, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// icoOf składa plik ICO z podanych obrazów; edge 256 zapisuje się jako 0.
func icoOf(edges []int, images [][]byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{0, 0, 1, 0})
	_ = binary.Write(&b, binary.LittleEndian, uint16(len(images)))
	offset := 6 + 16*len(images)
	for i, data := range images {
		edge := byte(edges[i])
		if edges[i] == 256 {
			edge = 0
		}
		b.Write([]byte{edge, edge, 0, 0, 1, 0, 32, 0})
		_ = binary.Write(&b, binary.LittleEndian, uint32(len(data)))
		_ = binary.Write(&b, binary.LittleEndian, uint32(offset))
		offset += len(data)
	}
	for _, data := range images {
		b.Write(data)
	}
	return b.Bytes()
}

func edgeOf(t *testing.T, data []byte) int {
	t.Helper()
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a PNG: %v", err)
	}
	return cfg.Width
}

func TestNormalizeKeepsOnlyTheFittingEntryOfAnICO(t *testing.T) {
	raw := icoOf([]int{16, 256, 32}, [][]byte{pngOf(t, 16, 16), pngOf(t, 256, 256), pngOf(t, 32, 32)})
	icon, err := normalize(raw)
	if err != nil {
		t.Fatal(err)
	}
	if icon.ContentType != "image/png" || edgeOf(t, icon.Data) != 32 {
		t.Errorf("got %s %dpx, want the 32px PNG entry", icon.ContentType, edgeOf(t, icon.Data))
	}
	if len(icon.Data) >= len(raw)/2 {
		t.Errorf("stored %d bytes of a %d byte ICO", len(icon.Data), len(raw))
	}
}

func TestNormalizeRewritesABitmapICOWithOneEntry(t *testing.T) {
	bmp16 := bytes.Repeat([]byte{1}, 40)
	bmp48 := bytes.Repeat([]byte{2}, 60)
	icon, err := normalize(icoOf([]int{16, 48}, [][]byte{bmp16, bmp48}))
	if err != nil {
		t.Fatal(err)
	}
	if icon.ContentType != "image/x-icon" {
		t.Fatalf("content type %s", icon.ContentType)
	}
	d := icon.Data
	if binary.LittleEndian.Uint16(d[4:6]) != 1 || d[6] != 48 ||
		binary.LittleEndian.Uint32(d[6+12:]) != 22 || !bytes.Equal(d[22:], bmp48) {
		t.Errorf("rewritten ICO is wrong: % x", d[:22])
	}
}

func TestNormalizeScalesLargeImagesDownAndKeepsSmallOnesAsTheyAre(t *testing.T) {
	small := pngOf(t, 32, 32)
	icon, err := normalize(small)
	if err != nil || !bytes.Equal(icon.Data, small) {
		t.Errorf("small PNG changed: %v", err)
	}

	icon, err = normalize(pngOf(t, 180, 90))
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := png.DecodeConfig(bytes.NewReader(icon.Data))
	if cfg.Width != 64 || cfg.Height != 32 {
		t.Errorf("scaled to %dx%d, want 64x32", cfg.Width, cfg.Height)
	}
}

// Nagłówek PNG z poprawną sumą kontrolną, który obiecuje obraz 50000×50000:
// odrzucony po samym nagłówku, zanim dekoder zarezerwuje pamięć.
func TestNormalizeRefusesHugeDimensionsBeforeDecoding(t *testing.T) {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], 50000)
	binary.BigEndian.PutUint32(ihdr[4:], 50000)
	ihdr[8], ihdr[9] = 8, 6
	var b bytes.Buffer
	b.Write(pngSignature)
	_ = binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	chunk := append([]byte("IHDR"), ihdr...)
	b.Write(chunk)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))

	if _, err := normalize(b.Bytes()); err == nil {
		t.Error("50000x50000 PNG accepted")
	}
}

func TestNormalizeTellsSVGFromOtherMarkup(t *testing.T) {
	ok := []string{
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"/>`,
		"\xef\xbb\xbf<?xml version=\"1.0\"?>\n<!-- logo -->\n<!DOCTYPE svg>\n<SVG viewBox=\"0 0 1 1\"></SVG>",
	}
	for _, s := range ok {
		icon, err := normalize([]byte(s))
		if err != nil || icon.ContentType != "image/svg+xml" {
			t.Errorf("SVG %q rejected: %v", s, err)
		}
	}
	bad := []string{
		`<html><body><svg></svg></body></html>`,
		`<svgfoo></svgfoo>`,
		`<!-- <svg> -->`,
		"plain text",
		"<svg>\xff</svg>",
	}
	for _, s := range bad {
		if _, err := normalize([]byte(s)); err == nil {
			t.Errorf("%q accepted as an icon", s)
		}
	}
}
