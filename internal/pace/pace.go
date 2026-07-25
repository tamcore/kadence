// Package pace converts human running paces without model arithmetic.
package pace

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

const (
	metricDistanceMeters   = 1000.0
	imperialDistanceMeters = 1609.344
	maxInt64               = int64(1<<63 - 1)
	metric                 = "metric"
	imperial               = "imperial"
	metersPerSecond        = "mps"
	metersPerSecondUnit    = "m/s"
	minutesPerKilometer    = "min/km"
	minutesPerMile         = "min/mi"
)

var pacePattern = regexp.MustCompile(`^(0|[1-9][0-9]*):([0-5][0-9])$`)

type Request struct {
	Unit       string
	TargetPace string
	Output     string
}

type Result struct {
	Value any    `json:"value"`
	Unit  string `json:"unit"`
}

func Convert(req Request) (Result, error) {
	inputDistance, err := distance(req.Unit)
	if err != nil {
		return Result{}, fmt.Errorf("input unit: %w", err)
	}
	if req.Output != metric && req.Output != imperial && req.Output != metersPerSecond {
		return Result{}, errors.New("output must be metric, imperial, or mps")
	}
	inputSeconds, err := parse(req.TargetPace)
	if err != nil {
		return Result{}, err
	}

	speed := inputDistance / float64(inputSeconds)
	if speed <= 0 || math.IsNaN(speed) || math.IsInf(speed, 0) {
		return Result{}, errors.New("pace conversion produced a non-finite speed")
	}
	if req.Output == metersPerSecond {
		return Result{Value: speed, Unit: metersPerSecondUnit}, nil
	}
	if req.Output == req.Unit {
		return Result{Value: format(inputSeconds), Unit: displayUnit(req.Output)}, nil
	}

	outputDistance, err := distance(req.Output)
	if err != nil {
		return Result{}, fmt.Errorf("output unit: %w", err)
	}
	outputSeconds := math.Round(outputDistance / speed)
	if outputSeconds <= 0 || outputSeconds >= float64(maxInt64) ||
		math.IsNaN(outputSeconds) || math.IsInf(outputSeconds, 0) {
		return Result{}, errors.New("converted pace is out of range")
	}
	return Result{
		Value: format(int64(outputSeconds)),
		Unit:  displayUnit(req.Output),
	}, nil
}

func distance(unit string) (float64, error) {
	switch unit {
	case metric:
		return metricDistanceMeters, nil
	case imperial:
		return imperialDistanceMeters, nil
	default:
		return 0, errors.New("must be metric or imperial")
	}
}

func parse(value string) (int64, error) {
	match := pacePattern.FindStringSubmatch(value)
	if match == nil {
		return 0, errors.New("targetpace must use strict M:SS")
	}
	minutes, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, errors.New("targetpace is out of range")
	}
	seconds, err := strconv.ParseInt(match[2], 10, 64)
	if err != nil || minutes > (maxInt64-seconds)/60 {
		return 0, errors.New("targetpace is out of range")
	}
	total := minutes*60 + seconds
	if total <= 0 {
		return 0, errors.New("targetpace must be greater than zero")
	}
	return total, nil
}

func format(seconds int64) string {
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

func displayUnit(unit string) string {
	if unit == metric {
		return minutesPerKilometer
	}
	return minutesPerMile
}
