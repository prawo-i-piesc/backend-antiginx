// Package schedules liczy terminy harmonogramów i uruchamia skany, którym
// termin minął.
package schedules

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/prawo-i-piesc/backend/internal/models"
)

// MaxMonthDay to ostatni dzień miesiąca, jaki można wybrać: 28. jest w każdym
// miesiącu, więc skan nie przepada w lutym.
const MaxMonthDay = 28

// horizon ogranicza szukanie kolejnego terminu. Każdy cykl ma termin w ciągu
// miesiąca, więc pętla nigdy do niego nie dochodzi przy poprawnych danych.
const horizon = 400

// Cadence to sprawdzony cykl harmonogramu.
type Cadence struct {
	Frequency string
	Hour      int
	Minute    int
	Weekday   time.Weekday
	MonthDay  int
	Location  *time.Location
}

// Definition to pola harmonogramu, z których powstaje cykl. Każdy problem
// zwraca jako pole JSON i powód, w kształcie VALIDATION_FAILED.
type Definition struct {
	Frequency string
	Time      string
	Weekday   *int
	MonthDay  *int
	Timezone  string
}

// FieldError to błąd jednego pola definicji.
type FieldError struct {
	Field  string
	Reason string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Reason }

// Parse sprawdza definicję i zwraca cykl.
func Parse(d Definition) (Cadence, error) {
	c := Cadence{Frequency: d.Frequency}

	switch d.Frequency {
	case models.ScheduleDaily, models.ScheduleWeekly, models.ScheduleMonthly:
	default:
		return c, &FieldError{"frequency", "oneof"}
	}

	hour, minute, ok := parseClock(d.Time)
	if !ok {
		return c, &FieldError{"time", "time"}
	}
	c.Hour, c.Minute = hour, minute

	switch d.Frequency {
	case models.ScheduleWeekly:
		if d.Weekday == nil || *d.Weekday < 0 || *d.Weekday > 6 {
			return c, &FieldError{"weekday", "required"}
		}
		c.Weekday = time.Weekday(*d.Weekday)
	case models.ScheduleMonthly:
		if d.MonthDay == nil || *d.MonthDay < 1 || *d.MonthDay > MaxMonthDay {
			return c, &FieldError{"month_day", "required"}
		}
		c.MonthDay = *d.MonthDay
	}

	// "Local" i pusta nazwa to strefa serwera, a nie użytkownika.
	if d.Timezone == "" || d.Timezone == "Local" {
		return c, &FieldError{"timezone", "required"}
	}
	loc, err := time.LoadLocation(d.Timezone)
	if err != nil {
		return c, &FieldError{"timezone", "timezone"}
	}
	c.Location = loc
	return c, nil
}

// FromSchedule odtwarza cykl zapisanego harmonogramu.
func FromSchedule(s *models.Schedule) (Cadence, error) {
	return Parse(Definition{
		Frequency: s.Frequency,
		Time:      s.Time,
		Weekday:   s.Weekday,
		MonthDay:  s.MonthDay,
		Timezone:  s.Timezone,
	})
}

func parseClock(value string) (int, int, bool) {
	hh, mm, found := strings.Cut(value, ":")
	if !found || len(hh) != 2 || len(mm) != 2 {
		return 0, 0, false
	}
	hour, err := strconv.Atoi(hh)
	if err != nil || hour < 0 || hour > 23 {
		return 0, 0, false
	}
	minute, err := strconv.Atoi(mm)
	if err != nil || minute < 0 || minute > 59 {
		return 0, 0, false
	}
	return hour, minute, true
}

// matches mówi, czy cykl ma termin w danym dniu kalendarza.
func (c Cadence) matches(day time.Time) bool {
	switch c.Frequency {
	case models.ScheduleWeekly:
		return day.Weekday() == c.Weekday
	case models.ScheduleMonthly:
		return day.Day() == c.MonthDay
	default:
		return true
	}
}

// at to termin w danym dniu. time.Date przesuwa godzinę, której nie ma przy
// zmianie czasu na letni, więc termin zawsze istnieje.
func (c Cadence) at(day time.Time) time.Time {
	return time.Date(day.Year(), day.Month(), day.Day(), c.Hour, c.Minute, 0, 0, c.Location)
}

var errNoTerm = errors.New("harmonogram nie ma terminu")

// Next to pierwszy termin po chwili after.
func (c Cadence) Next(after time.Time) (time.Time, error) {
	local := after.In(c.Location)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, c.Location)
	for i := 0; i < horizon; i++ {
		if c.matches(day) {
			if term := c.at(day); term.After(after) {
				return term, nil
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return time.Time{}, fmt.Errorf("%w po %s", errNoTerm, after.Format(time.RFC3339))
}

// Between to terminy w przedziale [from, to).
func (c Cadence) Between(from, to time.Time) []time.Time {
	var terms []time.Time
	next, err := c.Next(from.Add(-time.Nanosecond))
	for err == nil && next.Before(to) {
		terms = append(terms, next)
		next, err = c.Next(next)
	}
	return terms
}
