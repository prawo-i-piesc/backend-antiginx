package report

import (
	"embed"
	"html/template"
	"io"
)

//go:embed templates/report.html.tmpl
var templateFS embed.FS

var htmlTemplate = template.Must(template.New("report.html.tmpl").Funcs(template.FuncMap{
	"formatTime":     formatTime,
	"formatTimePtr":  formatTimePtr,
	"formatDuration": formatDuration,
}).ParseFS(templateFS, "templates/report.html.tmpl"))

// htmlView to dane szablonu: dokument i etykiety w wybranym języku.
type htmlView struct {
	Document
	L *Labels
}

// RenderHTML zapisuje raport jako samodzielną stronę: style są w pliku, nie
// ma skryptów ani zasobów z zewnątrz, więc działa offline i po wydruku.
// Wszystko, co przyszło ze skanowanej strony, escapuje html/template.
func RenderHTML(w io.Writer, doc Document, l *Labels) error {
	return htmlTemplate.Execute(w, htmlView{Document: doc, L: l})
}
