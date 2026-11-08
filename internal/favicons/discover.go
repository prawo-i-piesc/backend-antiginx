package favicons

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Ikona jest pokazywana w 16 pikselach CSS, czyli w 32 fizycznych na ekranie o
// podwójnej gęstości. Do tego rozmiaru dobieramy kandydatów.
const (
	idealEdge = 32
	maxEdge   = 64
)

// Kolejność kandydatów: najpierw ikona zadeklarowana w rozmiarze, który się
// pokaże, potem wektorowa, potem bez podanego rozmiaru (zwykle favicon.ico
// albo PNG 32 px), potem za duża, za mała, ikona dla iOS i na końcu
// /favicon.ico, o które przeglądarki pytają same.
const (
	rankFits = iota
	rankVector
	rankUnsized
	rankLarger
	rankSmaller
	rankApple
	rankDefault
)

type candidate struct {
	// URL jest pusty dla ikony wpisanej w stronę jako data:, wtedy treść jest w data.
	URL  string
	data []byte
	rank int
	// edge to zadeklarowany bok w pikselach, 0 gdy nieznany.
	edge int
}

// discover zbiera z <head> strony ikony, które da się pokazać, od najlepszej.
// page może być ucięta: liczy się tylko nagłówek dokumentu. Na końcu zawsze
// jest /favicon.ico, chyba że strona sama go wskazała.
func discover(page []byte, base *url.URL) []candidate {
	var found []candidate
	z := html.NewTokenizer(bytes.NewReader(page))
	baseSet := false

scan:
	for {
		switch z.Next() {
		case html.ErrorToken:
			break scan
		case html.EndTagToken:
			if name, _ := z.TagName(); atom.Lookup(name) == atom.Head {
				break scan
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			switch atom.Lookup(name) {
			case atom.Body:
				break scan
			case atom.Base:
				// Liczy się tylko pierwszy <base>, tak jak w przeglądarce.
				if href := attrs(z, hasAttr)["href"]; href != "" && !baseSet {
					if ref, err := base.Parse(href); err == nil {
						base = ref
					}
					baseSet = true
				}
			case atom.Link:
				if c, ok := linkCandidate(attrs(z, hasAttr), base); ok {
					found = append(found, c)
				}
			}
		}
	}

	if fallback, err := base.Parse("/favicon.ico"); err == nil {
		known := slices.ContainsFunc(found, func(c candidate) bool { return c.URL == fallback.String() })
		if !known {
			found = append(found, candidate{URL: fallback.String(), rank: rankDefault})
		}
	}

	slices.SortStableFunc(found, func(a, b candidate) int {
		if a.rank != b.rank {
			return cmp.Compare(a.rank, b.rank)
		}
		// Wśród pasujących i za dużych bliżej rozmiaru docelowego jest mniejsza,
		// wśród za małych większa.
		if a.rank == rankSmaller {
			return cmp.Compare(b.edge, a.edge)
		}
		return cmp.Compare(a.edge, b.edge)
	})
	return found
}

func attrs(z *html.Tokenizer, more bool) map[string]string {
	out := make(map[string]string)
	for more {
		var key, val []byte
		key, val, more = z.TagAttr()
		k := string(key)
		if _, seen := out[k]; !seen {
			out[k] = string(val)
		}
	}
	return out
}

func linkCandidate(a map[string]string, base *url.URL) (candidate, bool) {
	var icon, apple bool
	for _, rel := range strings.Fields(strings.ToLower(a["rel"])) {
		switch rel {
		case "icon":
			icon = true
		case "apple-touch-icon", "apple-touch-icon-precomposed":
			apple = true
		}
	}
	href := strings.TrimSpace(a["href"])
	if (!icon && !apple) || href == "" {
		return candidate{}, false
	}

	c := candidate{}
	if strings.HasPrefix(strings.ToLower(href), "data:") {
		data, ok := decodeDataURL(href)
		if !ok {
			return candidate{}, false
		}
		c.data = data
	} else {
		ref, err := base.Parse(href)
		if err != nil || (ref.Scheme != "http" && ref.Scheme != "https") {
			return candidate{}, false
		}
		ref.Fragment = ""
		c.URL = ref.String()
	}

	if !icon {
		c.rank = rankApple
		return c, true
	}

	sizes := strings.ToLower(a["sizes"])
	typ := strings.ToLower(strings.TrimSpace(a["type"]))
	vector := typ == "image/svg+xml" || strings.Contains(sizes, "any") ||
		strings.HasSuffix(strings.ToLower(strings.SplitN(href, "?", 2)[0]), ".svg")

	c.edge = pickEdge(sizes)
	switch {
	case vector:
		c.rank = rankVector
	case c.edge == 0:
		c.rank = rankUnsized
	case c.edge < idealEdge:
		c.rank = rankSmaller
	case c.edge <= maxEdge:
		c.rank = rankFits
	default:
		c.rank = rankLarger
	}
	return c, true
}

// pickEdge wybiera z atrybutu sizes ("16x16 32x32 48x48") bok najbliższy
// docelowemu: najmniejszy nie mniejszy niż idealEdge, a gdy takiego nie ma,
// największy.
func pickEdge(sizes string) int {
	best := 0
	for _, size := range strings.Fields(sizes) {
		w, _, ok := strings.Cut(size, "x")
		if !ok {
			continue
		}
		edge, err := strconv.Atoi(w)
		if err != nil || edge <= 0 {
			continue
		}
		switch {
		case best == 0:
			best = edge
		case edge >= idealEdge && (best < idealEdge || edge < best):
			best = edge
		case edge < idealEdge && best < idealEdge && edge > best:
			best = edge
		}
	}
	return best
}

// decodeDataURL czyta ikonę wpisaną w stronę (data:image/png;base64,...).
func decodeDataURL(href string) ([]byte, bool) {
	meta, payload, ok := strings.Cut(href[len("data:"):], ",")
	if !ok || len(payload) > downloadLimit*4/3+4 {
		return nil, false
	}
	if strings.HasSuffix(strings.ToLower(meta), ";base64") {
		payload = strings.Map(func(r rune) rune {
			if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
				return -1
			}
			return r
		}, payload)
		if data, err := base64.StdEncoding.DecodeString(payload); err == nil {
			return data, true
		}
		data, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(payload, "="))
		return data, err == nil
	}
	data, err := url.PathUnescape(payload)
	return []byte(data), err == nil
}
