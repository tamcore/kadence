package pace

import (
	"reflect"
	"testing"
)

const (
	testMetricUnit        = "metric"
	testImperialUnit      = "imperial"
	testMPSOutput         = "mps"
	testMPSUnit           = "m/s"
	testMetricDisplayUnit = "min/km"
	testPace452           = "4:52"
	testPace750           = "7:50"
)

func TestConvert(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		want Result
	}{
		{
			name: "metric to meters per second",
			req:  Request{Unit: testMetricUnit, TargetPace: testPace452, Output: testMPSOutput},
			want: Result{Value: 3.4246575342465753, Unit: testMPSUnit},
		},
		{
			name: "rounding regression",
			req:  Request{Unit: testMetricUnit, TargetPace: "5:35", Output: testMPSOutput},
			want: Result{Value: 2.985074626865672, Unit: testMPSUnit},
		},
		{
			name: "metric to imperial",
			req:  Request{Unit: testMetricUnit, TargetPace: testPace452, Output: testImperialUnit},
			want: Result{Value: testPace750, Unit: "min/mi"},
		},
		{
			name: "imperial to metric",
			req:  Request{Unit: testImperialUnit, TargetPace: testPace750, Output: testMetricUnit},
			want: Result{Value: testPace452, Unit: testMetricDisplayUnit},
		},
		{
			name: "same unit normalizes",
			req:  Request{Unit: testMetricUnit, TargetPace: "0:01", Output: testMetricUnit},
			want: Result{Value: "0:01", Unit: testMetricDisplayUnit},
		},
		{
			name: "imperial to meters per second",
			req:  Request{Unit: testImperialUnit, TargetPace: testPace750, Output: testMPSOutput},
			want: Result{Value: 3.424136170212766, Unit: testMPSUnit},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Convert(tt.req)
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Convert = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestConvertRejectsInvalidInput(t *testing.T) {
	tests := []Request{
		{Unit: "", TargetPace: testPace452, Output: testMPSOutput},
		{Unit: testMPSOutput, TargetPace: testPace452, Output: testMPSOutput},
		{Unit: testMetricUnit, TargetPace: "", Output: testMPSOutput},
		{Unit: testMetricUnit, TargetPace: "04:52", Output: testMPSOutput},
		{Unit: testMetricUnit, TargetPace: "4:5", Output: testMPSOutput},
		{Unit: testMetricUnit, TargetPace: "4:60", Output: testMPSOutput},
		{Unit: testMetricUnit, TargetPace: "0:00", Output: testMPSOutput},
		{Unit: testMetricUnit, TargetPace: "-4:52", Output: testMPSOutput},
		{Unit: testMetricUnit, TargetPace: "153722867280912930:08", Output: testMPSOutput},
		{Unit: testMetricUnit, TargetPace: testPace452, Output: ""},
		{Unit: testMetricUnit, TargetPace: testPace452, Output: "watts"},
	}
	for _, req := range tests {
		if _, err := Convert(req); err == nil {
			t.Errorf("Convert(%+v) succeeded", req)
		}
	}
}
