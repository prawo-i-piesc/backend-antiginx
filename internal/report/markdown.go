package report

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// RenderMarkdown zapisuje raport w CommonMark z tabelami GFM.
//
// Komunikaty i metadane pochodzą ze skanowanej strony, więc każdy tekst
// przechodzi przez escapeMarkdown: bez tego wynik mógłby wstrzyknąć do
// raportu link, obrazek ładowany przy podglądzie albo surowy HTML.
func RenderMarkdown(w io.Writer, doc Document, l *Labels) error {
	// bufio.Writer zapamiętuje pierwszy błąd zapisu i zwraca go z Flush.
	b := bufio.NewWriter(w)
	p := func(format string, args ...any) { _, _ = fmt.Fprintf(b, format, args...) }

	p("# %s\n\n", escapeMarkdown(l.Title))
	p("**%s:** %s\n\n", l.Target, escapeMarkdown(doc.Scan.TargetURL))

	p("## %s\n\n", l.SectionScan)
	p("| %s | %s |\n| --- | --- |\n", l.Field, l.Value)
	for _, row := range [][2]string{
		{l.Target, doc.Scan.TargetURL},
		{l.ScanID, doc.Scan.ID.String()},
		{l.Type, l.Kind(doc.Scan.Kind)},
		{l.Status, l.StatusName(doc.Scan.Status)},
		{l.Submitted, formatTime(doc.Scan.CreatedAt)},
		{l.Started, formatTimePtr(doc.Scan.StartedAt)},
		{l.Completed, formatTimePtr(doc.Scan.CompletedAt)},
		{l.Duration, formatDuration(doc.Scan.Duration())},
	} {
		p("| %s | %s |\n", row[0], escapeCell(row[1]))
	}

	s := doc.Summary
	p("\n## %s\n\n", l.SectionSummary)
	p("**%s:** %s\n\n", l.OverallAssessment, l.Risk(s))
	p("- %s: %d\n- %s: %d\n- %s: %d\n\n", l.Checks, s.Total, l.Passed, s.Passed, l.Failed, s.Failed)
	p("| %s | %s |\n| --- | --- |\n", l.SeverityLabel, l.FailedChecks)
	for _, c := range s.BySeverity {
		p("| %s | %d |\n", l.Severity(c.Severity), c.Count)
	}

	p("\n## %s\n\n", l.SectionFindings)
	if l.EngineNote != "" {
		p("_%s_\n\n", l.EngineNote)
	}
	if len(doc.Findings) == 0 {
		p("%s\n\n", l.NoFindings)
	}
	for _, f := range doc.Findings {
		p("### %s\n\n", escapeMarkdown(f.TestName))
		p("- %s: **%s**\n- %s: **%s**\n- %s: `%s`\n\n", l.Result, l.ResultOf(f), l.SeverityLabel, l.Severity(f.Severity), l.Rule, f.RuleID)
		if f.Message != "" {
			p("%s\n\n", escapeParagraph(f.Message))
		}
		if f.Details != "" {
			fence := codeFence(f.Details)
			p("<details>\n<summary>%s</summary>\n\n%sjson\n%s\n%s\n\n</details>\n\n", l.TechnicalDetails, fence, f.Details, fence)
			if f.DetailsTruncated {
				p("_%s_\n\n", l.DetailsTruncated)
			}
		}
	}

	p("---\n\n_%s %s_\n", l.GeneratedOn(doc), l.Disclaimer)
	return b.Flush()
}

// inlineSpecial to znaki, które w dowolnym miejscu linii mogą otworzyć link,
// obrazek, HTML, encję, kod, wyróżnienie albo komórkę tabeli.
const inlineSpecial = "\\`*_[]<>!|~&#"

// escapeMarkdown zamienia tekst jednej linii w zwykły tekst CommonMark/GFM.
// Poprzedza ukośnikiem znaki z inlineSpecial oraz znak, który na początku
// linii otworzyłby listę albo nagłówek. Pozostałej interpunkcji nie rusza,
// żeby surowy plik dało się czytać.
func escapeMarkdown(s string) string {
	var b strings.Builder
	b.Grow(len(s) + len(s)/8)

	// Lista numerowana: cyfry, a po nich '.' albo ')'.
	digits := 0
	for digits < len(s) && s[digits] >= '0' && s[digits] <= '9' {
		digits++
	}
	listMarker := -1
	if digits > 0 && digits < len(s) && (s[digits] == '.' || s[digits] == ')') {
		listMarker = digits
	}

	for i, r := range s {
		switch {
		case r < 0x80 && strings.IndexByte(inlineSpecial, byte(r)) >= 0,
			i == 0 && (r == '-' || r == '+' || r == '='),
			i == listMarker:
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// escapeCell mieści tekst w jednej komórce tabeli GFM.
func escapeCell(s string) string {
	return escapeMarkdown(strings.Join(strings.Fields(s), " "))
}

// escapeParagraph zachowuje podział na linie jako twarde złamania, a wcięcia
// usuwa, żeby tekst nie zamienił się w blok kodu.
func escapeParagraph(s string) string {
	var lines []string
	for line := range strings.Lines(strings.ReplaceAll(s, "\r\n", "\n")) {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, escapeMarkdown(line))
		}
	}
	return strings.Join(lines, "\\\n")
}

// codeFence zwraca płot dłuższy od najdłuższej serii backticków w treści,
// żeby treść nie mogła zamknąć bloku kodu przed czasem.
func codeFence(content string) string {
	longest, run := 0, 0
	for _, r := range content {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}
