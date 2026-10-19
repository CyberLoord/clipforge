package domain

import (
	"math"
	"testing"
)

func TestValidateClips(t *testing.T) {
	valid := Clip{Start: 0, End: 20, Title: "Title", Reason: "Reason"}
	tests := []struct {
		name      string
		clips     []Clip
		wantError bool
	}{
		{"valid", []Clip{valid}, false},
		{"minimum", []Clip{{Start: 0, End: 5, Title: "a", Reason: "b"}}, false},
		{"short video", []Clip{{Start: 0, End: 17.642667, Title: "a", Reason: "b"}}, false},
		{"empty", nil, true},
		{"out of bounds", []Clip{{Start: 90, End: 120, Title: "a", Reason: "b"}}, true},
		{"reversed", []Clip{{Start: 30, End: 10, Title: "a", Reason: "b"}}, true},
		{"too short", []Clip{{Start: 0, End: 4.999, Title: "a", Reason: "b"}}, true},
		{"too long", []Clip{{Start: 0, End: 61, Title: "a", Reason: "b"}}, true},
		{"duplicate", []Clip{valid, valid}, true},
		{"nan", []Clip{{Start: math.NaN(), End: 30, Title: "a", Reason: "b"}}, true},
		{"missing title", []Clip{{Start: 0, End: 20, Reason: "b"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateClips(tt.clips, 100); (err != nil) != tt.wantError {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestValidateClipDurationBoundaries(t *testing.T) {
	for _, tt := range []struct {
		duration  float64
		wantError bool
	}{
		{5, false},
		{15, false},
		{17.642667, false},
		{4.999, true},
	} {
		err := ValidateClip(Clip{Start: 0, End: tt.duration}, tt.duration)
		if (err != nil) != tt.wantError {
			t.Fatalf("duration=%g error=%v", tt.duration, err)
		}
	}
}

func TestValidateSegments(t *testing.T) {
	for _, segments := range [][]Segment{
		nil, {{Start: 0, End: 101, Text: "outside"}}, {{Start: 0, End: 2, Text: ""}}, {{Start: 5, End: 10, Text: "first"}, {Start: 0, End: 3, Text: "second"}},
	} {
		if ValidateSegments(segments, 100) == nil {
			t.Fatal("accepted invalid transcript")
		}
	}
	if err := ValidateSegments([]Segment{{Start: 0, End: 10, Text: "Привет"}}, 100); err != nil {
		t.Fatal(err)
	}
}
