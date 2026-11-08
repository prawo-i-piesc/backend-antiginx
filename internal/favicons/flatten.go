package favicons

import (
	"bytes"
	"encoding/xml"
	"errors"
	"image"
	"image/png"
	"io"
	"math"
	"strconv"
	"strings"

	"golang.org/x/image/draw"
)

// flattenSVG zamienia SVG, który tylko opakowuje jeden osadzony obraz
// rastrowy, w mały PNG z tym, co widać w jego viewBox. Tak eksportują ikony
// programy graficzne: plik waży tyle co obraz w środku, często ponad megabajt,
// a pokazujemy 16 px. Obraz jest kadrowany i skalowany tak jak w przeglądarce:
// przekształcenia z transform, przesunięcie z <use> i dopasowanie
// xMidYMid meet do pola obrazu.
//
// Bez rasteryzatora SVG nie narysujemy kształtów, więc plik z czymkolwiek poza
// tym jednym obrazem, z obrotem albo z przycinaniem, maską czy filtrem jest
// odrzucany.
func flattenSVG(raw []byte) (Icon, error) {
	doc, err := readWrapper(raw)
	if err != nil {
		return Icon{}, err
	}

	data, ok := decodeDataURL(doc.image.href)
	if !ok {
		return Icon{}, errUnsupported
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > decodeLimit || cfg.Height > decodeLimit {
		return Icon{}, errUnsupported
	}

	// Gdzie piksele obrazu lądują w układzie viewBox: px → (a·px + e, d·py + f).
	pw, ph := float64(cfg.Width), float64(cfg.Height)
	box := doc.image
	sx, sy := box.w/pw, box.h/ph
	if !box.stretch {
		s := math.Min(sx, sy)
		sx, sy = s, s
	}
	m := doc.transform.times(affine{a: sx, d: sy, e: box.x + (box.w-pw*sx)/2, f: box.y + (box.h-ph*sy)/2})
	if m.b != 0 || m.c != 0 || m.a <= 0 || m.d <= 0 {
		return Icon{}, errUnsupported
	}

	// viewBox w pikselach obrazu; może wystawać poza obraz, tam jest przezroczyście.
	vb := doc.viewBox
	x0, x1 := (vb.x-m.e)/m.a, (vb.x+vb.w-m.e)/m.a
	y0, y1 := (vb.y-m.f)/m.d, (vb.y+vb.h-m.f)/m.d
	cx0, cx1 := math.Max(x0, 0), math.Min(x1, pw)
	cy0, cy1 := math.Max(y0, 0), math.Min(y1, ph)
	if cx1-cx0 < 1 || cy1-cy0 < 1 {
		return Icon{}, errUnsupported
	}

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Icon{}, errUnsupported
	}

	ow, oh := fit(vb.w, vb.h)
	dst := image.NewNRGBA(image.Rect(0, 0, ow, oh))
	at := func(v, from, to float64, size int) int {
		return int(math.Round((v - from) / (to - from) * float64(size)))
	}
	dr := image.Rect(at(cx0, x0, x1, ow), at(cy0, y0, y1, oh), at(cx1, x0, x1, ow), at(cy1, y0, y1, oh))
	sr := image.Rect(int(math.Floor(cx0)), int(math.Floor(cy0)), int(math.Ceil(cx1)), int(math.Ceil(cy1)))
	if dr.Empty() {
		return Icon{}, errUnsupported
	}
	draw.CatmullRom.Scale(dst, dr, src, sr.Add(src.Bounds().Min), draw.Src, nil)

	var out bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&out, dst); err != nil {
		return Icon{}, err
	}
	return Icon{ContentType: "image/png", Data: out.Bytes()}, nil
}

// fit dopasowuje proporcje w×h do kwadratu maxEdge na dłuższym boku.
func fit(w, h float64) (int, int) {
	if w >= h {
		return maxEdge, max(1, int(math.Round(maxEdge*h/w)))
	}
	return max(1, int(math.Round(maxEdge*w/h))), maxEdge
}

type rect struct{ x, y, w, h float64 }

type svgImage struct {
	rect
	href string
	// stretch to preserveAspectRatio="none"; domyślnie obraz mieści się w polu bez zniekształceń.
	stretch bool
	// own to transform samego elementu, acc — razem z przodkami.
	own, acc affine
	inDefs   bool
	id       string
}

type svgUse struct {
	ref  string
	x, y float64
	acc  affine
}

type wrapper struct {
	viewBox   rect
	image     svgImage
	transform affine
}

var errNotWrapper = errors.New("favicons: SVG rysuje coś więcej niż jeden obraz")

// Elementy, które coś rysują albo zmieniają to, co widać. Poza <defs> wystarczy
// jeden, żeby plik nie był samym opakowaniem obrazu.
var drawing = map[string]bool{
	"path": true, "rect": true, "circle": true, "ellipse": true, "line": true,
	"polyline": true, "polygon": true, "text": true, "foreignobject": true, "svg": true,
}

// Te zmieniają wygląd także z <defs>, przez odwołanie.
var effects = map[string]bool{"clippath": true, "mask": true, "filter": true, "pattern": true}

func readWrapper(raw []byte) (wrapper, error) {
	dec := xml.NewDecoder(bytes.NewReader(raw))
	type frame struct {
		acc    affine
		inDefs bool
	}
	stack := []frame{{acc: identity}}
	var doc wrapper
	var images []svgImage
	var uses []svgUse
	root := true

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return wrapper{}, errUnsupported
		}

		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			attrs := make(map[string]string, len(t.Attr))
			for _, a := range t.Attr {
				attrs[strings.ToLower(a.Name.Local)] = a.Value
			}
			parent := stack[len(stack)-1]
			own, err := parseTransform(attrs["transform"])
			if err != nil || effects[name] || attrs["clip-path"] != "" || attrs["mask"] != "" || attrs["filter"] != "" {
				return wrapper{}, errNotWrapper
			}
			current := frame{acc: parent.acc.times(own), inDefs: parent.inDefs || name == "defs"}

			switch {
			case root:
				if name != "svg" {
					return wrapper{}, errUnsupported
				}
				vb, ok := viewBoxOf(attrs)
				if !ok {
					return wrapper{}, errUnsupported
				}
				doc.viewBox = vb
				root = false
			case name == "image":
				r, ok := rectOf(attrs)
				if !ok {
					return wrapper{}, errNotWrapper
				}
				images = append(images, svgImage{
					rect: r, href: attrs["href"], stretch: strings.TrimSpace(attrs["preserveaspectratio"]) == "none",
					own: own, acc: current.acc, inDefs: current.inDefs, id: attrs["id"],
				})
			case name == "use":
				if !current.inDefs {
					uses = append(uses, svgUse{ref: attrs["href"], x: number(attrs["x"]), y: number(attrs["y"]), acc: current.acc})
				}
			case drawing[name] && !current.inDefs:
				return wrapper{}, errNotWrapper
			}
			stack = append(stack, current)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	if root {
		return wrapper{}, errUnsupported
	}

	// Rysowane są obrazy spoza <defs> i to, co wskazują <use>. Ma tego być dokładnie jedno.
	drawn := 0
	for _, img := range images {
		if !img.inDefs {
			drawn++
			doc.image, doc.transform = img, img.acc
		}
	}
	for _, use := range uses {
		id, ok := strings.CutPrefix(use.ref, "#")
		if !ok {
			return wrapper{}, errNotWrapper
		}
		var target *svgImage
		for i := range images {
			if images[i].id == id {
				target = &images[i]
			}
		}
		if target == nil {
			return wrapper{}, errNotWrapper
		}
		drawn++
		// <use> rysuje element tak, jakby był jego dzieckiem przesuniętym o x, y.
		doc.image = *target
		doc.transform = use.acc.times(affine{a: 1, d: 1, e: use.x, f: use.y}).times(target.own)
	}
	if drawn != 1 {
		return wrapper{}, errNotWrapper
	}
	return doc, nil
}

func viewBoxOf(attrs map[string]string) (rect, bool) {
	if vb := strings.Fields(strings.ReplaceAll(attrs["viewbox"], ",", " ")); len(vb) == 4 {
		var v [4]float64
		for i, s := range vb {
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return rect{}, false
			}
			v[i] = f
		}
		return rect{v[0], v[1], v[2], v[3]}, v[2] > 0 && v[3] > 0
	}
	w, wok := length(attrs["width"])
	h, hok := length(attrs["height"])
	return rect{0, 0, w, h}, wok && hok && w > 0 && h > 0
}

func rectOf(attrs map[string]string) (rect, bool) {
	w, wok := length(attrs["width"])
	h, hok := length(attrs["height"])
	return rect{number(attrs["x"]), number(attrs["y"]), w, h}, wok && hok && w > 0 && h > 0
}

// length czyta długość w pikselach ("2015", "2015px"); procentów nie przyjmuje.
func length(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "px"), 64)
	return f, err == nil
}

func number(s string) float64 {
	f, _ := length(s)
	return f
}

// affine to przekształcenie x' = a·x + c·y + e, y' = b·x + d·y + f.
type affine struct{ a, b, c, d, e, f float64 }

var identity = affine{a: 1, d: 1}

// times składa przekształcenia: najpierw n, potem m.
func (m affine) times(n affine) affine {
	return affine{
		a: m.a*n.a + m.c*n.b,
		b: m.b*n.a + m.d*n.b,
		c: m.a*n.c + m.c*n.d,
		d: m.b*n.c + m.d*n.d,
		e: m.a*n.e + m.c*n.f + m.e,
		f: m.b*n.e + m.d*n.f + m.f,
	}
}

// parseTransform czyta matrix, translate i scale. Obrót i pochylenie dają
// błąd, bo obrazu po nich nie da się wyciąć prostokątem.
func parseTransform(s string) (affine, error) {
	m := identity
	rest := strings.TrimSpace(s)
	for rest != "" {
		open := strings.IndexByte(rest, '(')
		end := strings.IndexByte(rest, ')')
		if open < 0 || end < open {
			return affine{}, errUnsupported
		}
		name := strings.TrimSpace(rest[:open])
		var args []float64
		for _, f := range strings.Fields(strings.ReplaceAll(rest[open+1:end], ",", " ")) {
			v, err := strconv.ParseFloat(f, 64)
			if err != nil {
				return affine{}, errUnsupported
			}
			args = append(args, v)
		}
		var t affine
		switch {
		case name == "matrix" && len(args) == 6:
			t = affine{args[0], args[1], args[2], args[3], args[4], args[5]}
		case name == "translate" && len(args) == 1:
			t = affine{a: 1, d: 1, e: args[0]}
		case name == "translate" && len(args) == 2:
			t = affine{a: 1, d: 1, e: args[0], f: args[1]}
		case name == "scale" && len(args) == 1:
			t = affine{a: args[0], d: args[0]}
		case name == "scale" && len(args) == 2:
			t = affine{a: args[0], d: args[1]}
		default:
			return affine{}, errUnsupported
		}
		m = m.times(t)
		rest = strings.TrimLeft(rest[end+1:], " \t\r\n,")
	}
	return m, nil
}
