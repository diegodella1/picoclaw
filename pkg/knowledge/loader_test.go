package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func setupTestKnowledge(t *testing.T) (*Loader, string) {
	t.Helper()
	tmpDir := t.TempDir()
	workspace := tmpDir

	// Create knowledge dir with a test topic
	topicDir := filepath.Join(workspace, "knowledge", "historia-antigua")
	os.MkdirAll(topicDir, 0755)

	meta := KnowledgeMeta{
		Slug:        "historia-antigua",
		Title:       "Historia Antigua",
		Description: "Civilizaciones antiguas",
		Keywords:    []string{"historia", "antigua", "roma", "grecia", "egipto"},
		Status:      "ready",
		CreatedAt:   Now(),
		UpdatedAt:   Now(),
		Version:     1,
		CharCount:   100,
		AutoInject:  true,
	}
	data, _ := json.MarshalIndent(meta, "", "  ")
	os.WriteFile(filepath.Join(topicDir, "META.json"), data, 0644)
	os.WriteFile(filepath.Join(topicDir, "KNOWLEDGE.md"), []byte("# Historia Antigua\n\nRoma fue fundada en el 753 a.C."), 0644)

	loader := NewLoader(workspace)
	return loader, workspace
}

func TestRefreshIndex(t *testing.T) {
	loader, _ := setupTestKnowledge(t)
	all := loader.ListAll()
	if len(all) != 1 {
		t.Fatalf("expected 1 topic, got %d", len(all))
	}
	if all[0].Slug != "historia-antigua" {
		t.Fatalf("expected slug 'historia-antigua', got %q", all[0].Slug)
	}
}

func TestFindRelevant_Match(t *testing.T) {
	loader, _ := setupTestKnowledge(t)

	// Should match with 2+ keywords
	results := loader.FindRelevant("contame sobre historia antigua de roma", 2)
	if len(results) == 0 {
		t.Fatal("expected at least 1 result")
	}
	if results[0].Slug != "historia-antigua" {
		t.Fatalf("expected 'historia-antigua', got %q", results[0].Slug)
	}
}

func TestFindRelevant_NoMatch(t *testing.T) {
	loader, _ := setupTestKnowledge(t)

	// Only 1 keyword match — should not return (requires 2+)
	results := loader.FindRelevant("hola como estás", 2)
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestBuildContext(t *testing.T) {
	loader, _ := setupTestKnowledge(t)

	ctx := loader.BuildContext("historia antigua de roma y grecia", 4000)
	if ctx == "" {
		t.Fatal("expected non-empty context")
	}
	if !contains(ctx, "Historia Antigua") {
		t.Fatal("expected context to contain 'Historia Antigua'")
	}
	if !contains(ctx, "Roma fue fundada") {
		t.Fatal("expected context to contain knowledge content")
	}
}

func TestBuildContext_NoMatch(t *testing.T) {
	loader, _ := setupTestKnowledge(t)

	ctx := loader.BuildContext("qué hora es en buenos aires", 4000)
	if ctx != "" {
		t.Fatalf("expected empty context, got %q", ctx)
	}
}

func TestSaveMeta(t *testing.T) {
	loader, workspace := setupTestKnowledge(t)

	meta := KnowledgeMeta{
		Slug:       "golang-patterns",
		Title:      "Go Patterns",
		Keywords:   []string{"golang", "patterns", "concurrency"},
		Status:     "ready",
		AutoInject: true,
	}

	if err := loader.SaveMeta(meta); err != nil {
		t.Fatalf("SaveMeta failed: %v", err)
	}

	// Verify file was created
	path := filepath.Join(workspace, "knowledge", "golang-patterns", "META.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("META.json was not created")
	}
}

func TestLoadContent(t *testing.T) {
	loader, _ := setupTestKnowledge(t)

	content, err := loader.LoadContent("historia-antigua")
	if err != nil {
		t.Fatalf("LoadContent failed: %v", err)
	}
	if !contains(content, "Roma fue fundada") {
		t.Fatalf("unexpected content: %q", content)
	}
}

func TestLoadContent_NotFound(t *testing.T) {
	loader, _ := setupTestKnowledge(t)

	_, err := loader.LoadContent("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent topic")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
