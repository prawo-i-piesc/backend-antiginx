package handlers

import "strings"

// listPageDefault to rozmiar strony listy, gdy żądanie go nie podaje.
const listPageDefault = 25

// listPage to stronicowanie list w API: limit 1–100 i przesunięcie.
type listPage struct {
	Limit  int `form:"limit" json:"limit" binding:"omitempty,min=1,max=100"`
	Offset int `form:"offset" json:"offset" binding:"omitempty,min=0"`
}

func (p *listPage) normalize() {
	if p.Limit == 0 {
		p.Limit = listPageDefault
	}
}

// escapeLike chroni znaki specjalne LIKE, żeby fraza z wyszukiwarki pasowała
// dosłownie; zapytanie podaje ESCAPE '\'.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
