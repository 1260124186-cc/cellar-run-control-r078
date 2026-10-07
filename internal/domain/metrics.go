package domain

import "math"

type RunMetrics struct {
	FinalGravity        float64 `json:"final_gravity"`
	ApparentAttenuation float64 `json:"apparent_attenuation_percent"`
	EstimatedAlcoholABV float64 `json:"estimated_alcohol_abv"`
	ObservationCount    int     `json:"observation_count"`
}

func CalculateMetrics(originalGravity, finalGravity float64, count int) RunMetrics {
	attenuation := 0.0
	if originalGravity > 1 {
		attenuation = ((originalGravity - finalGravity) / (originalGravity - 1)) * 100
	}
	alcohol := (originalGravity - finalGravity) * 131.25
	if alcohol < 0 {
		alcohol = 0
	}
	return RunMetrics{
		FinalGravity:        round2(finalGravity),
		ApparentAttenuation: round2(attenuation),
		EstimatedAlcoholABV: round2(alcohol),
		ObservationCount:    count,
	}
}

func round2(value float64) float64 {
	return math.Round(value*100) / 100
}
