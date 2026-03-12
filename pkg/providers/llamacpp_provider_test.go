package providers

import (
	"testing"
)

func TestBuildChatMLPrompt(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "Sos un asistente útil."},
		{Role: "user", Content: "Hola, ¿cómo estás?"},
	}

	prompt := buildChatMLPrompt(messages)

	expected := "<|im_start|>system\nSos un asistente útil.<|im_end|>\n<|im_start|>user\nHola, ¿cómo estás?<|im_end|>\n<|im_start|>assistant\n"
	if prompt != expected {
		t.Errorf("ChatML prompt mismatch.\nGot:\n%s\nExpected:\n%s", prompt, expected)
	}
}

func TestBuildChatMLPromptMultimodal(t *testing.T) {
	messages := []Message{
		{Role: "user", Parts: []ContentPart{
			{Type: "text", Text: "¿Qué ves en la imagen?"},
			{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,abc"}},
		}},
	}

	prompt := buildChatMLPrompt(messages)

	// Should extract text, skip images
	if !contains(prompt, "¿Qué ves en la imagen?") {
		t.Error("Should extract text from multimodal parts")
	}
	if contains(prompt, "base64") {
		t.Error("Should not include image data in ChatML prompt")
	}
}

func TestStripSpecialTokens(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello world<|im_end|>", "Hello world"},
		{"Response<|endoftext|>\n", "Response"},
		{"Clean text", "Clean text"},
		{"<|im_start|>assistant\nHi<|im_end|>", "assistant\nHi"},
	}

	for _, tt := range tests {
		got := stripSpecialTokens(tt.input)
		if got != tt.expected {
			t.Errorf("stripSpecialTokens(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	if n := estimateTokens(""); n != 0 {
		t.Errorf("empty string should be 0 tokens, got %d", n)
	}
	if n := estimateTokens("hi"); n != 1 {
		t.Errorf("short string should be at least 1 token, got %d", n)
	}
	if n := estimateTokens("Esta es una oración de prueba para estimar tokens"); n < 10 {
		t.Errorf("expected >10 estimated tokens, got %d", n)
	}
}

func TestMaxTokensDefault(t *testing.T) {
	p := &LlamaCppProvider{}
	if p.maxTokens() != 512 {
		t.Errorf("default maxTokens should be 512, got %d", p.maxTokens())
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
