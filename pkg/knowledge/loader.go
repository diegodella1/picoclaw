package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// KnowledgeMeta contains metadata for a knowledge topic.
type KnowledgeMeta struct {
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Keywords    []string `json:"keywords"`
	Status      string   `json:"status"`      // "researching" | "ready" | "stale"
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	Version     int      `json:"version"`
	CharCount   int      `json:"char_count"`
	AutoInject  bool     `json:"auto_inject"`
}

// Loader manages knowledge topics stored in workspace/knowledge/.
type Loader struct {
	baseDir string
	index   []KnowledgeMeta
	mu      sync.RWMutex
}

// NewLoader creates a new knowledge Loader.
func NewLoader(workspace string) *Loader {
	baseDir := filepath.Join(workspace, "knowledge")
	os.MkdirAll(baseDir, 0755)
	l := &Loader{baseDir: baseDir}
	l.RefreshIndex()
	return l
}

// RefreshIndex scans the knowledge directory and rebuilds the in-memory index.
func (l *Loader) RefreshIndex() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.index = nil

	entries, err := os.ReadDir(l.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		metaPath := filepath.Join(l.baseDir, entry.Name(), "META.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var meta KnowledgeMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			logger.WarnCF("knowledge", "Invalid META.json",
				map[string]interface{}{"dir": entry.Name(), "error": err.Error()})
			continue
		}
		l.index = append(l.index, meta)
	}

	logger.InfoCF("knowledge", "Knowledge index refreshed",
		map[string]interface{}{"topics": len(l.index)})
	return nil
}

// scoredTopic pairs a topic with its relevance score.
type scoredTopic struct {
	meta  KnowledgeMeta
	score int
}

// FindRelevant returns topics relevant to the given message, ranked by keyword match score.
func (l *Loader) FindRelevant(message string, maxResults int) []KnowledgeMeta {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if len(l.index) == 0 {
		return nil
	}

	words := strings.Fields(strings.ToLower(message))
	wordSet := make(map[string]bool, len(words))
	for _, w := range words {
		// Strip common punctuation
		w = strings.Trim(w, ".,!?¿¡;:\"'()[]{}…")
		if len(w) >= 3 { // Skip very short words
			wordSet[w] = true
		}
	}

	var scored []scoredTopic
	for _, meta := range l.index {
		if meta.Status != "ready" || !meta.AutoInject {
			continue
		}
		score := 0
		for _, kw := range meta.Keywords {
			kwLower := strings.ToLower(kw)
			if wordSet[kwLower] {
				score++
			}
			// Also check if keyword appears as substring in the message
			if score == 0 && strings.Contains(strings.ToLower(message), kwLower) && len(kwLower) >= 4 {
				score++
			}
		}
		if score >= 2 {
			scored = append(scored, scoredTopic{meta: meta, score: score})
		}
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	if maxResults > 0 && len(scored) > maxResults {
		scored = scored[:maxResults]
	}

	results := make([]KnowledgeMeta, len(scored))
	for i, s := range scored {
		results[i] = s.meta
	}
	return results
}

// LoadContent reads the KNOWLEDGE.md file for a given topic slug.
func (l *Loader) LoadContent(slug string) (string, error) {
	path := filepath.Join(l.baseDir, slug, "KNOWLEDGE.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// maxKnowledgeChars is the maximum characters of knowledge context to inject.
const maxKnowledgeChars = 4000

// BuildContext constructs a knowledge context string for injection into the system prompt.
// It finds relevant topics and loads their content, respecting the character budget.
func (l *Loader) BuildContext(message string, budget int) string {
	if budget <= 0 {
		budget = maxKnowledgeChars
	}

	relevant := l.FindRelevant(message, 2)
	if len(relevant) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# Knowledge Context\n\n")
	remaining := budget - sb.Len()

	for _, meta := range relevant {
		content, err := l.LoadContent(meta.Slug)
		if err != nil {
			continue
		}
		// Truncate if needed
		if len(content) > remaining-100 {
			content = content[:remaining-100] + "\n[...truncated]"
		}
		sb.WriteString("## ")
		sb.WriteString(meta.Title)
		sb.WriteString("\n\n")
		sb.WriteString(content)
		sb.WriteString("\n\n")
		remaining = budget - sb.Len()
		if remaining < 200 {
			break
		}
	}

	return sb.String()
}

// ListAll returns all indexed knowledge topics.
func (l *Loader) ListAll() []KnowledgeMeta {
	l.mu.RLock()
	defer l.mu.RUnlock()
	result := make([]KnowledgeMeta, len(l.index))
	copy(result, l.index)
	return result
}

// SaveMeta writes a META.json file for a topic, creating the directory if needed.
func (l *Loader) SaveMeta(meta KnowledgeMeta) error {
	dir := filepath.Join(l.baseDir, meta.Slug)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}

	metaPath := filepath.Join(dir, "META.json")
	tmpPath := metaPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, metaPath)
}

// GetBaseDir returns the base directory for knowledge topics.
func (l *Loader) GetBaseDir() string {
	return l.baseDir
}

// Now returns the current time formatted as RFC3339 (helper for callers).
func Now() string {
	return time.Now().Format(time.RFC3339)
}
