package domain

import "time"

type RunState string

const (
	RunPlanned      RunState = "planned"
	RunReserved     RunState = "reserved"
	RunFermenting   RunState = "fermenting"
	RunConditioning RunState = "conditioning"
	RunCompleted    RunState = "completed"
	RunAborted      RunState = "aborted"
)

type RecipeStepProgress struct {
	Sequence     int        `json:"sequence"`
	Label        string     `json:"label"`
	TemperatureC float64    `json:"temperature_c"`
	HoldMinutes  int        `json:"hold_minutes"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

type FermentationRun struct {
	ID             string               `json:"id"`
	Code           string               `json:"code"`
	FormulaID      string               `json:"formula_id"`
	VesselID       *string              `json:"vessel_id,omitempty"`
	PlannedVolumeL float64              `json:"planned_volume_liters"`
	Notes          string               `json:"notes,omitempty"`
	State          RunState             `json:"state"`
	StartedAt      *time.Time           `json:"started_at,omitempty"`
	EndedAt        *time.Time           `json:"ended_at,omitempty"`
	Observations   []GravityObservation `json:"observations"`
	StepProgress   []RecipeStepProgress `json:"step_progress,omitempty"`
	CurrentStep    int                  `json:"current_step,omitempty"`
	Metrics        *RunMetrics          `json:"metrics,omitempty"`
	Version        int64                `json:"version"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
}

func (r FermentationRun) Clone() FermentationRun {
	cloned := r
	cloned.Observations = append([]GravityObservation(nil), r.Observations...)
	cloned.StepProgress = make([]RecipeStepProgress, len(r.StepProgress))
	for index, step := range r.StepProgress {
		cloned.StepProgress[index] = step
		if step.StartedAt != nil {
			value := *step.StartedAt
			cloned.StepProgress[index].StartedAt = &value
		}
		if step.CompletedAt != nil {
			value := *step.CompletedAt
			cloned.StepProgress[index].CompletedAt = &value
		}
	}
	if r.VesselID != nil {
		value := *r.VesselID
		cloned.VesselID = &value
	}
	if r.StartedAt != nil {
		value := *r.StartedAt
		cloned.StartedAt = &value
	}
	if r.EndedAt != nil {
		value := *r.EndedAt
		cloned.EndedAt = &value
	}
	if r.Metrics != nil {
		value := *r.Metrics
		cloned.Metrics = &value
	}
	return cloned
}

func (r FermentationRun) Validate() error {
	if err := ValidateRunCode(r.Code, "code"); err != nil {
		return err
	}
	if r.PlannedVolumeL <= 0 {
		return FieldError(CodeInvalidInput, "planned_volume_liters",
			"must be greater than zero")
	}
	if len([]rune(r.Notes)) > 1000 {
		return FieldError(CodeInvalidInput, "notes",
			"must not exceed 1000 characters")
	}
	return nil
}

func (r *FermentationRun) ReserveVessel(vesselID string, now time.Time) error {
	if r.State != RunPlanned {
		return NewError(CodeRunState, "only a planned run can reserve a vessel")
	}
	value := vesselID
	r.VesselID = &value
	r.State = RunReserved
	r.UpdatedAt = now.UTC()
	r.Version++
	return nil
}

func (r *FermentationRun) StartFermentation(now time.Time, recipe []FormulaStep) error {
	if r.State != RunReserved || r.VesselID == nil {
		return NewError(CodeRunState, "only a reserved run can start fermentation")
	}
	stamp := now.UTC()
	r.State = RunFermenting
	r.StartedAt = &stamp
	r.StepProgress = make([]RecipeStepProgress, 0, len(recipe))
	for _, step := range recipe {
		r.StepProgress = append(r.StepProgress, RecipeStepProgress{
			Sequence:     step.Sequence,
			Label:        step.Label,
			TemperatureC: step.TemperatureC,
			HoldMinutes:  step.HoldMinutes,
		})
	}
	r.CurrentStep = 0
	if len(r.StepProgress) > 0 {
		started := stamp
		r.StepProgress[0].StartedAt = &started
		r.CurrentStep = 1
	}
	r.UpdatedAt = stamp
	r.Version++
	return nil
}

// StepsComplete reports whether every formula step has a recorded
// completion time. A single completed step is not enough: completions form a
// strict prefix and the run is not ready until the final step is recorded.
func (r FermentationRun) StepsComplete() bool {
	if len(r.StepProgress) == 0 {
		return true
	}
	for _, step := range r.StepProgress {
		if step.CompletedAt == nil {
			return false
		}
	}
	return true
}

func (r FermentationRun) RequireStepsComplete() error {
	if !r.StepsComplete() {
		return NewError(CodeRunState,
			"all formula steps must be completed before this transition")
	}
	return nil
}

// IsRecipeStepConfirmed reports whether the requested step already has a
// recorded completion. Such a confirmation is an idempotent replay: it must
// return the recorded fact without advancing the version or rewriting times.
func (r FermentationRun) IsRecipeStepConfirmed(sequence int) bool {
	if sequence < 1 || sequence > len(r.StepProgress) {
		return false
	}
	return r.StepProgress[sequence-1].CompletedAt != nil
}

// AdvanceRecipeStep records completion of the single current formula step.
// It returns changed=false (and mutates nothing) when the request replays a
// step that is already recorded.
func (r *FermentationRun) AdvanceRecipeStep(sequence int, now time.Time) (bool, error) {
	if r.State != RunFermenting {
		return false, NewError(CodeRunState,
			"only a fermenting run can advance formula steps")
	}
	if sequence < 1 || sequence > len(r.StepProgress) {
		return false, NewError(CodeRunState, "formula step was not found")
	}
	step := &r.StepProgress[sequence-1]
	if step.CompletedAt != nil {
		// Repeated confirmation of the same fact is a no-op.
		return false, nil
	}
	if sequence != r.CurrentStep {
		return false, NewError(CodeRunState,
			"only the current formula step can be completed")
	}
	if step.StartedAt == nil {
		return false, NewError(CodeRunState,
			"current formula step has no start time")
	}
	hold := time.Duration(step.HoldMinutes) * time.Minute
	if now.UTC().Before(step.StartedAt.UTC().Add(hold)) {
		return false, NewError(CodeRunState,
			"formula step hold time has not elapsed")
	}
	stamp := now.UTC()
	step.CompletedAt = &stamp
	if sequence < len(r.StepProgress) {
		r.CurrentStep = sequence + 1
		next := &r.StepProgress[sequence]
		started := stamp
		next.StartedAt = &started
	} else {
		r.CurrentStep = len(r.StepProgress) + 1
	}
	r.UpdatedAt = stamp
	r.Version++
	return true, nil
}

func (r *FermentationRun) AddObservation(observation GravityObservation) error {
	if r.State != RunFermenting || r.StartedAt == nil {
		return NewError(CodeRunState, "observations require a fermenting run")
	}
	if len(r.Observations) >= 500 {
		return NewError(CodeObservationConflict,
			"run cannot contain more than 500 observations")
	}
	var previous *GravityObservation
	if len(r.Observations) > 0 {
		value := r.Observations[len(r.Observations)-1]
		previous = &value
	}
	if err := ValidateObservation(*r.StartedAt, previous, observation, observation.ReceivedAt); err != nil {
		return err
	}
	expected := len(r.Observations) + 1
	if observation.Sequence != expected {
		return NewError(CodeObservationConflict,
			"observation sequence must be contiguous and begin at 1")
	}
	observation.RunID = r.ID
	r.Observations = append(r.Observations, observation)
	r.UpdatedAt = observation.ObservedAt.UTC()
	r.Version++
	return nil
}

func (r *FermentationRun) BeginConditioning(now time.Time) error {
	if r.State != RunFermenting {
		return NewError(CodeRunState, "only a fermenting run can enter conditioning")
	}
	if err := r.RequireStepsComplete(); err != nil {
		return err
	}
	if len(r.Observations) < 2 {
		return NewError(CodeRunState,
			"conditioning requires at least two observations")
	}
	r.State = RunConditioning
	r.UpdatedAt = now.UTC()
	r.Version++
	return nil
}

func (r *FermentationRun) Complete(originalGravity float64, now time.Time) error {
	if r.State != RunConditioning {
		return NewError(CodeRunState, "only a conditioning run can be completed")
	}
	if err := r.RequireStepsComplete(); err != nil {
		return err
	}
	if len(r.Observations) < 3 {
		return NewError(CodeRunState,
			"completion requires at least three observations")
	}
	stamp := now.UTC()
	final := r.Observations[len(r.Observations)-1]
	metrics := CalculateMetrics(originalGravity, final.Gravity, len(r.Observations))
	r.Metrics = &metrics
	r.State = RunCompleted
	r.EndedAt = &stamp
	r.UpdatedAt = stamp
	r.Version++
	return nil
}

func (r *FermentationRun) Abort(now time.Time) error {
	if r.State == RunCompleted || r.State == RunAborted {
		return NewError(CodeRunState, "completed or aborted runs cannot be aborted")
	}
	stamp := now.UTC()
	r.State = RunAborted
	r.EndedAt = &stamp
	r.UpdatedAt = stamp
	r.Version++
	return nil
}

func (r FermentationRun) HadStarted() bool {
	return r.StartedAt != nil
}
