package favicons

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	_ "image/gif" // dekodery formatów, w których przychodzą ikony
	_ "image/jpeg"
	"image/png"
	"net/http"
	"strings"
	"unicode/utf8"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	// storedLimit to największa ikona, jaką zapisujemy. Po normalizacji zwykła
	// ikona ma 1–5 KiB, więc to tylko bariera na wypadek dziwnego pliku.
	storedLimit = 64 << 10
	// decodeLimit to największy bok obrazu, który w ogóle dekodujemy. Wymiary
	// czytamy z nagłówka przed dekodowaniem, więc mały plik udający obraz
	// 50000×50000 nie zajmie pamięci.
	decodeLimit = 1024
	// maxICOEntries ogranicza katalog pliku ICO; prawdziwe mają kilka wpisów.
	maxICOEntries = 64
)

var errUnsupported = errors.New("favicons: plik nie jest obsługiwaną ikoną")

// Icon to ikona gotowa do zapisu i serwowania.
type Icon struct {
	ContentType string
	Data        []byte
}

// normalize sprawdza, czym naprawdę jest plik (nagłówek Content-Type serwera
// się nie liczy), i sprowadza go do małej ikony:
//   - z ICO zostaje jeden wpis w rozmiarze najbliższym docelowemu, bo pełny
//     plik z rozmiarami do 256 px waży często kilkadziesiąt razy więcej,
//   - obraz rastrowy większy niż maxEdge jest zmniejszany i zapisywany jako PNG,
//   - SVG przechodzi bez zmian; jego skrypty nie wykonają się ani w <img>, ani
//     przy otwarciu adresu wprost, bo serwujemy go z CSP z sandbox.
func normalize(raw []byte) (Icon, error) {
	var icon Icon
	switch {
	case isICO(raw):
		entry, isPNG, err := icoEntry(raw)
		if err != nil {
			return Icon{}, err
		}
		if isPNG {
			icon, err = raster(entry, "image/png")
			if err != nil {
				return Icon{}, err
			}
		} else {
			icon = Icon{ContentType: "image/x-icon", Data: entry}
		}
	case isSVG(raw):
		icon = Icon{ContentType: "image/svg+xml", Data: raw}
	default:
		ct := http.DetectContentType(raw)
		switch ct {
		case "image/png", "image/gif", "image/jpeg", "image/webp":
		default:
			return Icon{}, errUnsupported
		}
		var err error
		if icon, err = raster(raw, ct); err != nil {
			return Icon{}, err
		}
	}

	if len(icon.Data) == 0 || len(icon.Data) > storedLimit {
		return Icon{}, errUnsupported
	}
	return icon, nil
}

// raster zostawia mały obraz bez zmian, a większy niż maxEdge zmniejsza do
// maxEdge na dłuższym boku i zapisuje jako PNG.
func raster(raw []byte, contentType string) (Icon, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > decodeLimit || cfg.Height > decodeLimit {
		return Icon{}, errUnsupported
	}
	if cfg.Width <= maxEdge && cfg.Height <= maxEdge {
		return Icon{ContentType: contentType, Data: raw}, nil
	}

	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return Icon{}, errUnsupported
	}
	w, h := cfg.Width, cfg.Height
	if w >= h {
		w, h = maxEdge, max(1, h*maxEdge/w)
	} else {
		w, h = max(1, w*maxEdge/h), maxEdge
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)

	var out bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&out, dst); err != nil {
		return Icon{}, err
	}
	return Icon{ContentType: "image/png", Data: out.Bytes()}, nil
}

func isICO(raw []byte) bool {
	return len(raw) >= 6 && raw[0] == 0 && raw[1] == 0 && raw[2] == 1 && raw[3] == 0
}

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// icoEntry wybiera z pliku ICO jeden obraz. Wpis z PNG zwraca jako sam PNG, a
// wpis z bitmapą jako nowy plik ICO z tym jednym wpisem.
func icoEntry(raw []byte) ([]byte, bool, error) {
	count := int(binary.LittleEndian.Uint16(raw[4:6]))
	if count == 0 || count > maxICOEntries || len(raw) < 6+16*count {
		return nil, false, errUnsupported
	}

	best := -1
	var bestEdge, bestDepth int
	for i := 0; i < count; i++ {
		e := raw[6+16*i : 6+16*(i+1)]
		size := int(binary.LittleEndian.Uint32(e[8:12]))
		offset := int(binary.LittleEndian.Uint32(e[12:16]))
		if size == 0 || offset < 6+16*count || offset > len(raw) || size > len(raw)-offset {
			continue
		}
		edge := int(e[0])
		if edge == 0 {
			edge = 256 // bajt 0 w katalogu znaczy 256 px
		}
		depth := int(binary.LittleEndian.Uint16(e[6:8]))
		if best < 0 || betterEdge(edge, bestEdge) || (edge == bestEdge && depth > bestDepth) {
			best, bestEdge, bestDepth = i, edge, depth
		}
	}
	if best < 0 {
		return nil, false, errUnsupported
	}

	e := raw[6+16*best : 6+16*(best+1)]
	size := binary.LittleEndian.Uint32(e[8:12])
	offset := binary.LittleEndian.Uint32(e[12:16])
	data := raw[offset : offset+size]
	if bytes.HasPrefix(data, pngSignature) {
		return data, true, nil
	}

	const headerSize = 6 + 16
	out := make([]byte, headerSize+len(data))
	copy(out, []byte{0, 0, 1, 0, 1, 0})
	copy(out[6:], e)
	binary.LittleEndian.PutUint32(out[6+12:], headerSize)
	copy(out[headerSize:], data)
	return out, false, nil
}

// betterEdge mówi, czy bok a lepiej pasuje do pokazania niż b: najlepszy jest
// najmniejszy nie mniejszy niż idealEdge, a z mniejszych największy.
func betterEdge(a, b int) bool {
	switch {
	case a >= idealEdge && b >= idealEdge:
		return a < b
	case a >= idealEdge:
		return true
	case b >= idealEdge:
		return false
	default:
		return a > b
	}
}

// isSVG sprawdza, czy przed korzeniem <svg> jest tylko prolog XML: deklaracja,
// komentarze, DOCTYPE i białe znaki.
func isSVG(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	rest := bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	for range 32 {
		rest = bytes.TrimLeft(rest, " \t\r\n")
		switch {
		case hasPrefixFold(rest, "<svg"):
			return len(rest) > 4 && strings.IndexByte(" \t\r\n>/", rest[4]) >= 0
		case bytes.HasPrefix(rest, []byte("<?")):
			rest = skipPast(rest, "?>")
		case bytes.HasPrefix(rest, []byte("<!--")):
			rest = skipPast(rest, "-->")
		case hasPrefixFold(rest, "<!doctype"):
			rest = skipPast(rest, ">")
		default:
			return false
		}
		if rest == nil {
			return false
		}
	}
	return false
}

func skipPast(b []byte, end string) []byte {
	i := bytes.Index(b, []byte(end))
	if i < 0 {
		return nil
	}
	return b[i+len(end):]
}

func hasPrefixFold(b []byte, prefix string) bool {
	return len(b) >= len(prefix) && bytes.EqualFold(b[:len(prefix)], []byte(prefix))
}
