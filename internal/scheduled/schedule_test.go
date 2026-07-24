package scheduled_test

import (
	"testing"
	"time"

	"github.com/tamcore/kadence/internal/scheduled"
)

const (
	timezoneBerlin = "Europe/Berlin"
	timezoneUTC    = "UTC"
	timezoneMars   = "Mars/Olympus"
	rruleDaily     = "FREQ=DAILY"
)

func TestValidateTimezone(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		zone string
		want bool
	}{
		{name: "iana region", zone: timezoneBerlin, want: true},
		{name: "utc", zone: timezoneUTC, want: true},
		{name: "unknown", zone: timezoneMars, want: false},
		{name: "fixed offset", zone: "+02:00", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := scheduled.ValidateTimezone(tc.zone)
			if (err == nil) != tc.want {
				t.Fatalf("ValidateTimezone(%q) error = %v, want valid=%t", tc.zone, err, tc.want)
			}
		})
	}
}

func TestScheduleValidate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		spec scheduled.Schedule
		want bool
	}{
		{
			name: "future one off",
			spec: scheduled.Schedule{At: now.Add(time.Hour), Timezone: timezoneBerlin},
			want: true,
		},
		{
			name: "past one off",
			spec: scheduled.Schedule{At: now.Add(-time.Second), Timezone: timezoneBerlin},
			want: false,
		},
		{
			name: "daily rrule",
			spec: scheduled.Schedule{DTStart: now, RRULE: "FREQ=DAILY;INTERVAL=2", Timezone: timezoneBerlin},
			want: true,
		},
		{
			name: "fifty nine minutes is too frequent",
			spec: scheduled.Schedule{DTStart: now, RRULE: "FREQ=MINUTELY;INTERVAL=59", Timezone: timezoneBerlin},
			want: false,
		},
		{
			name: "one hour is allowed",
			spec: scheduled.Schedule{DTStart: now, RRULE: "FREQ=HOURLY", Timezone: timezoneBerlin},
			want: true,
		},
		{
			name: "missing schedule",
			spec: scheduled.Schedule{Timezone: timezoneBerlin},
			want: false,
		},
		{
			name: "both one off and recurrence",
			spec: scheduled.Schedule{At: now.Add(time.Hour), DTStart: now, RRULE: rruleDaily, Timezone: timezoneBerlin},
			want: false,
		},
		{
			name: "recurrence needs dtstart",
			spec: scheduled.Schedule{RRULE: rruleDaily, Timezone: timezoneBerlin},
			want: false,
		},
		{
			name: "secondly interval of an hour is allowed",
			spec: scheduled.Schedule{DTStart: now, RRULE: "FREQ=SECONDLY;INTERVAL=3600", Timezone: timezoneBerlin},
			want: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.Validate(now)
			if (err == nil) != tc.want {
				t.Fatalf("Validate() error = %v, want valid=%t", err, tc.want)
			}
		})
	}
}

func TestScheduleRejectsMultipleOccurrencesWithinAnHour(t *testing.T) {
	t.Parallel()
	spec := scheduled.Schedule{
		DTStart:  time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC),
		RRULE:    "FREQ=HOURLY;BYMINUTE=0,30",
		Timezone: timezoneUTC,
	}
	if err := spec.Validate(time.Now()); err == nil {
		t.Fatal("Validate() unexpectedly accepted two occurrences in one hour")
	}
}

func TestScheduleNextAfterPreservesLocalTimeAcrossDST(t *testing.T) {
	t.Parallel()
	berlin, err := time.LoadLocation(timezoneBerlin)
	if err != nil {
		t.Fatal(err)
	}
	spec := scheduled.Schedule{
		DTStart:  time.Date(2026, 3, 28, 9, 30, 0, 0, berlin),
		RRULE:    rruleDaily,
		Timezone: timezoneBerlin,
	}
	next, err := spec.NextAfter(time.Date(2026, 3, 28, 10, 0, 0, 0, berlin))
	if err != nil {
		t.Fatalf("NextAfter: %v", err)
	}
	want := time.Date(2026, 3, 29, 9, 30, 0, 0, berlin)
	if !next.Equal(want) || next.Hour() != 9 || next.Minute() != 30 {
		t.Fatalf("next = %s, want %s at 09:30 local", next, want)
	}
}

func TestScheduleCoalesceMissed(t *testing.T) {
	t.Parallel()
	spec := scheduled.Schedule{
		DTStart:  time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC),
		RRULE:    rruleDaily,
		Timezone: timezoneUTC,
	}
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	occurrence, next, err := spec.CoalesceMissed(now)
	if err != nil {
		t.Fatalf("CoalesceMissed: %v", err)
	}
	if want := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC); !occurrence.Equal(want) {
		t.Fatalf("occurrence = %s, want latest missed %s", occurrence, want)
	}
	if want := time.Date(2026, 7, 25, 9, 0, 0, 0, time.UTC); !next.Equal(want) {
		t.Fatalf("next = %s, want first future %s", next, want)
	}
}

func TestScheduleNoOccurrencePaths(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	oneOff := scheduled.Schedule{At: now, Timezone: timezoneUTC}
	if got, err := (scheduled.Schedule{At: now.Add(time.Hour), Timezone: timezoneUTC}).NextAfter(now); err != nil || !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("future one-off NextAfter = %s, %v", got, err)
	}
	if _, err := oneOff.NextAfter(now); err == nil {
		t.Fatal("expired one-off NextAfter unexpectedly succeeded")
	}
	if _, _, err := oneOff.CoalesceMissed(now); err == nil {
		t.Fatal("one-off CoalesceMissed unexpectedly succeeded")
	}
	counted := scheduled.Schedule{
		DTStart:  now,
		RRULE:    "FREQ=DAILY;COUNT=1",
		Timezone: timezoneUTC,
	}
	if _, err := counted.NextAfter(now); err == nil {
		t.Fatal("exhausted recurrence NextAfter unexpectedly succeeded")
	}
	if _, _, err := counted.CoalesceMissed(now); err == nil {
		t.Fatal("exhausted recurrence CoalesceMissed unexpectedly succeeded")
	}
	future := scheduled.Schedule{
		DTStart:  now.Add(time.Hour),
		RRULE:    rruleDaily,
		Timezone: timezoneUTC,
	}
	if _, _, err := future.CoalesceMissed(now); err == nil {
		t.Fatal("future recurrence CoalesceMissed unexpectedly succeeded")
	}
	invalid := scheduled.Schedule{DTStart: now, RRULE: "not an rrule", Timezone: timezoneUTC}
	if _, err := invalid.NextAfter(now); err == nil {
		t.Fatal("invalid recurrence NextAfter unexpectedly succeeded")
	}
	if _, _, err := invalid.CoalesceMissed(now); err == nil {
		t.Fatal("invalid recurrence CoalesceMissed unexpectedly succeeded")
	}
	if err := (scheduled.Schedule{At: now.Add(time.Hour), Timezone: timezoneMars}).Validate(now); err == nil {
		t.Fatal("invalid timezone Validate unexpectedly succeeded")
	}
	if _, err := (scheduled.Schedule{RRULE: rruleDaily, Timezone: timezoneMars}).NextAfter(now); err == nil {
		t.Fatal("invalid timezone recurrence unexpectedly succeeded")
	}
	if _, err := (scheduled.Schedule{RRULE: rruleDaily, Timezone: timezoneUTC}).NextAfter(now); err == nil {
		t.Fatal("missing DTSTART recurrence unexpectedly succeeded")
	}
	tooFast := scheduled.Schedule{DTStart: now, RRULE: "FREQ=SECONDLY;INTERVAL=3599", Timezone: timezoneUTC}
	if err := tooFast.Validate(now); err == nil {
		t.Fatal("too-fast secondly recurrence unexpectedly succeeded")
	}
}

func TestScheduleAcceptsRRulePrefix(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	spec := scheduled.Schedule{DTStart: now, RRULE: "RRULE:FREQ=DAILY;COUNT=1", Timezone: timezoneUTC}
	if err := spec.Validate(now); err != nil {
		t.Fatalf("Validate with RRULE prefix: %v", err)
	}
}

func TestOccurrenceKey(t *testing.T) {
	t.Parallel()
	instant := time.Date(2026, 7, 24, 9, 30, 45, 123456789, time.FixedZone("CEST", 2*60*60))
	if got, want := scheduled.OccurrenceKey(instant), "2026-07-24T07:30:45.123456789Z"; got != want {
		t.Fatalf("OccurrenceKey() = %q, want %q", got, want)
	}
}
