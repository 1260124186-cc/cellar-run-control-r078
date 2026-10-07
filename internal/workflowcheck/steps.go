package workflowcheck

import (
	"fmt"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/clock"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/httpapi"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/service"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/storage"
)

// stepExecution exercises formula process-step confirmation: a single
// current step, real hold durations, idempotent replays, prefix ordering,
// restart durability, and the conditioning/completion gates.
func stepExecution(api *client, fixedClock *clock.Fixed, dataDir string) error {
	formula, err := createStepFormula(api, "Step Execution Formula")
	if err != nil {
		return err
	}

	if err := verifyActiveStepRules(api, fixedClock, formula.ID); err != nil {
		return err
	}
	if err := verifyStepDurability(api, fixedClock, dataDir, formula.ID); err != nil {
		return err
	}
	return nil
}

// verifyActiveStepRules checks hold timing, observations, ordering, stale
// versions, concurrent replays, and terminal-state guards on a fresh run.
func verifyActiveStepRules(api *client, fixedClock *clock.Fixed, formulaID string) error {
	vessel, err := createCheckVessel(api, "VSL-303", 300)
	if err != nil {
		return err
	}
	started, err := createStartedRunAt(api, formulaID, vessel.ID, "RUN-STEPS-GUARD",
		fixedClock.Advance, false)
	if err != nil {
		return err
	}
	runID := started.Run.ID
	runStart := *started.Run.StartedAt

	// Before any time passes the hold has not elapsed.
	early, err := advanceStepRaw(api, runID, 1, nil)
	if err != nil {
		return err
	}
	if err := requireCode(early, 409, string(domain.CodeRunState),
		"confirm step before its hold elapses"); err != nil {
		return err
	}

	// A backfilled observation changes UpdatedAt but must not satisfy or
	// reset the step hold, which is measured from the step's start time.
	obsResponse, err := api.call("POST", "/v1/runs/"+runID+"/observations", map[string]any{
		"sequence":      1,
		"gravity":       1.050,
		"temperature_c": 20.0,
		"observed_at":   runStart.Add(30 * time.Second),
		"note":          "backfilled observation",
	}, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(obsResponse, 200, "append backfilled observation"); err != nil {
		return err
	}
	stillEarly, err := advanceStepRaw(api, runID, 1, nil)
	if err != nil {
		return err
	}
	if err := requireCode(stillEarly, 409, string(domain.CodeRunState),
		"observation must not satisfy step hold"); err != nil {
		return err
	}

	// Later steps cannot jump ahead of the current step.
	skipping, err := advanceStepRaw(api, runID, 2, nil)
	if err != nil {
		return err
	}
	if err := requireCode(skipping, 409, string(domain.CodeRunState),
		"skip ahead to a later step"); err != nil {
		return err
	}

	// A genuine stale aggregate version is still rejected on a new fact.
	fixedClock.Advance(61 * time.Minute)
	staleVersion := int64(1)
	stale, err := advanceStepRaw(api, runID, 1, &staleVersion)
	if err != nil {
		return err
	}
	if err := requireCode(stale, 412, string(domain.CodeRevisionConflict),
		"confirm current step with a stale version"); err != nil {
		return err
	}

	// Confirm step 1 for real.
	first, err := confirmStep(api, runID, 1, nil)
	if err != nil {
		return err
	}
	if first.CurrentStep != 2 || len(first.StepProgress) != 3 {
		return errInvalidWorkflow("confirming step 1 did not move to step 2")
	}
	firstRecord := first.StepProgress[0]
	secondStart := first.StepProgress[1].StartedAt
	if firstRecord.StartedAt == nil || !firstRecord.StartedAt.Equal(runStart) {
		return errInvalidWorkflow("step 1 start time was not the run start")
	}
	if firstRecord.CompletedAt == nil || secondStart == nil ||
		!secondStart.Equal(*firstRecord.CompletedAt) {
		return errInvalidWorkflow("step 2 did not start when step 1 completed")
	}

	// Replaying step 1 with the same stale header is idempotent: the recorded
	// fact comes back unchanged, without a conflict, version bump, or write.
	replay, err := confirmStep(api, runID, 1, &staleVersion)
	if err != nil {
		return err
	}
	if replay.Version != first.Version {
		return errInvalidWorkflow("idempotent replay advanced the run version")
	}
	if replay.CurrentStep != 2 ||
		replay.StepProgress[0].CompletedAt == nil ||
		!replay.StepProgress[0].CompletedAt.Equal(*firstRecord.CompletedAt) {
		return errInvalidWorkflow("idempotent replay rewrote the recorded fact")
	}

	// Two terminals confirming the same next step concurrently record one
	// fact exactly once and both read it back.
	fixedClock.Advance(31 * time.Minute)
	results := make(chan runView, 2)
	var wg sync.WaitGroup
	for index := 0; index < 2; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			view, confirmErr := confirmStep(api, runID, 2, nil)
			if confirmErr != nil {
				results <- runView{ID: ""}
				return
			}
			results <- view
		}()
	}
	wg.Wait()
	close(results)
	var concurrent []runView
	for view := range results {
		if view.ID == "" {
			return errInvalidWorkflow("a concurrent confirmation failed")
		}
		concurrent = append(concurrent, view)
	}
	for _, view := range concurrent {
		if view.Version != first.Version+1 || view.CurrentStep != 3 {
			return errInvalidWorkflow("concurrent confirmations did not produce one fact")
		}
		if view.StepProgress[1].CompletedAt == nil ||
			!view.StepProgress[1].CompletedAt.Equal(*concurrent[0].StepProgress[1].CompletedAt) {
			return errInvalidWorkflow("concurrent confirmations recorded different times")
		}
	}

	// With only two of three steps complete, conditioning and completion are
	// forbidden even though the observation and hold requirements are met.
	conditioning, err := api.call("POST", "/v1/runs/"+runID+"/conditioning", nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(conditioning, 409, string(domain.CodeRunState),
		"conditioning with an open step"); err != nil {
		return err
	}
	complete, err := api.call("POST", "/v1/runs/"+runID+"/complete", nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(complete, 409, string(domain.CodeRunState),
		"completing with an open step"); err != nil {
		return err
	}

	// Unknown sequence numbers are rejected without any change.
	unknown, err := advanceStepRaw(api, runID, 99, nil)
	if err != nil {
		return err
	}
	if err := requireCode(unknown, 409, string(domain.CodeRunState),
		"confirm an unknown step"); err != nil {
		return err
	}

	// Aborting locks step advancement; completed runs are covered through the
	// durability scenario below.
	abortResponse, err := api.call("POST", "/v1/runs/"+runID+"/abort", nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(abortResponse, 200, "abort partially advanced run"); err != nil {
		return err
	}
	afterAbort, err := advanceStepRaw(api, runID, 3, nil)
	if err != nil {
		return err
	}
	if err := requireCode(afterAbort, 409, string(domain.CodeRunState),
		"advance step after abort"); err != nil {
		return err
	}
	return nil
}

// verifyStepDurability drives a run through its final step across a controller
// restart and confirms the persisted facts survive unchanged.
func verifyStepDurability(
	api *client,
	fixedClock *clock.Fixed,
	dataDir string,
	formulaID string,
) error {
	vessel, err := createCheckVessel(api, "VSL-304", 300)
	if err != nil {
		return err
	}
	started, err := createStartedRunAt(api, formulaID, vessel.ID, "RUN-STEPS-RESTART",
		fixedClock.Advance, false)
	if err != nil {
		return err
	}
	runID := started.Run.ID
	runStart := *started.Run.StartedAt

	fixedClock.Advance(61 * time.Minute)
	first, err := confirmStep(api, runID, 1, nil)
	if err != nil {
		return err
	}
	fixedClock.Advance(31 * time.Minute)
	second, err := confirmStep(api, runID, 2, nil)
	if err != nil {
		return err
	}

	// Restart with two of three steps complete: same current step, same times.
	reopened := reopenCheckAPI(dataDir, fixedClock)
	defer reopened.Close()

	read, err := reopened.getRun(runID)
	if err != nil {
		return err
	}
	if read.State != domain.RunFermenting || read.CurrentStep != 3 {
		return errInvalidWorkflow("restart lost the single current step")
	}
	if len(read.StepProgress) != 3 ||
		read.StepProgress[0].StartedAt == nil ||
		!read.StepProgress[0].StartedAt.Equal(runStart) ||
		read.StepProgress[0].CompletedAt == nil ||
		!read.StepProgress[0].CompletedAt.Equal(*first.StepProgress[0].CompletedAt) ||
		read.StepProgress[1].CompletedAt == nil ||
		!read.StepProgress[1].CompletedAt.Equal(*second.StepProgress[1].CompletedAt) ||
		read.StepProgress[2].StartedAt == nil ||
		!read.StepProgress[2].StartedAt.Equal(*second.StepProgress[2].StartedAt) {
		return errInvalidWorkflow("restart altered recorded step times")
	}

	// Hold timing is still enforced against the persisted step start.
	blocked, err := reopened.advance(runID, 3, nil)
	if err != nil {
		return err
	}
	if err := requireCode(blocked, 409, string(domain.CodeRunState),
		"confirm final step before its hold after restart"); err != nil {
		return err
	}
	blockedConditioning, err := reopened.call("POST",
		"/v1/runs/"+runID+"/conditioning", nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(blockedConditioning, 409, string(domain.CodeRunState),
		"conditioning with an open step after restart"); err != nil {
		return err
	}

	// Finish the final step; all steps are now real facts.
	fixedClock.Advance(241 * time.Minute)
	final, err := reopened.confirm(runID, 3, nil)
	if err != nil {
		return err
	}
	if final.CurrentStep != 4 || !final.StepsAreComplete() {
		return errInvalidWorkflow("final step did not complete the recipe")
	}

	// The formula requires observations for conditioning/completion; append
	// three valid points (the restart path accepts them normally).
	base := runStart.Add(3 * time.Minute)
	points := []float64{1.050, 1.028, 1.014}
	for index, gravity := range points {
		obs, err := reopened.call("POST", "/v1/runs/"+runID+"/observations", map[string]any{
			"sequence":      index + 1,
			"gravity":       gravity,
			"temperature_c": 20.0,
			"observed_at":   base.Add(time.Duration(index) * time.Minute),
		}, nil)
		if err != nil {
			return err
		}
		if err := requireStatus(obs, 200, "append restart-scenario observation"); err != nil {
			return err
		}
	}

	conditioningResponse, err := reopened.call("POST",
		"/v1/runs/"+runID+"/conditioning", nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(conditioningResponse, 200, "enter conditioning after all steps"); err != nil {
		return err
	}
	var conditioningView runView
	if err := conditioningResponse.decode(&conditioningView); err != nil {
		return err
	}
	if err := requireRunState(conditioningView, domain.RunConditioning); err != nil {
		return err
	}
	completeResponse, err := reopened.call("POST",
		"/v1/runs/"+runID+"/complete", nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(completeResponse, 200, "complete run after all steps"); err != nil {
		return err
	}
	var completed pairView
	if err := completeResponse.decode(&completed); err != nil {
		return err
	}
	if err := requireRunState(completed.Run, domain.RunCompleted); err != nil {
		return err
	}
	if err := requireVesselState(completed.Vessel, domain.VesselCleaning); err != nil {
		return err
	}

	// A completed run can no longer advance steps, even by replay.
	afterComplete, err := reopened.advance(runID, 3, nil)
	if err != nil {
		return err
	}
	if err := requireCode(afterComplete, 409, string(domain.CodeRunState),
		"advance step after completion"); err != nil {
		return err
	}

	// One more restart: the completed run keeps every step timestamp.
	reopened.Close()
	finalAPI := reopenCheckAPI(dataDir, fixedClock)
	defer finalAPI.Close()
	readBack, err := finalAPI.getRun(runID)
	if err != nil {
		return err
	}
	if readBack.State != domain.RunCompleted || readBack.CurrentStep != 4 {
		return errInvalidWorkflow("completed run changed after final restart")
	}
	for index, step := range readBack.StepProgress {
		if step.StartedAt == nil || step.CompletedAt == nil {
			return fmt.Errorf("completed run step %d lost times after restart", index+1)
		}
	}
	return nil
}

func createStepFormula(api *client, name string) (formulaView, error) {
	created, err := api.call("POST", "/v1/formulas", createFormulaPayload(name), nil)
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(created, 201, "create step formula"); err != nil {
		return formulaView{}, err
	}
	var formula formulaView
	if err := created.decode(&formula); err != nil {
		return formulaView{}, err
	}
	steps, err := api.call("PUT", "/v1/formulas/"+formula.ID+"/steps",
		map[string]any{
			"steps": []map[string]any{
				{"sequence": 1, "label": "Warm pitch", "temperature_c": 20.0, "hold_minutes": 60},
				{"sequence": 2, "label": "Primary hold", "temperature_c": 19.0, "hold_minutes": 30},
				{"sequence": 3, "label": "Slow finish", "temperature_c": 18.0, "hold_minutes": 240},
			},
		}, ptrInt64(1))
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(steps, 200, "set step formula steps"); err != nil {
		return formulaView{}, err
	}
	approved, err := api.call("POST", "/v1/formulas/"+formula.ID+"/approve",
		nil, ptrInt64(2))
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(approved, 200, "approve step formula"); err != nil {
		return formulaView{}, err
	}
	if err := approved.decode(&formula); err != nil {
		return formulaView{}, err
	}
	return formula, nil
}

type checkAPI struct {
	store  *storage.Store
	server *httptest.Server
	client *client
}

func reopenCheckAPI(dataDir string, fixedClock *clock.Fixed) *checkAPI {
	store, err := storage.Open(dataDir)
	if err != nil {
		panic(err)
	}
	app := service.New(store, fixedClock)
	server := httptest.NewServer(httpapi.NewHandler(app))
	return &checkAPI{store: store, server: server, client: newClient(server.URL)}
}

func (c *checkAPI) Close() {
	c.server.Close()
}

func (c *checkAPI) call(method, path string, payload any, version *int64) (response, error) {
	return c.client.call(method, path, payload, version)
}

func (c *checkAPI) getRun(runID string) (runView, error) {
	response, err := c.call("GET", "/v1/runs/"+runID, nil, nil)
	if err != nil {
		return runView{}, err
	}
	if err := requireStatus(response, 200, "read run"); err != nil {
		return runView{}, err
	}
	var view runView
	if err := response.decode(&view); err != nil {
		return runView{}, err
	}
	return view, nil
}

func (c *checkAPI) advance(runID string, sequence int, version *int64) (response, error) {
	return c.call("POST", "/v1/runs/"+runID+"/steps/advance",
		map[string]any{"sequence": sequence}, version)
}

func (c *checkAPI) confirm(runID string, sequence int, version *int64) (runView, error) {
	response, err := c.advance(runID, sequence, version)
	if err != nil {
		return runView{}, err
	}
	if err := requireStatus(response, 200, "confirm formula step"); err != nil {
		return runView{}, err
	}
	var view runView
	if err := response.decode(&view); err != nil {
		return runView{}, err
	}
	return view, nil
}

func advanceStepRaw(api *client, runID string, sequence int, version *int64) (response, error) {
	return api.call("POST", "/v1/runs/"+runID+"/steps/advance",
		map[string]any{"sequence": sequence}, version)
}

func confirmStep(api *client, runID string, sequence int, version *int64) (runView, error) {
	response, err := advanceStepRaw(api, runID, sequence, version)
	if err != nil {
		return runView{}, err
	}
	if err := requireStatus(response, 200, "confirm formula step"); err != nil {
		return runView{}, err
	}
	var view runView
	if err := response.decode(&view); err != nil {
		return runView{}, err
	}
	return view, nil
}

func (r runView) StepsAreComplete() bool {
	for _, step := range r.StepProgress {
		if step.CompletedAt == nil {
			return false
		}
	}
	return len(r.StepProgress) > 0
}
