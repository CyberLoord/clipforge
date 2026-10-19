package domain

import (
	"math"
	"testing"
)

func TestMontageValidation(t *testing.T) {
	valid := func() []Clip {
		return []Clip{
			{Style: "dynamic", Title: "Движение", Reason: "Короткие планы", Cuts: []Cut{{10, 12}, {1, 3}, {7, 9}}},
			{Style: "cinematic", Title: "Плавный", Reason: "Длинные планы", Cuts: []Cut{{1, 5}, {10, 14}}},
		}
	}
	if err := ValidateMontages(valid(), 15); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		change func([]Clip) []Clip
	}{
		{"single variant", func(c []Clip) []Clip { return c[:1] }},
		{"duplicate style", func(c []Clip) []Clip { c[1].Style = c[0].Style; return c }},
		{"duplicate plan", func(c []Clip) []Clip { c[1].Cuts = c[0].Cuts; return c }},
		{"near duplicate", func(c []Clip) []Clip { c[1].Cuts = []Cut{{10.01, 12}, {1, 3}, {7, 9}}; return c }},
		{"overlapping cuts", func(c []Clip) []Clip { c[0].Cuts = []Cut{{0, 5}, {4, 7}}; return c }},
		{"whole source", func(c []Clip) []Clip { c[0].Cuts = []Cut{{0, 5}, {5, 10}, {10, 15}}; return c }},
		{"outside source", func(c []Clip) []Clip { c[0].Cuts[0].End = 16; return c }},
		{"short cut", func(c []Clip) []Clip { c[0].Cuts[0].End = 10.5; return c }},
		{"nonfinite", func(c []Clip) []Clip { c[0].Cuts[0].Start = math.NaN(); return c }},
		{"empty metadata", func(c []Clip) []Clip { c[0].Reason = ""; return c }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateMontages(tt.change(valid()), 15); err == nil {
				t.Fatal("invalid montage accepted")
			}
		})
	}
}

func TestFractionalMontageBoundaries(t *testing.T) {
	cuts := []Cut{{3.1, 4.1}, {5.1, 6.1}, {7.1, 8.1}, {9.1, 10.1}, {11.1, 12.1}}
	if err := ValidateCuts(cuts, 15); err != nil {
		t.Fatalf("exact one-second cuts rejected: %v", err)
	}
	cuts[0].End -= 0.001
	if err := ValidateCuts(cuts, 15); err == nil {
		t.Fatal("short cut accepted")
	}
}
