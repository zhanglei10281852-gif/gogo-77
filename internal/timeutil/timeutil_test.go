package timeutil

import (
	"encoding/json"
	"testing"
)

func TestParseRequiresUTC(t *testing.T) {
	if _, err := Parse("2026-03-14T09:00:00+02:00"); err == nil {
		t.Fatal("an offset timestamp was accepted")
	}
	if _, err := Parse("2026-03-14 09:00:00Z"); err == nil {
		t.Fatal("a non RFC 3339 timestamp was accepted")
	}
	if _, err := Parse("   "); err == nil {
		t.Fatal("an empty timestamp was accepted")
	}
	stamp, err := Parse(" 2026-03-14T09:00:00Z ")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if stamp.String() != "2026-03-14T09:00:00Z" {
		t.Fatalf("round trip = %q", stamp.String())
	}
}

func TestZeroStampIsUnset(t *testing.T) {
	var stamp Stamp
	if stamp.IsSet() {
		t.Fatal("the zero stamp reports itself as set")
	}
	if stamp.String() != "" {
		t.Fatalf("zero stamp renders as %q", stamp.String())
	}
	if stamp.Day() != "" {
		t.Fatalf("zero stamp day = %q", stamp.Day())
	}
}

func TestJSONRoundTrip(t *testing.T) {
	type holder struct {
		At Stamp `json:"at"`
	}
	raw := []byte(`{"at":"2026-03-14T09:30:00Z"}`)
	var decoded holder
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.At.Day() != "2026-03-14" {
		t.Fatalf("day = %q", decoded.At.Day())
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(encoded) != `{"at":"2026-03-14T09:30:00Z"}` {
		t.Fatalf("encoded = %s", encoded)
	}
	var empty holder
	if err := json.Unmarshal([]byte(`{"at":""}`), &empty); err != nil {
		t.Fatalf("empty string: %v", err)
	}
	if empty.At.IsSet() {
		t.Fatal("an empty string decoded to a set stamp")
	}
	if err := json.Unmarshal([]byte(`{"at":7}`), &empty); err == nil {
		t.Fatal("a numeric timestamp was accepted")
	}
}

func TestArithmetic(t *testing.T) {
	base := MustParse("2026-03-14T08:00:00Z")
	later := base.AddHours(26)
	if later.String() != "2026-03-15T10:00:00Z" {
		t.Fatalf("AddHours = %s", later)
	}
	if hours := later.HoursSince(base); hours != 26 {
		t.Fatalf("HoursSince = %v", hours)
	}
	if hours := base.HoursSince(later); hours != -26 {
		t.Fatalf("negative HoursSince = %v", hours)
	}
	if got := base.AddDays(-3).Day(); got != "2026-03-11" {
		t.Fatalf("AddDays = %s", got)
	}
	if got := base.StartOfDay().String(); got != "2026-03-14T00:00:00Z" {
		t.Fatalf("StartOfDay = %s", got)
	}
	if base.AddDays(1).DayIndex()-base.DayIndex() != 1 {
		t.Fatal("DayIndex did not advance by one across a day")
	}
}

func TestWindowIsHalfOpen(t *testing.T) {
	end := MustParse("2026-03-20T00:00:00Z")
	window := NewWindow(end, 7)
	if window.Days() != 7 {
		t.Fatalf("window days = %d", window.Days())
	}
	if !window.Contains(MustParse("2026-03-13T00:00:00Z")) {
		t.Fatal("the lower bound should be inside the window")
	}
	if window.Contains(MustParse("2026-03-20T00:00:00Z")) {
		t.Fatal("the upper bound should be outside the window")
	}
	if window.Contains(MustParse("2026-03-12T23:59:59Z")) {
		t.Fatal("an instant before the window was accepted")
	}
	var unset Stamp
	if window.Contains(unset) {
		t.Fatal("an unset stamp was accepted into a window")
	}
}

func TestLatestEarliestSkipUnset(t *testing.T) {
	stamps := []Stamp{{}, MustParse("2026-03-14T08:00:00Z"), {}, MustParse("2026-03-19T08:00:00Z")}
	latest, ok := Latest(stamps)
	if !ok || latest.Day() != "2026-03-19" {
		t.Fatalf("Latest = %s ok=%v", latest, ok)
	}
	earliest, ok := Earliest(stamps)
	if !ok || earliest.Day() != "2026-03-14" {
		t.Fatalf("Earliest = %s ok=%v", earliest, ok)
	}
	if _, ok := Latest([]Stamp{{}, {}}); ok {
		t.Fatal("Latest found an instant among unset stamps")
	}
}

func TestSortStamps(t *testing.T) {
	stamps := []Stamp{
		MustParse("2026-03-19T08:00:00Z"),
		MustParse("2026-03-14T08:00:00Z"),
		MustParse("2026-03-16T08:00:00Z"),
	}
	SortStamps(stamps)
	if stamps[0].Day() != "2026-03-14" || stamps[2].Day() != "2026-03-19" {
		t.Fatalf("sorted order = %v", stamps)
	}
}

func TestParseDay(t *testing.T) {
	stamp, err := ParseDay("2026-03-14")
	if err != nil {
		t.Fatalf("ParseDay: %v", err)
	}
	if stamp.String() != "2026-03-14T00:00:00Z" {
		t.Fatalf("ParseDay = %s", stamp)
	}
	if _, err := ParseDay("14/03/2026"); err == nil {
		t.Fatal("a non ISO day was accepted")
	}
}
