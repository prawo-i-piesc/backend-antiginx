package report

import (
	"io"
	"strconv"

	"codeberg.org/go-pdf/fpdf"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/goregular"
)

// Fonty Go są wkompilowane w binarkę: obejmują pełny zakres znaków, których
// potrzebujemy (w tym polskie), a standardowe fonty PDF znają tylko cp1252.
const (
	fontSans = "gosans"
	fontMono = "gomono"
)

const (
	pageMargin = 15.0
	lineHeight = 5.0
)

type rgb struct{ r, g, b int }

var (
	colorText  = rgb{29, 36, 51}
	colorMuted = rgb{91, 100, 117}
	colorLine  = rgb{221, 226, 234}
	colorPanel = rgb{246, 248, 251}
	colorPass  = rgb{46, 125, 79}
	colorFail  = rgb{192, 57, 43}
)

var severityColors = map[Severity]rgb{
	SeverityCritical: {139, 26, 26},
	SeverityHigh:     {192, 57, 43},
	SeverityMedium:   {217, 130, 43},
	SeverityLow:      {181, 154, 0},
	SeverityInfo:     {47, 111, 179},
	SeverityNone:     {46, 125, 79},
}

// RenderPDF zapisuje raport jako PDF w formacie A4.
func RenderPDF(w io.Writer, doc Document, l *Labels) error {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(pageMargin, pageMargin, pageMargin)
	pdf.SetAutoPageBreak(true, pageMargin+5)
	pdf.SetTitle(l.Title+" – "+doc.Scan.TargetURL, true)
	pdf.SetAuthor(toolName, true)
	pdf.SetCreator(toolName, true)
	pdf.SetCreationDate(doc.GeneratedAt)
	pdf.SetModificationDate(doc.GeneratedAt)

	pdf.AddUTF8FontFromBytes(fontSans, "", goregular.TTF)
	pdf.AddUTF8FontFromBytes(fontSans, "B", gobold.TTF)
	pdf.AddUTF8FontFromBytes(fontMono, "", gomono.TTF)

	pdf.AliasNbPages("{nb}")
	pdf.SetFooterFunc(func() {
		pdf.SetY(-pageMargin)
		setFont(pdf, "", 8, colorMuted)
		pdf.CellFormat(0, 4, l.GeneratedOn(doc), "", 0, "L", false, 0, "")
		pdf.CellFormat(0, 4, l.Page(pdf.PageNo(), "{nb}"), "", 0, "R", false, 0, "")
	})

	pdf.AddPage()
	r := pdfRenderer{pdf: pdf, width: contentWidth(pdf), l: l}
	r.header(doc)
	r.scanTable(doc.Scan)
	r.summary(doc.Summary)
	r.findings(doc.Findings)
	r.disclaimer()

	return pdf.Output(w)
}

type pdfRenderer struct {
	pdf   *fpdf.Fpdf
	width float64
	l     *Labels
}

func contentWidth(pdf *fpdf.Fpdf) float64 {
	pageW, _ := pdf.GetPageSize()
	left, _, right, _ := pdf.GetMargins()
	return pageW - left - right
}

func setFont(pdf *fpdf.Fpdf, style string, size float64, c rgb) {
	pdf.SetFont(fontSans, style, size)
	pdf.SetTextColor(c.r, c.g, c.b)
}

func (r pdfRenderer) header(doc Document) {
	setFont(r.pdf, "B", 20, colorText)
	r.pdf.MultiCell(r.width, 9, r.l.Title, "", "L", false)
	setFont(r.pdf, "", 11, colorMuted)
	r.pdf.MultiCell(r.width, 6, doc.Scan.TargetURL, "", "L", false)
	r.pdf.Ln(2)
}

func (r pdfRenderer) section(title string) {
	r.pdf.Ln(4)
	setFont(r.pdf, "B", 14, colorText)
	r.pdf.CellFormat(r.width, 8, title, "", 1, "L", false, 0, "")
	x, y := r.pdf.GetXY()
	r.pdf.SetDrawColor(colorLine.r, colorLine.g, colorLine.b)
	r.pdf.Line(x, y, x+r.width, y)
	r.pdf.Ln(3)
}

func (r pdfRenderer) scanTable(s ScanInfo) {
	l := r.l
	r.section(l.SectionScan)
	labelW := 40.0
	for _, row := range [][2]string{
		{l.Target, s.TargetURL},
		{l.ScanID, s.ID.String()},
		{l.Type, l.Kind(s.Kind)},
		{l.Status, l.StatusName(s.Status)},
		{l.Submitted, formatTime(s.CreatedAt)},
		{l.Started, formatTimePtr(s.StartedAt)},
		{l.Completed, formatTimePtr(s.CompletedAt)},
		{l.Duration, formatDuration(s.Duration())},
	} {
		setFont(r.pdf, "B", 10, colorMuted)
		r.pdf.CellFormat(labelW, lineHeight+1, row[0], "", 0, "L", false, 0, "")
		setFont(r.pdf, "", 10, colorText)
		r.pdf.MultiCell(r.width-labelW, lineHeight+1, row[1], "", "L", false)
	}
}

func (r pdfRenderer) summary(s Summary) {
	l := r.l
	r.section(l.SectionSummary)

	cards := []struct {
		value, label string
		color        rgb
	}{
		{l.Risk(s), l.OverallAssessment, colorText},
		{strconv.Itoa(s.Total), l.Checks, colorText},
		{strconv.Itoa(s.Passed), l.Passed, colorPass},
		{strconv.Itoa(s.Failed), l.Failed, colorFail},
	}
	gap := 3.0
	cardW := (r.width - gap*float64(len(cards)-1)) / float64(len(cards))
	x, y := r.pdf.GetXY()
	r.pdf.SetFillColor(colorPanel.r, colorPanel.g, colorPanel.b)
	r.pdf.SetDrawColor(colorLine.r, colorLine.g, colorLine.b)
	for i, c := range cards {
		cx := x + float64(i)*(cardW+gap)
		r.pdf.RoundedRect(cx, y, cardW, 18, 2, "1234", "FD")
		r.pdf.SetXY(cx+3, y+2.5)
		// Ocena bywa dłuższa od liczb, zwłaszcza po polsku; maleje, aż zmieści się w kafelku.
		size := 13.0
		for setFont(r.pdf, "B", size, c.color); size > 8 && r.pdf.GetStringWidth(c.value) > cardW-6; size -= 0.5 {
			setFont(r.pdf, "B", size-0.5, c.color)
		}
		r.pdf.CellFormat(cardW-6, 7, c.value, "", 0, "L", false, 0, "")
		r.pdf.SetXY(cx+3, y+10)
		setFont(r.pdf, "", 8.5, colorMuted)
		r.pdf.CellFormat(cardW-6, 5, c.label, "", 0, "L", false, 0, "")
	}
	r.pdf.SetXY(x, y+22)

	setFont(r.pdf, "B", 10, colorMuted)
	r.pdf.CellFormat(40, lineHeight+1, l.SeverityLabel, "B", 0, "L", false, 0, "")
	r.pdf.CellFormat(40, lineHeight+1, l.FailedChecks, "B", 1, "L", false, 0, "")
	for _, c := range s.BySeverity {
		r.badge(l.Severity(c.Severity), severityColors[c.Severity])
		r.pdf.SetX(x + 40)
		setFont(r.pdf, "", 10, colorText)
		r.pdf.CellFormat(40, lineHeight+1, strconv.Itoa(c.Count), "", 1, "L", false, 0, "")
	}
}

// badge rysuje kolorową etykietę w bieżącym miejscu i przesuwa kursor za nią.
func (r pdfRenderer) badge(text string, c rgb) {
	setFont(r.pdf, "B", 8, rgb{255, 255, 255})
	w := r.pdf.GetStringWidth(text) + 4
	x, y := r.pdf.GetXY()
	r.pdf.SetFillColor(c.r, c.g, c.b)
	r.pdf.RoundedRect(x, y+0.75, w, lineHeight-0.5, 1.5, "1234", "F")
	r.pdf.SetXY(x, y+0.75)
	r.pdf.CellFormat(w, lineHeight-0.5, text, "", 0, "C", false, 0, "")
	r.pdf.SetXY(x+w+2, y)
}

func (r pdfRenderer) findings(findings []Finding) {
	l := r.l
	r.section(l.SectionFindings)
	if l.EngineNote != "" {
		setFont(r.pdf, "", 8.5, colorMuted)
		r.pdf.MultiCell(r.width, 4, l.EngineNote, "", "L", false)
		r.pdf.Ln(2)
	}
	if len(findings) == 0 {
		setFont(r.pdf, "", 10, colorMuted)
		r.pdf.MultiCell(r.width, lineHeight, l.NoFindings, "", "L", false)
		return
	}

	_, pageH := r.pdf.GetPageSize()
	for _, f := range findings {
		// Nagłówek wyniku nie powinien zostać sam na dole strony.
		if r.pdf.GetY() > pageH-pageMargin-35 {
			r.pdf.AddPage()
		}
		left := r.pdf.GetX()

		r.badge(l.Severity(f.Severity), severityColors[f.Severity])
		status, color := l.ResultOf(f), colorPass
		if !f.Passed {
			color = colorFail
		}
		setFont(r.pdf, "B", 9, color)
		statusW := r.pdf.GetStringWidth(status) + 2
		nameW := left + r.width - r.pdf.GetX() - statusW
		setFont(r.pdf, "B", 11, colorText)
		y := r.pdf.GetY()
		r.pdf.MultiCell(nameW, lineHeight+0.5, f.TestName, "", "L", false)
		after := r.pdf.GetY()
		r.pdf.SetXY(left+r.width-statusW, y)
		setFont(r.pdf, "B", 9, color)
		r.pdf.CellFormat(statusW, lineHeight+0.5, status, "", 0, "R", false, 0, "")
		r.pdf.SetXY(left, after)

		r.pdf.SetFont(fontMono, "", 7.5)
		r.pdf.SetTextColor(colorMuted.r, colorMuted.g, colorMuted.b)
		r.pdf.CellFormat(r.width, 4, f.RuleID, "", 1, "L", false, 0, "")

		if f.Message != "" {
			r.pdf.Ln(1)
			setFont(r.pdf, "", 10, colorText)
			r.pdf.MultiCell(r.width, lineHeight, f.Message, "", "L", false)
		}
		if f.Details != "" {
			r.pdf.Ln(1.5)
			r.pdf.SetFont(fontMono, "", 7.5)
			r.pdf.SetTextColor(colorText.r, colorText.g, colorText.b)
			r.pdf.SetFillColor(colorPanel.r, colorPanel.g, colorPanel.b)
			r.pdf.MultiCell(r.width, 3.6, f.Details, "", "L", true)
			if f.DetailsTruncated {
				setFont(r.pdf, "", 8, colorMuted)
				r.pdf.MultiCell(r.width, 4, l.DetailsTruncated, "", "L", false)
			}
		}

		r.pdf.Ln(2.5)
		x, y := r.pdf.GetXY()
		r.pdf.SetDrawColor(colorLine.r, colorLine.g, colorLine.b)
		r.pdf.Line(x, y, x+r.width, y)
		r.pdf.Ln(3)
	}
}

func (r pdfRenderer) disclaimer() {
	r.pdf.Ln(2)
	setFont(r.pdf, "", 8, colorMuted)
	r.pdf.MultiCell(r.width, 4, r.l.Disclaimer, "", "L", false)
}
