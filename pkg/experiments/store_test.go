package experiments

import (
	"fmt"
	"testing"
)

func setupTestStore(t *testing.T) *Store {
	t.Helper()
	tmpDir := t.TempDir()
	return NewStore(tmpDir)
}

func TestAddAndGetActive(t *testing.T) {
	store := setupTestStore(t)

	h := Hypothesis{
		ID:         "test-1",
		Title:      "Test Hypothesis",
		Category:   "tone",
		Adjustment: "Be more concise",
		Criteria:   "User satisfaction",
		MaxCycles:  4,
	}

	if err := store.Add(h); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	active := store.GetActive()
	if len(active) != 1 {
		t.Fatalf("expected 1 active, got %d", len(active))
	}
	if active[0].ID != "test-1" {
		t.Fatalf("expected ID 'test-1', got %q", active[0].ID)
	}
}

func TestMaxActiveLimit(t *testing.T) {
	store := setupTestStore(t)

	for i := 0; i < 3; i++ {
		err := store.Add(Hypothesis{
			ID:        fmt.Sprintf("h-%d", i),
			Title:     fmt.Sprintf("H%d", i),
			MaxCycles: 4,
		})
		if err != nil {
			t.Fatalf("Add %d failed: %v", i, err)
		}
	}

	// 4th should fail
	err := store.Add(Hypothesis{ID: "h-3", Title: "H3", MaxCycles: 4})
	if err != ErrMaxActive {
		t.Fatalf("expected ErrMaxActive, got %v", err)
	}
}

func TestUpdate(t *testing.T) {
	store := setupTestStore(t)

	store.Add(Hypothesis{ID: "u-1", Title: "U1", MaxCycles: 4})

	err := store.Update("u-1", "accepted", "2026-01-01T00:00:00Z", 3)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	all := store.GetAll()
	if len(all) != 1 || all[0].Status != "accepted" {
		t.Fatalf("expected status 'accepted', got %q", all[0].Status)
	}
}

func TestUpdateNotFound(t *testing.T) {
	store := setupTestStore(t)

	err := store.Update("nonexistent", "rejected", "", -1)
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestExpireOld(t *testing.T) {
	store := setupTestStore(t)

	store.Add(Hypothesis{ID: "e-1", Title: "E1", MaxCycles: 2})
	store.Update("e-1", "", "", 2) // Set cycle count to max

	expired := store.ExpireOld()
	if expired != 1 {
		t.Fatalf("expected 1 expired, got %d", expired)
	}

	active := store.GetActive()
	if len(active) != 0 {
		t.Fatalf("expected 0 active after expiry, got %d", len(active))
	}
}

func TestBuildAdjustmentsPrompt_Empty(t *testing.T) {
	store := setupTestStore(t)

	prompt := store.BuildAdjustmentsPrompt()
	if prompt != "" {
		t.Fatalf("expected empty prompt, got %q", prompt)
	}
}

func TestBuildAdjustmentsPrompt_WithActive(t *testing.T) {
	store := setupTestStore(t)

	store.Add(Hypothesis{
		ID:         "p-1",
		Title:      "Conciseness",
		Category:   "tone",
		Adjustment: "Keep responses under 200 chars when possible",
		MaxCycles:  4,
	})

	prompt := store.BuildAdjustmentsPrompt()
	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
	if !containsStr(prompt, "Conciseness") {
		t.Fatal("expected prompt to contain 'Conciseness'")
	}
	if !containsStr(prompt, "Keep responses under 200 chars") {
		t.Fatal("expected prompt to contain the adjustment text")
	}
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

