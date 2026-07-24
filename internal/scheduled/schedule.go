// Package scheduled contains the scheduling domain shared by the API and worker.
package scheduled

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/teambition/rrule-go"
)

var errNoOccurrence = errors.New("scheduled: no occurrence after time")

// Schedule is either a future one-off instant or an RFC 5545 RRULE whose
// DTSTART is interpreted in Timezone.
type Schedule struct {
	At       time.Time
	DTStart  time.Time
	RRULE    string
	Timezone string
}

// ValidateTimezone loads an IANA location accepted for Scheduled tasks.
func ValidateTimezone(name string) (*time.Location, error) {
	if name != "UTC" && !strings.Contains(name, "/") {
		return nil, fmt.Errorf("scheduled: timezone %q is not an IANA timezone", name)
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("scheduled: load timezone %q: %w", name, err)
	}
	return location, nil
}

// Validate confirms the schedule has exactly one representation and cannot
// create an immediately overdue one-off task.
func (s Schedule) Validate(now time.Time) error {
	if _, err := ValidateTimezone(s.Timezone); err != nil {
		return err
	}
	hasOneOff := !s.At.IsZero()
	hasRecurrence := strings.TrimSpace(s.RRULE) != ""
	if hasOneOff == hasRecurrence {
		return errors.New("scheduled: specify exactly one of at or rrule")
	}
	if hasOneOff {
		if !s.At.After(now) {
			return errors.New("scheduled: one-off time must be in the future")
		}
		return nil
	}
	if s.DTStart.IsZero() {
		return errors.New("scheduled: DTSTART is required for a recurrence")
	}
	_, err := s.recurrence()
	return err
}

// NextAfter returns the first occurrence strictly after after.
func (s Schedule) NextAfter(after time.Time) (time.Time, error) {
	if !s.At.IsZero() {
		if s.At.After(after) {
			return s.At, nil
		}
		return time.Time{}, errNoOccurrence
	}
	rule, err := s.recurrence()
	if err != nil {
		return time.Time{}, err
	}
	next := rule.After(after, false)
	if next.IsZero() {
		return time.Time{}, errNoOccurrence
	}
	return next, nil
}

// CoalesceMissed returns one catch-up occurrence (the most recent due one)
// and the following future occurrence. It is for recurring schedules only.
func (s Schedule) CoalesceMissed(now time.Time) (time.Time, time.Time, error) {
	if !s.At.IsZero() {
		return time.Time{}, time.Time{}, errors.New("scheduled: one-off schedules cannot coalesce")
	}
	rule, err := s.recurrence()
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	missed := rule.Before(now, true)
	if missed.IsZero() {
		return time.Time{}, time.Time{}, errNoOccurrence
	}
	next := rule.After(now, false)
	if next.IsZero() {
		return time.Time{}, time.Time{}, errNoOccurrence
	}
	return missed, next, nil
}

// OccurrenceKey is the stable UTC identity of one task occurrence.
func OccurrenceKey(occurrence time.Time) string {
	return occurrence.UTC().Format(time.RFC3339Nano)
}

func (s Schedule) recurrence() (*rrule.RRule, error) {
	location, err := ValidateTimezone(s.Timezone)
	if err != nil {
		return nil, err
	}
	if s.DTStart.IsZero() {
		return nil, errors.New("scheduled: DTSTART is required for a recurrence")
	}
	ruleText := strings.TrimPrefix(strings.TrimSpace(s.RRULE), "RRULE:")
	rule, err := rrule.StrToRRule(ruleText)
	if err != nil {
		return nil, fmt.Errorf("scheduled: parse rrule: %w", err)
	}
	rule.DTStart(s.DTStart.In(location))
	if err := validateMinimumInterval(rule); err != nil {
		return nil, err
	}
	return rule, nil
}

func validateMinimumInterval(rule *rrule.RRule) error {
	options := rule.OrigOptions
	interval := max(options.Interval, 1)
	switch options.Freq {
	case rrule.SECONDLY:
		if interval < 3600 {
			return errors.New("scheduled: recurring interval must be at least one hour")
		}
	case rrule.MINUTELY:
		if interval < 60 {
			return errors.New("scheduled: recurring interval must be at least one hour")
		}
	}

	previous := rule.GetDTStart()
	for range 128 {
		next := rule.After(previous, false)
		if next.IsZero() {
			return nil
		}
		if next.Sub(previous) < time.Hour {
			return errors.New("scheduled: recurring interval must be at least one hour")
		}
		previous = next
	}
	return nil
}
