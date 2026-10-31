package report

import (
	"fmt"
	"io"
	"strings"
	"time"
)

type Format string

const (
	FormatPDF      Format = "pdf"
	FormatHTML     Format = "html"
	FormatMarkdown Format = "md"
	FormatSARIF    Format = "sarif"
)

// Formats to formaty w kolejności, w jakiej podaje je dokumentacja API.
var Formats = []Format{FormatPDF, FormatHTML, FormatMarkdown, FormatSARIF}

// ParseFormat przyjmuje nazwę formatu z zapytania; pusta oznacza PDF.
func ParseFormat(raw string) (Format, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "pdf":
		return FormatPDF, true
	case "html":
		return FormatHTML, true
	case "md", "markdown":
		return FormatMarkdown, true
	case "sarif":
		return FormatSARIF, true
	}
	return "", false
}

func (f Format) ContentType() string {
	switch f {
	case FormatPDF:
		return "application/pdf"
	case FormatHTML:
		return "text/html; charset=utf-8"
	case FormatMarkdown:
		return "text/markdown; charset=utf-8"
	case FormatSARIF:
		return "application/sarif+json"
	}
	return "application/octet-stream"
}

func (f Format) Extension() string {
	if f == FormatSARIF {
		return "sarif"
	}
	return string(f)
}

// Filename to nazwa pliku do Content-Disposition, wyłącznie ASCII.
func Filename(doc Document, f Format) string {
	return fmt.Sprintf("antiginx-report-%s.%s", doc.Scan.ID, f.Extension())
}

// Render zapisuje doc w formacie f z etykietami w języku lang. SARIF czyta
// maszyna, więc jest zawsze taki sam, niezależnie od języka.
func Render(w io.Writer, doc Document, f Format, lang Lang) error {
	l := LabelsFor(lang)
	switch f {
	case FormatPDF:
		return RenderPDF(w, doc, l)
	case FormatHTML:
		return RenderHTML(w, doc, l)
	case FormatMarkdown:
		return RenderMarkdown(w, doc, l)
	case FormatSARIF:
		return RenderSARIF(w, doc)
	}
	return fmt.Errorf("report: nieznany format %q", f)
}

// Formaty czasu są wspólne dla obu języków: UTC w zapisie ISO czyta się tak
// samo po polsku i po angielsku.

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

func formatTimePtr(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return formatTime(*t)
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	return d.Round(time.Second).String()
}
