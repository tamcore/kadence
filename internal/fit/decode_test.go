package fit

import (
	"bytes"
	"testing"
	"time"

	fitencoder "github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/kit/datetime"
	"github.com/muktihari/fit/profile/factory"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/profile/untyped/fieldnum"
	"github.com/muktihari/fit/profile/untyped/mesgnum"
	"github.com/muktihari/fit/proto"
)

func TestDecodeReturnsActivityMetricSummaryAndLapSplits(t *testing.T) {
	got, err := Decode(bytes.NewReader(testActivityFIT(t, 2)))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	if got.Summary.Sport != "running" {
		t.Errorf("Summary.Sport = %q, want running", got.Summary.Sport)
	}
	if got.Summary.DistanceMeters != 12345.67 {
		t.Errorf("Summary.DistanceMeters = %v, want 12345.67", got.Summary.DistanceMeters)
	}
	if got.Summary.ElapsedSeconds != 3723.456 {
		t.Errorf("Summary.ElapsedSeconds = %v, want 3723.456", got.Summary.ElapsedSeconds)
	}
	if got.Summary.AverageHeartRateBPM != 145 {
		t.Errorf("Summary.AverageHeartRateBPM = %v, want 145", got.Summary.AverageHeartRateBPM)
	}
	if len(got.Splits) != 2 {
		t.Fatalf("len(Splits) = %d, want 2", len(got.Splits))
	}
	if got.Splits[0].DistanceMeters != 1000 {
		t.Errorf("Splits[0].DistanceMeters = %v, want 1000", got.Splits[0].DistanceMeters)
	}
	if got.Splits[1].AveragePaceSecondsPerKilometer != 300 {
		t.Errorf("Splits[1].AveragePaceSecondsPerKilometer = %v, want 300", got.Splits[1].AveragePaceSecondsPerKilometer)
	}
}

func TestDecodeRejectsInvalidFIT(t *testing.T) {
	if _, err := Decode(bytes.NewReader([]byte("not a FIT file"))); err == nil {
		t.Fatal("Decode() error = nil, want invalid FIT error")
	}
}

func TestDecodeBoundsLapSplits(t *testing.T) {
	got, err := Decode(bytes.NewReader(testActivityFIT(t, maxSplits+1)))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(got.Splits) != maxSplits {
		t.Errorf("len(Splits) = %d, want %d", len(got.Splits), maxSplits)
	}
	if !got.SplitsTruncated {
		t.Error("SplitsTruncated = false, want true")
	}
}

func testActivityFIT(t *testing.T, lapCount int) []byte {
	t.Helper()

	start := time.Date(2026, 7, 24, 6, 0, 0, 0, time.UTC)
	startFIT := datetime.ToUint32(start)
	endFIT := datetime.ToUint32(start.Add(3723456 * time.Millisecond))
	messages := make([]proto.Message, 0, 3+lapCount)
	messages = append(messages,
		proto.Message{Num: mesgnum.FileId, Fields: []proto.Field{
			factory.CreateField(mesgnum.FileId, fieldnum.FileIdType).WithValue(typedef.FileActivity),
			factory.CreateField(mesgnum.FileId, fieldnum.FileIdTimeCreated).WithValue(startFIT),
		}},
		proto.Message{Num: mesgnum.Activity, Fields: []proto.Field{
			factory.CreateField(mesgnum.Activity, fieldnum.ActivityType).WithValue(typedef.ActivityManual),
			factory.CreateField(mesgnum.Activity, fieldnum.ActivityTimestamp).WithValue(endFIT),
			factory.CreateField(mesgnum.Activity, fieldnum.ActivityNumSessions).WithValue(uint16(1)),
		}},
		proto.Message{Num: mesgnum.Session, Fields: []proto.Field{
			factory.CreateField(mesgnum.Session, fieldnum.SessionTimestamp).WithValue(endFIT),
			factory.CreateField(mesgnum.Session, fieldnum.SessionStartTime).WithValue(startFIT),
			factory.CreateField(mesgnum.Session, fieldnum.SessionSport).WithValue(typedef.SportRunning),
			factory.CreateField(mesgnum.Session, fieldnum.SessionTotalElapsedTime).WithValue(uint32(3723456)),
			factory.CreateField(mesgnum.Session, fieldnum.SessionTotalTimerTime).WithValue(uint32(3600000)),
			factory.CreateField(mesgnum.Session, fieldnum.SessionTotalDistance).WithValue(uint32(1234567)),
			factory.CreateField(mesgnum.Session, fieldnum.SessionTotalCalories).WithValue(uint16(900)),
			factory.CreateField(mesgnum.Session, fieldnum.SessionAvgSpeed).WithValue(uint16(3428)),
			factory.CreateField(mesgnum.Session, fieldnum.SessionMaxSpeed).WithValue(uint16(4500)),
			factory.CreateField(mesgnum.Session, fieldnum.SessionAvgHeartRate).WithValue(uint8(145)),
			factory.CreateField(mesgnum.Session, fieldnum.SessionMaxHeartRate).WithValue(uint8(172)),
			factory.CreateField(mesgnum.Session, fieldnum.SessionAvgCadence).WithValue(uint8(88)),
		}},
	)

	for i := range lapCount {
		messages = append(messages, proto.Message{Num: mesgnum.Lap, Fields: []proto.Field{
			factory.CreateField(mesgnum.Lap, fieldnum.LapTimestamp).WithValue(datetime.ToUint32(start.Add(time.Duration(i+1) * 5 * time.Minute))),
			factory.CreateField(mesgnum.Lap, fieldnum.LapStartTime).WithValue(datetime.ToUint32(start.Add(time.Duration(i) * 5 * time.Minute))),
			factory.CreateField(mesgnum.Lap, fieldnum.LapTotalElapsedTime).WithValue(uint32(300000)),
			factory.CreateField(mesgnum.Lap, fieldnum.LapTotalTimerTime).WithValue(uint32(300000)),
			factory.CreateField(mesgnum.Lap, fieldnum.LapTotalDistance).WithValue(uint32(100000)),
			factory.CreateField(mesgnum.Lap, fieldnum.LapTotalCalories).WithValue(uint16(72)),
			factory.CreateField(mesgnum.Lap, fieldnum.LapAvgSpeed).WithValue(uint16(3333)),
			factory.CreateField(mesgnum.Lap, fieldnum.LapMaxSpeed).WithValue(uint16(4000)),
			factory.CreateField(mesgnum.Lap, fieldnum.LapAvgHeartRate).WithValue(uint8(144)),
			factory.CreateField(mesgnum.Lap, fieldnum.LapMaxHeartRate).WithValue(uint8(152)),
			factory.CreateField(mesgnum.Lap, fieldnum.LapAvgCadence).WithValue(uint8(89)),
		}})
	}

	var out bytes.Buffer
	if err := fitencoder.New(&out).Encode(&proto.FIT{Messages: messages}); err != nil {
		t.Fatalf("encode test FIT: %v", err)
	}
	return out.Bytes()
}
