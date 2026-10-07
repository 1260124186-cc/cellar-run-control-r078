package domain

import "time"

type GravityObservation struct {
	RunID        string    `json:"run_id"`
	Sequence     int       `json:"sequence"`
	Gravity      float64   `json:"gravity"`
	TemperatureC float64   `json:"temperature_c"`
	ObservedAt   time.Time `json:"observed_at"`
	Note         string    `json:"note,omitempty"`
	ReceivedAt   time.Time `json:"received_at"`
}

func ValidateObservation(
	runStartedAt time.Time,
	previous *GravityObservation,
	candidate GravityObservation,
	now time.Time,
) error {
	if candidate.Gravity < 0.980 || candidate.Gravity > 1.200 {
		return FieldError(CodeInvalidInput, "gravity",
			"must be between 0.980 and 1.200")
	}
	if candidate.TemperatureC < -5 || candidate.TemperatureC > 60 {
		return FieldError(CodeInvalidInput, "temperature_c",
			"must be between -5 and 60 Celsius")
	}
	if len([]rune(candidate.Note)) > 500 {
		return FieldError(CodeInvalidInput, "note",
			"must not exceed 500 characters")
	}
	observed := candidate.ObservedAt.UTC()
	if observed.Before(runStartedAt.UTC()) {
		return FieldError(CodeInvalidInput, "observed_at",
			"must not precede the run start")
	}
	if observed.After(now.UTC().Add(time.Minute)) {
		return FieldError(CodeInvalidInput, "observed_at",
			"must not be in the future")
	}
	if previous != nil {
		if !observed.After(previous.ObservedAt.UTC()) {
			return NewError(CodeObservationConflict,
				"observation time must increase strictly")
		}
		if candidate.Gravity-previous.Gravity > 0.010 {
			return NewError(CodeObservationConflict,
				"gravity must not rise more than 0.010 above the previous value")
		}
	}
	return nil
}
