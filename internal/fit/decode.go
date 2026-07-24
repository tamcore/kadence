// Package fit decodes activity FIT files into a small, metric-labelled model.
package fit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/profile/mesgdef"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/profile/untyped/mesgnum"
	"github.com/muktihari/fit/proto"
)

const (
	maxFITBytes = 32 << 20
	maxSplits   = 100
)

var (
	// ErrInputTooLarge means the FIT input exceeded the decoder's in-memory limit.
	ErrInputTooLarge = errors.New("FIT input exceeds maximum size")
	// ErrNotActivity means the decoded FIT data was not an activity with a session.
	ErrNotActivity = errors.New("FIT data is not an activity")
)

// Activity is the bounded subset of an activity FIT file that is safe to
// return to a caller. It deliberately contains no GPS positions or records.
type Activity struct {
	Summary         Summary `json:"summary"`
	Splits          []Split `json:"splits"`
	SplitsTruncated bool    `json:"splits_truncated"`
}

// Summary contains activity metrics in the units named by each field.
type Summary struct {
	Sport                  string    `json:"sport"`
	StartTime              time.Time `json:"start_time"`
	ElapsedSeconds         float64   `json:"elapsed_seconds"`
	TimerSeconds           float64   `json:"timer_seconds"`
	DistanceMeters         float64   `json:"distance_meters"`
	Calories               uint16    `json:"calories"`
	AverageSpeedMetersPerS float64   `json:"average_speed_meters_per_second"`
	MaximumSpeedMetersPerS float64   `json:"maximum_speed_meters_per_second"`
	AverageHeartRateBPM    uint8     `json:"average_heart_rate_bpm"`
	MaximumHeartRateBPM    uint8     `json:"maximum_heart_rate_bpm"`
	AverageCadenceRPM      uint8     `json:"average_cadence_rpm"`
}

// Split contains the metrics for one FIT lap, in the units named by each
// field. It intentionally excludes positions, records, and arbitrary FIT
// developer data.
type Split struct {
	StartTime                      time.Time `json:"start_time"`
	ElapsedSeconds                 float64   `json:"elapsed_seconds"`
	TimerSeconds                   float64   `json:"timer_seconds"`
	DistanceMeters                 float64   `json:"distance_meters"`
	Calories                       uint16    `json:"calories"`
	AverageSpeedMetersPerS         float64   `json:"average_speed_meters_per_second"`
	MaximumSpeedMetersPerS         float64   `json:"maximum_speed_meters_per_second"`
	AveragePaceSecondsPerKilometer float64   `json:"average_pace_seconds_per_kilometer"`
	AverageHeartRateBPM            uint8     `json:"average_heart_rate_bpm"`
	MaximumHeartRateBPM            uint8     `json:"maximum_heart_rate_bpm"`
	AverageCadenceRPM              uint8     `json:"average_cadence_rpm"`
}

// Decode validates and decodes one FIT activity from r. The input and output
// are both bounded: at most 32 MiB are read and at most 100 lap splits are
// retained. FIT record samples are discarded as they are decoded.
func Decode(r io.Reader) (Activity, error) {
	if r == nil {
		return Activity{}, fmt.Errorf("decode FIT activity: %w", ErrNotActivity)
	}

	data, err := io.ReadAll(io.LimitReader(r, maxFITBytes+1))
	if err != nil {
		return Activity{}, fmt.Errorf("read FIT activity: %w", err)
	}
	if len(data) > maxFITBytes {
		return Activity{}, ErrInputTooLarge
	}

	collector := newActivityCollector()
	dec := decoder.New(bytes.NewReader(data), decoder.WithMesgListener(collector), decoder.WithBroadcastOnly())
	if _, err := dec.Decode(); err != nil {
		return Activity{}, fmt.Errorf("decode FIT activity: %w", err)
	}
	if !collector.isActivity || !collector.hasSummary {
		return Activity{}, ErrNotActivity
	}

	return Activity{
		Summary:         collector.summary,
		Splits:          collector.splits,
		SplitsTruncated: collector.splitsTruncated,
	}, nil
}

type activityCollector struct {
	isActivity      bool
	hasSummary      bool
	summary         Summary
	splits          []Split
	splitsTruncated bool
}

func newActivityCollector() *activityCollector {
	return &activityCollector{splits: make([]Split, 0, maxSplits)}
}

func (c *activityCollector) OnMesg(mesg proto.Message) {
	switch mesg.Num {
	case mesgnum.FileId:
		c.isActivity = mesgdef.NewFileId(&mesg).Type == typedef.FileActivity
	case mesgnum.Session:
		if !c.hasSummary {
			c.summary = summaryFrom(mesgdef.NewSession(&mesg))
			c.hasSummary = true
		}
	case mesgnum.Lap:
		if len(c.splits) == maxSplits {
			c.splitsTruncated = true
			return
		}
		c.splits = append(c.splits, splitFrom(mesgdef.NewLap(&mesg)))
	}
}

func summaryFrom(session *mesgdef.Session) Summary {
	return Summary{
		Sport:                  session.Sport.String(),
		StartTime:              session.StartTime,
		ElapsedSeconds:         session.TotalElapsedTimeScaled(),
		TimerSeconds:           session.TotalTimerTimeScaled(),
		DistanceMeters:         session.TotalDistanceScaled(),
		Calories:               session.TotalCalories,
		AverageSpeedMetersPerS: session.AvgSpeedScaled(),
		MaximumSpeedMetersPerS: session.MaxSpeedScaled(),
		AverageHeartRateBPM:    session.AvgHeartRate,
		MaximumHeartRateBPM:    session.MaxHeartRate,
		AverageCadenceRPM:      session.AvgCadence,
	}
}

func splitFrom(lap *mesgdef.Lap) Split {
	distanceMeters := lap.TotalDistanceScaled()
	paceSecondsPerKilometer := 0.0
	if distanceMeters > 0 {
		paceSecondsPerKilometer = lap.TotalTimerTimeScaled() * 1000 / distanceMeters
	}

	return Split{
		StartTime:                      lap.StartTime,
		ElapsedSeconds:                 lap.TotalElapsedTimeScaled(),
		TimerSeconds:                   lap.TotalTimerTimeScaled(),
		DistanceMeters:                 distanceMeters,
		Calories:                       lap.TotalCalories,
		AverageSpeedMetersPerS:         lap.AvgSpeedScaled(),
		MaximumSpeedMetersPerS:         lap.MaxSpeedScaled(),
		AveragePaceSecondsPerKilometer: paceSecondsPerKilometer,
		AverageHeartRateBPM:            lap.AvgHeartRate,
		MaximumHeartRateBPM:            lap.MaxHeartRate,
		AverageCadenceRPM:              lap.AvgCadence,
	}
}
