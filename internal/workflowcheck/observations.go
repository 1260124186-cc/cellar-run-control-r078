package workflowcheck

import (
	"fmt"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

func observationsCompletion(api *client, advance func(time.Duration)) error {
	formula, err := ensureApprovedFormula(api, "Completion Formula")
	if err != nil {
		return err
	}
	vessel, err := createCheckVessel(api, "VSL-202", 300)
	if err != nil {
		return err
	}
	started, err := createStartedRun(
		api,
		formula.ID,
		vessel.ID,
		"RUN-OBSERVATIONS",
		advance,
	)
	if err != nil {
		return err
	}

	base := time.Now().UTC().Add(10 * time.Second)
	points := []struct {
		gravity     float64
		temperature float64
	}{
		{gravity: 1.050, temperature: 20.0},
		{gravity: 1.028, temperature: 20.5},
		{gravity: 1.014, temperature: 19.8},
	}
	version := started.Run.Version
	for index, point := range points {
		sequence := index + 1
		response, err := api.call("POST", "/v1/runs/"+started.Run.ID+"/observations",
			map[string]any{
				"sequence":      sequence,
				"gravity":       point.gravity,
				"temperature_c": point.temperature,
				"observed_at":   verifiedAt(base, sequence),
				"note":          fmt.Sprintf("observation %d", sequence),
			}, ptrInt64(version))
		if err != nil {
			return err
		}
		if err := requireStatus(response, 200, "append observation"); err != nil {
			return err
		}
		var run runView
		if err := response.decode(&run); err != nil {
			return err
		}
		version = run.Version
	}

	invalidRange, err := api.call("POST", "/v1/runs/"+started.Run.ID+"/observations",
		map[string]any{
			"sequence":      4,
			"gravity":       1.4,
			"temperature_c": 20,
			"observed_at":   verifiedAt(base, 4),
			"note":          "invalid range",
		}, ptrInt64(version))
	if err != nil {
		return err
	}
	if err := requireCode(invalidRange, 400, string(domain.CodeInvalidInput),
		"reject out-of-range gravity"); err != nil {
		return err
	}

	nonMonotonic, err := api.call("POST", "/v1/runs/"+started.Run.ID+"/observations",
		map[string]any{
			"sequence":      4,
			"gravity":       1.012,
			"temperature_c": 20,
			"observed_at":   verifiedAt(base, 3),
			"note":          "repeated time",
		}, ptrInt64(version))
	if err != nil {
		return err
	}
	if err := requireCode(nonMonotonic, 409, string(domain.CodeObservationConflict),
		"reject non-monotonic observation"); err != nil {
		return err
	}

	conditioningResponse, err := api.call("POST",
		"/v1/runs/"+started.Run.ID+"/conditioning", nil, ptrInt64(version))
	if err != nil {
		return err
	}
	if err := requireStatus(conditioningResponse, 200, "begin conditioning"); err != nil {
		return err
	}
	var conditioning runView
	if err := conditioningResponse.decode(&conditioning); err != nil {
		return err
	}
	if err := requireRunState(conditioning, domain.RunConditioning); err != nil {
		return err
	}

	completeResponse, err := api.call("POST", "/v1/runs/"+started.Run.ID+"/complete",
		nil, ptrInt64(conditioning.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(completeResponse, 200, "complete run"); err != nil {
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
	if completed.Run.Metrics == nil ||
		completed.Run.Metrics.ObservationCount != 3 ||
		completed.Run.Metrics.ApparentAttenuation <= 0 ||
		completed.Run.Metrics.EstimatedAlcoholABV <= 0 {
		return errInvalidWorkflow("completion metrics were not calculated")
	}

	repeatResponse, err := api.call("POST", "/v1/runs/"+started.Run.ID+"/complete",
		nil, ptrInt64(completed.Run.Version))
	if err != nil {
		return err
	}
	if err := requireCode(repeatResponse, 409, string(domain.CodeRunState),
		"complete run twice"); err != nil {
		return err
	}

	readResponse, err := api.call("GET", "/v1/runs/"+started.Run.ID, nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(readResponse, 200, "read completed run"); err != nil {
		return err
	}
	var readBack runView
	if err := readResponse.decode(&readBack); err != nil {
		return err
	}
	if len(readBack.Observations) != 3 || readBack.Metrics == nil {
		return errInvalidWorkflow("completed run lost observations or metrics")
	}
	return nil
}

func createCheckVessel(api *client, code string, capacity float64) (vesselView, error) {
	response, err := api.call("POST", "/v1/vessels", map[string]any{
		"code":            code,
		"capacity_liters": capacity,
	}, nil)
	if err != nil {
		return vesselView{}, err
	}
	if err := requireStatus(response, 201, "create check vessel"); err != nil {
		return vesselView{}, err
	}
	var vessel vesselView
	if err := response.decode(&vessel); err != nil {
		return vesselView{}, err
	}
	return vessel, nil
}

func createStartedRun(
	api *client,
	formulaID string,
	vesselID string,
	code string,
	advance func(time.Duration),
) (pairView, error) {
	runResponse, err := api.call("POST", "/v1/runs",
		createRunPayload(code, formulaID), nil)
	if err != nil {
		return pairView{}, err
	}
	if err := requireStatus(runResponse, 201, "create check run"); err != nil {
		return pairView{}, err
	}
	var run runView
	if err := runResponse.decode(&run); err != nil {
		return pairView{}, err
	}
	reserveResponse, err := api.call("POST", "/v1/runs/"+run.ID+"/reserve",
		map[string]any{"vessel_id": vesselID}, ptrInt64(run.Version))
	if err != nil {
		return pairView{}, err
	}
	if err := requireStatus(reserveResponse, 200, "reserve check vessel"); err != nil {
		return pairView{}, err
	}
	var reserved pairView
	if err := reserveResponse.decode(&reserved); err != nil {
		return pairView{}, err
	}
	startResponse, err := api.call("POST", "/v1/runs/"+run.ID+"/start",
		nil, ptrInt64(reserved.Run.Version))
	if err != nil {
		return pairView{}, err
	}
	if err := requireStatus(startResponse, 200, "start check run"); err != nil {
		return pairView{}, err
	}
	var started pairView
	if err := startResponse.decode(&started); err != nil {
		return pairView{}, err
	}
	version := started.Run.Version
	for sequence := 1; sequence <= 3; sequence++ {
		advance(5 * time.Hour)
		stepResponse, err := api.call(
			"POST",
			"/v1/runs/"+started.Run.ID+"/steps/advance",
			map[string]any{"sequence": sequence},
			ptrInt64(version),
		)
		if err != nil {
			return pairView{}, err
		}
		if err := requireStatus(stepResponse, 200, "advance formula step"); err != nil {
			return pairView{}, err
		}
		var updated runView
		if err := stepResponse.decode(&updated); err != nil {
			return pairView{}, err
		}
		version = updated.Version
	}
	started.Run.Version = version
	return started, nil
}
