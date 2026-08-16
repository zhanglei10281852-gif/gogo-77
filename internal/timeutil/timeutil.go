// Package timeutil defines the single time representation used across
// FluWatershed.
//
// Every timestamp that enters the system is an RFC 3339 string that must carry
// an explicit UTC designator. Local offsets are rejected rather than converted,
// because a surveillance record whose offset was guessed is worse than a record
// that failed to load. Nothing in this package reads the system clock: the
// "now" that windows and holding times are measured against always arrives from
// the caller as data.
package timeutil

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Layout is the canonical wire format: RFC 3339 at second resolution in UTC.
const Layout = "2006-01-02T15:04:05Z"

// DayLayout is the calendar-day key used to bucket samples into intervals.
const DayLayout = "2006-01-02"

// Stamp is a validated UTC instant. The zero Stamp is "unset" and reports
// false from IsSet.
type Stamp struct {
	inner time.Time
}

// Parse converts an RFC 3339 UTC string into a Stamp.
func Parse(text string) (Stamp, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return Stamp{}, errors.New("timestamp is empty")
	}
	if !strings.HasSuffix(trimmed, "Z") {
		return Stamp{}, fmt.Errorf("timestamp %q must end in Z; only UTC is accepted", trimmed)
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return Stamp{}, fmt.Errorf("timestamp %q is not RFC 3339: %w", trimmed, err)
	}
	return Stamp{inner: parsed.UTC().Truncate(time.Second)}, nil
}

// MustParse is Parse for constants known to be well formed; it panics on bad
// input and is only used for package-level defaults and tests.
func MustParse(text string) Stamp {
	stamp, err := Parse(text)
	if err != nil {
		panic(err)
	}
	return stamp
}

// IsSet reports whether the Stamp holds a real instant.
func (s Stamp) IsSet() bool { return !s.inner.IsZero() }

// String renders the Stamp in Layout, or an empty string when unset.
func (s Stamp) String() string {
	if !s.IsSet() {
		return ""
	}
	return s.inner.Format(Layout)
}

// Time exposes the underlying UTC time.
func (s Stamp) Time() time.Time { return s.inner }

// Unix is the second count since the epoch, used for stable sort keys.
func (s Stamp) Unix() int64 { return s.inner.Unix() }

// Before reports whether s precedes other.
func (s Stamp) Before(other Stamp) bool { return s.inner.Before(other.inner) }

// After reports whether s follows other.
func (s Stamp) After(other Stamp) bool { return s.inner.After(other.inner) }

// Equal reports whether the two instants coincide.
func (s Stamp) Equal(other Stamp) bool { return s.inner.Equal(other.inner) }

// HoursSince returns the number of hours from earlier to s. The result is
// negative when s precedes earlier, which callers use to spot records whose
// analysis appears to happen before collection.
func (s Stamp) HoursSince(earlier Stamp) float64 {
	return s.inner.Sub(earlier.inner).Hours()
}

// AddHours returns the instant hours after s; a negative value moves backwards.
func (s Stamp) AddHours(hours float64) Stamp {
	shifted := s.inner.Add(time.Duration(hours * float64(time.Hour)))
	return Stamp{inner: shifted.UTC().Truncate(time.Second)}
}

// AddDays returns the instant days after s.
func (s Stamp) AddDays(days int) Stamp {
	return Stamp{inner: s.inner.AddDate(0, 0, days)}
}

// Day is the calendar-day key in DayLayout, the interval grain used by
// baselines and trend series.
func (s Stamp) Day() string {
	if !s.IsSet() {
		return ""
	}
	return s.inner.Format(DayLayout)
}

// DayIndex is the count of whole days from the epoch, giving trend regressions
// a small integral x axis that is stable across runs.
func (s Stamp) DayIndex() int {
	return int(s.inner.Unix() / 86400)
}

// StartOfDay truncates s to midnight UTC.
func (s Stamp) StartOfDay() Stamp {
	y, m, d := s.inner.Date()
	return Stamp{inner: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

// MarshalJSON writes the Stamp as its canonical string.
func (s Stamp) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

// UnmarshalJSON reads a canonical string, accepting "" as unset.
func (s *Stamp) UnmarshalJSON(raw []byte) error {
	text := strings.TrimSpace(string(raw))
	if text == "null" {
		*s = Stamp{}
		return nil
	}
	if len(text) < 2 || text[0] != '"' || text[len(text)-1] != '"' {
		return fmt.Errorf("timestamp must be a JSON string, got %s", text)
	}
	body := text[1 : len(text)-1]
	if body == "" {
		*s = Stamp{}
		return nil
	}
	parsed, err := Parse(body)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

// ParseDay reads a bare calendar day in DayLayout as midnight UTC.
func ParseDay(text string) (Stamp, error) {
	parsed, err := time.Parse(DayLayout, strings.TrimSpace(text))
	if err != nil {
		return Stamp{}, fmt.Errorf("day %q is not %s: %w", text, DayLayout, err)
	}
	return Stamp{inner: parsed.UTC()}, nil
}

// Window is a half-open interval [From, To) over instants.
type Window struct {
	From Stamp `json:"from"`
	To   Stamp `json:"to"`
}

// NewWindow builds the window that ends at end and reaches back days days.
func NewWindow(end Stamp, days int) Window {
	if days < 0 {
		days = 0
	}
	return Window{From: end.AddDays(-days), To: end}
}

// Contains reports whether stamp falls inside the window. An unset bound is
// treated as unbounded on that side.
func (w Window) Contains(stamp Stamp) bool {
	if !stamp.IsSet() {
		return false
	}
	if w.From.IsSet() && stamp.Before(w.From) {
		return false
	}
	if w.To.IsSet() && !stamp.Before(w.To) {
		return false
	}
	return true
}

// Days is the length of the window in whole days, rounded down.
func (w Window) Days() int {
	if !w.From.IsSet() || !w.To.IsSet() {
		return 0
	}
	return int(w.To.HoursSince(w.From) / 24)
}

// SortStamps orders a slice of stamps ascending in place.
func SortStamps(stamps []Stamp) {
	sort.Slice(stamps, func(i, j int) bool { return stamps[i].Before(stamps[j]) })
}

// Latest returns the newest stamp in the slice and whether one was found.
func Latest(stamps []Stamp) (Stamp, bool) {
	var best Stamp
	found := false
	for _, s := range stamps {
		if !s.IsSet() {
			continue
		}
		if !found || s.After(best) {
			best = s
			found = true
		}
	}
	return best, found
}

// Earliest returns the oldest stamp in the slice and whether one was found.
func Earliest(stamps []Stamp) (Stamp, bool) {
	var best Stamp
	found := false
	for _, s := range stamps {
		if !s.IsSet() {
			continue
		}
		if !found || s.Before(best) {
			best = s
			found = true
		}
	}
	return best, found
}
