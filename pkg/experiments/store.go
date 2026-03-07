package experiments

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// Hypothesis represents a behavioral experiment.
type Hypothesis struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Category    string `json:"category"`               // tone, memory, tool_usage, proactivity
	Adjustment  string `json:"adjustment"`              // behavioral instruction to inject
	Criteria    string `json:"criteria"`                // how to measure success
	Status      string `json:"status"`                  // active, accepted, rejected, expired
	CreatedAt   string `json:"created_at"`
	EvaluatedAt string `json:"evaluated_at,omitempty"`
	CycleCount  int    `json:"cycle_count"`
	MaxCycles   int    `json:"max_cycles"` // default 4 (~1 month of weekly evaluations)
}

// Store manages behavioral experiments.
type Store struct {
	filePath   string
	hypotheses []Hypothesis
	mu         sync.RWMutex
}

const maxActiveHypotheses = 3

// NewStore creates a new experiments Store.
func NewStore(workspace string) *Store {
	stateDir := filepath.Join(workspace, "state")
	os.MkdirAll(stateDir, 0755)

	s := &Store{
		filePath: filepath.Join(stateDir, "experiments.json"),
	}
	s.load()
	return s
}

// load reads experiments from disk.
func (s *Store) load() {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		s.hypotheses = nil
		return
	}
	var hyps []Hypothesis
	if err := json.Unmarshal(data, &hyps); err != nil {
		logger.WarnCF("experiments", "Failed to parse experiments.json",
			map[string]interface{}{"error": err.Error()})
		s.hypotheses = nil
		return
	}
	s.hypotheses = hyps
}

// save writes experiments to disk atomically.
func (s *Store) save() error {
	data, err := json.MarshalIndent(s.hypotheses, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := s.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, s.filePath)
}

// GetActive returns all active hypotheses.
func (s *Store) GetActive() []Hypothesis {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var active []Hypothesis
	for _, h := range s.hypotheses {
		if h.Status == "active" {
			active = append(active, h)
		}
	}
	return active
}

// GetAll returns all hypotheses.
func (s *Store) GetAll() []Hypothesis {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Hypothesis, len(s.hypotheses))
	copy(result, s.hypotheses)
	return result
}

// Add adds a new hypothesis if the active limit isn't reached.
func (s *Store) Add(h Hypothesis) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Count active
	active := 0
	for _, existing := range s.hypotheses {
		if existing.Status == "active" {
			active++
		}
	}
	if active >= maxActiveHypotheses {
		return ErrMaxActive
	}

	if h.MaxCycles <= 0 {
		h.MaxCycles = 4
	}
	if h.Status == "" {
		h.Status = "active"
	}
	if h.CreatedAt == "" {
		h.CreatedAt = time.Now().Format(time.RFC3339)
	}

	s.hypotheses = append(s.hypotheses, h)
	return s.save()
}

// Update updates a hypothesis by ID.
func (s *Store) Update(id string, status string, evaluatedAt string, cycleCount int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.hypotheses {
		if s.hypotheses[i].ID == id {
			if status != "" {
				s.hypotheses[i].Status = status
			}
			if evaluatedAt != "" {
				s.hypotheses[i].EvaluatedAt = evaluatedAt
			}
			if cycleCount >= 0 {
				s.hypotheses[i].CycleCount = cycleCount
			}
			return s.save()
		}
	}
	return ErrNotFound
}

// ExpireOld marks hypotheses as expired if they've exceeded their MaxCycles.
func (s *Store) ExpireOld() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	expired := 0
	for i := range s.hypotheses {
		if s.hypotheses[i].Status == "active" && s.hypotheses[i].CycleCount >= s.hypotheses[i].MaxCycles {
			s.hypotheses[i].Status = "expired"
			s.hypotheses[i].EvaluatedAt = time.Now().Format(time.RFC3339)
			expired++
		}
	}
	if expired > 0 {
		s.save()
	}
	return expired
}

// BuildAdjustmentsPrompt returns a prompt section with active behavioral adjustments.
func (s *Store) BuildAdjustmentsPrompt() string {
	active := s.GetActive()
	if len(active) == 0 {
		return ""
	}

	result := "## Active Behavioral Adjustments\n\n"
	result += "These adjustments are being tested. Follow them as guidelines:\n\n"
	for i, h := range active {
		result += fmt.Sprintf("%d. **%s** (%s): %s\n", i+1, h.Title, h.Category, h.Adjustment)
	}
	result += "\nIf the user objects to any behavior, note it for the next evaluation cycle.\n"
	return result
}

// Errors
var (
	ErrMaxActive = &storeError{"maximum active hypotheses reached (3)"}
	ErrNotFound  = &storeError{"hypothesis not found"}
)

type storeError struct {
	msg string
}

func (e *storeError) Error() string {
	return e.msg
}
