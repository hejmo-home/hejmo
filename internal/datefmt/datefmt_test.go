package datefmt_test

import (
	"testing"
	"time"

	"github.com/hejmo-home/hejmo/internal/datefmt"
	"github.com/hejmo-home/hejmo/internal/store"
)

var (
	christmas = time.Date(2026, 12, 24, 18, 5, 9, 0, time.UTC)
	newYear   = time.Date(2027, 1, 3, 0, 0, 0, 0, time.UTC)
	afternoon = time.Date(2026, 11, 14, 13, 0, 0, 0, time.UTC)
)

func TestDateAndShort(t *testing.T) {
	cases := []struct {
		name        string
		f           store.DateFormat
		date, short string
	}{
		{"day first", store.DateDMY, "24.12.2026", "24.12."},
		{"month first", store.DateMDY, "12/24/2026", "12/24"},
		{"unset means day first", "", "24.12.2026", "24.12."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := datefmt.Date(c.f, christmas); got != c.date {
				t.Errorf("Date = %q, want %q", got, c.date)
			}
			if got := datefmt.Short(c.f, christmas); got != c.short {
				t.Errorf("Short = %q, want %q", got, c.short)
			}
		})
	}
	if got := datefmt.Date(store.DateDMY, time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)); got != "05.03.2026" {
		t.Errorf("two digits: %q", got)
	}
}

func TestRange(t *testing.T) {
	mon, sun := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		f    store.DateFormat
		a, b time.Time
		want string
	}{
		{store.DateDMY, mon, sun, "09.11. – 15.11.2026"},
		{store.DateMDY, mon, sun, "11/09 – 11/15/2026"},
		{store.DateDMY, time.Date(2026, 12, 28, 0, 0, 0, 0, time.UTC), newYear, "28.12.2026 – 03.01.2027"},
		{store.DateMDY, time.Date(2026, 12, 28, 0, 0, 0, 0, time.UTC), newYear, "12/28/2026 – 01/03/2027"},
	}
	for _, c := range cases {
		if got := datefmt.Range(c.f, c.a, c.b); got != c.want {
			t.Errorf("Range(%s) = %q, want %q", c.f, got, c.want)
		}
	}
}

func TestRangeParts(t *testing.T) {
	from, to := datefmt.RangeParts(store.DateDMY, time.Date(2026, 12, 28, 0, 0, 0, 0, time.UTC), newYear)
	if from != "28.12.2026" || to != "03.01.2027" {
		t.Errorf("year change: %q, %q", from, to)
	}
	from, to = datefmt.RangeParts(store.DateMDY, time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC))
	if from != "11/09" || to != "11/15/2026" {
		t.Errorf("same year: %q, %q", from, to)
	}
}

func TestDateTime(t *testing.T) {
	const nb = " "
	cases := []struct {
		name        string
		d           store.DateFormat
		tf          store.TimeFormat
		short, secs string
		tm          time.Time
	}{
		{"dmy 24h", store.DateDMY, store.Time24h, "14.11.2026" + nb + "13:00", "14.11.2026" + nb + "13:00:00", afternoon},
		{"dmy 12h", store.DateDMY, store.Time12h, "14.11.2026" + nb + "1:00" + nb + "PM", "14.11.2026" + nb + "1:00:00" + nb + "PM", afternoon},
		{"mdy 24h", store.DateMDY, store.Time24h, "11/14/2026" + nb + "13:00", "11/14/2026" + nb + "13:00:00", afternoon},
		{"mdy 12h", store.DateMDY, store.Time12h, "11/14/2026" + nb + "1:00" + nb + "PM", "11/14/2026" + nb + "1:00:00" + nb + "PM", afternoon},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := datefmt.DateTime(c.d, c.tf, c.tm); got != c.short {
				t.Errorf("DateTime = %q, want %q", got, c.short)
			}
			if got := datefmt.DateTimeSeconds(c.d, c.tf, c.tm); got != c.secs {
				t.Errorf("DateTimeSeconds = %q, want %q", got, c.secs)
			}
		})
	}
}

func TestClockWithSeconds(t *testing.T) {
	if got := datefmt.ClockSeconds(store.Time12h, afternoon); got != "1:00:00 PM" {
		t.Errorf("12h: %q", got)
	}
	if got := datefmt.ClockSeconds(store.Time24h, afternoon); got != "13:00:00" {
		t.Errorf("24h: %q", got)
	}
}
