package schedules

import (
	"errors"
	"testing"
	"time"
)

func intp(v int) *int { return &v }

func mustParse(t *testing.T, d Definition) Cadence {
	t.Helper()
	c, err := Parse(d)
	if err != nil {
		t.Fatalf("Parse(%+v): %v", d, err)
	}
	return c
}

func warsaw(t *testing.T, value string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Fatal(err)
	}
	at, err := time.ParseInLocation("2006-01-02 15:04", value, loc)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestNextDaily(t *testing.T) {
	c := mustParse(t, Definition{Frequency: "daily", Time: "08:30", Timezone: "Europe/Warsaw"})

	for _, tc := range []struct{ after, want string }{
		{"2026-11-16 07:00", "2026-11-16 08:30"},
		// Dokładnie w terminie: następny jest jutro, nie ten sam.
		{"2026-11-16 08:30", "2026-11-17 08:30"},
		{"2026-11-16 23:59", "2026-11-17 08:30"},
	} {
		got, err := c.Next(warsaw(t, tc.after))
		if err != nil {
			t.Fatal(err)
		}
		if want := warsaw(t, tc.want); !got.Equal(want) {
			t.Errorf("Next(%s) = %s, want %s", tc.after, got, want)
		}
	}
}

func TestNextWeeklyAndMonthly(t *testing.T) {
	// 2026-11-16 to poniedziałek.
	weekly := mustParse(t, Definition{Frequency: "weekly", Time: "06:00", Weekday: intp(int(time.Friday)), Timezone: "Europe/Warsaw"})
	got, _ := weekly.Next(warsaw(t, "2026-11-16 12:00"))
	if want := warsaw(t, "2026-11-20 06:00"); !got.Equal(want) {
		t.Errorf("weekly = %s, want %s", got, want)
	}

	monthly := mustParse(t, Definition{Frequency: "monthly", Time: "06:00", MonthDay: intp(15), Timezone: "Europe/Warsaw"})
	got, _ = monthly.Next(warsaw(t, "2026-11-16 12:00"))
	if want := warsaw(t, "2026-12-15 06:00"); !got.Equal(want) {
		t.Errorf("monthly = %s, want %s", got, want)
	}
}

// Termin to godzina w strefie użytkownika: po zmianie czasu skan dalej rusza
// o 08:00 na zegarku, czyli o innej godzinie UTC.
func TestNextKeepsWallClockAcrossDST(t *testing.T) {
	c := mustParse(t, Definition{Frequency: "daily", Time: "08:00", Timezone: "Europe/Warsaw"})

	before, _ := c.Next(warsaw(t, "2026-10-23 09:00"))
	after, _ := c.Next(warsaw(t, "2026-10-24 09:00"))
	if before.UTC().Hour() != 6 || after.UTC().Hour() != 7 {
		t.Errorf("UTC hours %d and %d, want 6 (CEST) and 7 (CET)", before.UTC().Hour(), after.UTC().Hour())
	}
}

// 02:30 nie istnieje w dniu zmiany na czas letni; termin i tak musi być.
func TestNextOnMissingHour(t *testing.T) {
	c := mustParse(t, Definition{Frequency: "daily", Time: "02:30", Timezone: "Europe/Warsaw"})
	got, err := c.Next(warsaw(t, "2026-03-28 12:00"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Day() != 29 {
		t.Errorf("Next = %s, want a term on 29 March", got)
	}
}

func TestBetween(t *testing.T) {
	c := mustParse(t, Definition{Frequency: "weekly", Time: "10:00", Weekday: intp(int(time.Monday)), Timezone: "UTC"})
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	terms := c.Between(from, to)
	if len(terms) != 5 {
		t.Fatalf("got %d Mondays in November 2026, want 5: %v", len(terms), terms)
	}
	// Termin równy from się liczy, równy to już nie.
	edge := c.Between(terms[0], terms[1])
	if len(edge) != 1 || !edge[0].Equal(terms[0]) {
		t.Errorf("Between(terms[0], terms[1]) = %v, want only terms[0]", edge)
	}
}

func TestParseRejects(t *testing.T) {
	for _, tc := range []struct {
		name  string
		def   Definition
		field string
	}{
		{"hourly", Definition{Frequency: "hourly", Time: "08:00", Timezone: "UTC"}, "frequency"},
		{"time", Definition{Frequency: "daily", Time: "8:00", Timezone: "UTC"}, "time"},
		{"time range", Definition{Frequency: "daily", Time: "24:00", Timezone: "UTC"}, "time"},
		{"weekday", Definition{Frequency: "weekly", Time: "08:00", Timezone: "UTC"}, "weekday"},
		{"weekday range", Definition{Frequency: "weekly", Time: "08:00", Weekday: intp(7), Timezone: "UTC"}, "weekday"},
		{"month day", Definition{Frequency: "monthly", Time: "08:00", MonthDay: intp(31), Timezone: "UTC"}, "month_day"},
		{"timezone", Definition{Frequency: "daily", Time: "08:00", Timezone: "Mars/Olympus"}, "timezone"},
		{"server zone", Definition{Frequency: "daily", Time: "08:00", Timezone: "Local"}, "timezone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.def)
			var field *FieldError
			if !errors.As(err, &field) || field.Field != tc.field {
				t.Errorf("Parse = %v, want an error on %s", err, tc.field)
			}
		})
	}
}
