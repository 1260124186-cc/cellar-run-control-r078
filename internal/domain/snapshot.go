package domain

import "time"

type Snapshot struct {
	Revision    int64                      `json:"revision"`
	UpdatedAt   time.Time                  `json:"updated_at"`
	Formulas    map[string]Formula         `json:"formulas"`
	Vessels     map[string]Vessel          `json:"vessels"`
	Runs        map[string]FermentationRun `json:"runs"`
	NameIndex   map[string]string          `json:"formula_name_index"`
	VesselIndex map[string]string          `json:"vessel_code_index"`
	RunIndex    map[string]string          `json:"run_code_index"`
}

func EmptySnapshot() Snapshot {
	return Snapshot{
		Revision:    0,
		Formulas:    map[string]Formula{},
		Vessels:     map[string]Vessel{},
		Runs:        map[string]FermentationRun{},
		NameIndex:   map[string]string{},
		VesselIndex: map[string]string{},
		RunIndex:    map[string]string{},
	}
}

func (s Snapshot) Clone() Snapshot {
	cloned := EmptySnapshot()
	cloned.Revision = s.Revision
	cloned.UpdatedAt = s.UpdatedAt
	for key, value := range s.Formulas {
		cloned.Formulas[key] = value.Clone()
	}
	for key, value := range s.Vessels {
		cloned.Vessels[key] = value
		if value.ActiveRunID != nil {
			runID := *value.ActiveRunID
			cloned.Vessels[key] = value
			cloned.Vessels[key] = withActiveRun(value, &runID)
		}
	}
	for key, value := range s.Runs {
		cloned.Runs[key] = value.Clone()
	}
	for key, value := range s.NameIndex {
		cloned.NameIndex[key] = value
	}
	for key, value := range s.VesselIndex {
		cloned.VesselIndex[key] = value
	}
	for key, value := range s.RunIndex {
		cloned.RunIndex[key] = value
	}
	return cloned
}

func withActiveRun(v Vessel, runID *string) Vessel {
	value := v
	if runID == nil {
		value.ActiveRunID = nil
		return value
	}
	copied := *runID
	value.ActiveRunID = &copied
	return value
}
