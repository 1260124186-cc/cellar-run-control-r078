package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

const stateFileName = "state.json"

type Store struct {
	mu       sync.RWMutex
	path     string
	snapshot domain.Snapshot
	now      func() time.Time
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	store := &Store{
		path:     filepath.Join(dataDir, stateFileName),
		snapshot: domain.EmptySnapshot(),
		now:      func() time.Time { return time.Now().UTC() },
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	var loaded domain.Snapshot
	if err := json.Unmarshal(raw, &loaded); err != nil {
		return fmt.Errorf("decode state: %w", err)
	}
	normalized, err := normalizeSnapshot(loaded)
	if err != nil {
		return err
	}
	s.snapshot = normalized
	return nil
}

func normalizeSnapshot(value domain.Snapshot) (domain.Snapshot, error) {
	if value.Revision < 0 {
		return domain.Snapshot{}, fmt.Errorf("state revision cannot be negative")
	}
	if value.Formulas == nil {
		value.Formulas = map[string]domain.Formula{}
	}
	if value.Vessels == nil {
		value.Vessels = map[string]domain.Vessel{}
	}
	if value.Runs == nil {
		value.Runs = map[string]domain.FermentationRun{}
	}
	if value.NameIndex == nil {
		value.NameIndex = map[string]string{}
	}
	if value.VesselIndex == nil {
		value.VesselIndex = map[string]string{}
	}
	if value.RunIndex == nil {
		value.RunIndex = map[string]string{}
	}
	if err := validateIndexes(value); err != nil {
		return domain.Snapshot{}, err
	}
	normalizeRunStepProgress(&value)
	return value, nil
}

func normalizeRunStepProgress(value *domain.Snapshot) {
	for id, run := range value.Runs {
		if len(run.StepProgress) == 0 {
			continue
		}
		if run.CurrentStep < 1 || run.CurrentStep > len(run.StepProgress)+1 {
			run.CurrentStep = 1
		}
		if run.CurrentStep <= len(run.StepProgress) {
			step := &run.StepProgress[run.CurrentStep-1]
			if step.CompletedAt == nil && step.StartedAt != nil && !run.UpdatedAt.IsZero() {
				stamp := run.UpdatedAt.UTC()
				step.StartedAt = &stamp
			}
		}
		value.Runs[id] = run
	}
}

func validateIndexes(value domain.Snapshot) error {
	expectedNames := make(map[string]string, len(value.Formulas))
	for id, formula := range value.Formulas {
		key := domain.NameKey(formula.Name)
		if key == "" {
			return fmt.Errorf("formula %s has an empty normalized name", id)
		}
		if existing, found := expectedNames[key]; found && existing != id {
			return fmt.Errorf("formula name index contains a duplicate key")
		}
		expectedNames[key] = id
	}
	if err := compareIndexes("formula name", value.NameIndex, expectedNames); err != nil {
		return err
	}

	expectedVessels := make(map[string]string, len(value.Vessels))
	for id, vessel := range value.Vessels {
		if existing, found := expectedVessels[vessel.Code]; found && existing != id {
			return fmt.Errorf("vessel index contains a duplicate code")
		}
		expectedVessels[vessel.Code] = id
	}
	if err := compareIndexes("vessel code", value.VesselIndex, expectedVessels); err != nil {
		return err
	}

	expectedRuns := make(map[string]string, len(value.Runs))
	for id, run := range value.Runs {
		if existing, found := expectedRuns[run.Code]; found && existing != id {
			return fmt.Errorf("run index contains a duplicate code")
		}
		expectedRuns[run.Code] = id
	}
	return compareIndexes("run code", value.RunIndex, expectedRuns)
}

func compareIndexes(label string, actual, expected map[string]string) error {
	if len(actual) != len(expected) {
		return fmt.Errorf("%s index does not match its collection", label)
	}
	for key, expectedID := range expected {
		actualID, found := actual[key]
		if !found || actualID != expectedID {
			return fmt.Errorf("%s index does not match its collection", label)
		}
	}
	return nil
}

func (s *Store) Revision() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot.Revision
}

func (s *Store) View(visitor func(domain.Snapshot) error) error {
	s.mu.RLock()
	copied := s.snapshot.Clone()
	s.mu.RUnlock()
	return visitor(copied)
}

func (s *Store) Update(expected *int64, mutate func(*domain.Snapshot) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if expected != nil && *expected != s.snapshot.Revision {
		return domain.NewError(domain.CodeRevisionConflict,
			"the supplied revision does not match the current snapshot")
	}
	candidate := s.snapshot.Clone()
	if err := mutate(&candidate); err != nil {
		return err
	}
	candidate.Revision = s.snapshot.Revision + 1
	candidate.UpdatedAt = s.now()
	if err := writeSnapshot(s.path, candidate); err != nil {
		return domain.NewError(domain.CodeStorage, err.Error())
	}
	s.snapshot = candidate
	return nil
}

func (s *Store) Execute(mutator func(*domain.Snapshot) error) error {
	return s.Update(nil, mutator)
}
