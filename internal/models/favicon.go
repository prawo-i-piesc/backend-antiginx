package models

import "time"

// Favicon to ikona strony w postaci, w jakiej ją serwujemy, pod hashem SHA-256
// tej zawartości. Witryny z tą samą ikoną dzielą jeden wiersz.
type Favicon struct {
	// Hash to SHA-256 z Data zapisany szesnastkowo; jest też adresem ikony w API.
	Hash        string    `gorm:"type:char(64);primaryKey"`
	ContentType string    `gorm:"type:varchar(32);not null"`
	Data        []byte    `gorm:"not null"`
	CreatedAt   time.Time `gorm:"not null"`
}

// SiteIcon wiąże witrynę z jej ikoną. Witryna to host z portem, gdy ten nie
// jest domyślny, bez schematu: http:// i https:// tej samej strony mają jedną
// ikonę.
type SiteIcon struct {
	Host string `gorm:"type:varchar(300);primaryKey"`
	// FaviconHash jest pusty, dopóki ikony nie mamy albo gdy strona żadnej nie
	// ma. Indeks służy sprawdzaniu, czy ikonę ktoś jeszcze wskazuje.
	FaviconHash *string `gorm:"type:char(64);index"`
	// CheckedAt to początek ostatniej próby pobrania. Od niego liczy się, kiedy
	// wolno spróbować znowu.
	CheckedAt time.Time `gorm:"not null"`
}
