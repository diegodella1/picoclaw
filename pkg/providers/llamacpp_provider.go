package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// LlamaCppProvider supports local LLM inference via llama.cpp.
// Two modes:
//   - "server": connects to a running llama-server (OpenAI-compatible API)
//   - "binary": runs llama-cli as a subprocess for each request
type LlamaCppProvider struct {
	cfg        config.LlamaCppConfig
	httpProv   *HTTPProvider // used in server mode
	defaultMdl string
}

// NewLlamaCppProvider creates a provider based on config mode.
func NewLlamaCppProvider(cfg config.LlamaCppConfig) (*LlamaCppProvider, error) {
	p := &LlamaCppProvider{cfg: cfg}

	switch cfg.Mode {
	case "server":
		if cfg.APIBase == "" {
			return nil, fmt.Errorf("llamacpp server mode requires api_base (e.g. http://localhost:8080/v1)")
		}
		p.httpProv = NewHTTPProvider("", cfg.APIBase, "")
		p.defaultMdl = cfg.DefaultModel
		if p.defaultMdl == "" {
			p.defaultMdl = "qwen2.5-1.5b-instruct"
		}

	case "binary":
		if cfg.BinaryPath == "" {
			return nil, fmt.Errorf("llamacpp binary mode requires binary_path (path to llama-cli)")
		}
		if cfg.ModelPath == "" {
			return nil, fmt.Errorf("llamacpp binary mode requires model_path (path to .gguf file)")
		}
		// Verify binary exists
		if _, err := exec.LookPath(cfg.BinaryPath); err != nil {
			// Try as absolute path
			if _, err2 := exec.LookPath(cfg.BinaryPath); err2 != nil {
				logger.WarnCF("llamacpp", "Binary not found at path, will try at runtime", map[string]interface{}{
					"path": cfg.BinaryPath,
				})
			}
		}
		p.defaultMdl = "local"

	default:
		return nil, fmt.Errorf("llamacpp: invalid mode %q (use \"server\" or \"binary\")", cfg.Mode)
	}

	return p, nil
}

// Chat implements LLMProvider. Routes to server or binary mode.
func (p *LlamaCppProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	if p.cfg.Mode == "server" {
		return p.chatServer(ctx, messages, tools, model, options)
	}
	return p.chatBinary(ctx, messages, tools, options)
}

// chatServer delegates to the HTTP provider (llama-server is OpenAI-compatible).
func (p *LlamaCppProvider) chatServer(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	if model == "" || model == "local" {
		model = p.defaultMdl
	}

	// Small local models: reduce max_tokens to avoid OOM
	if _, ok := options["max_tokens"]; !ok {
		options["max_tokens"] = p.maxTokens()
	}

	resp, err := p.httpProv.Chat(ctx, messages, tools, model, options)
	if err != nil {
		return nil, fmt.Errorf("llamacpp server: %w", err)
	}
	return resp, nil
}

// chatBinary runs llama-cli as a subprocess with a ChatML prompt.
func (p *LlamaCppProvider) chatBinary(ctx context.Context, messages []Message, tools []ToolDefinition, options map[string]interface{}) (*LLMResponse, error) {
	prompt := buildChatMLPrompt(messages)

	maxTok := p.maxTokens()
	if mt, ok := options["max_tokens"].(int); ok && mt > 0 {
		maxTok = mt
	}

	temp := p.cfg.Temperature
	if temp <= 0 {
		if t, ok := options["temperature"].(float64); ok && t > 0 {
			temp = t
		} else {
			temp = 0.7
		}
	}

	threads := p.cfg.Threads
	if threads <= 0 {
		threads = 4 // Pi 5 has 4 cores
	}

	ctxSize := p.cfg.ContextSize
	if ctxSize <= 0 {
		ctxSize = 2048
	}

	args := []string{
		"-m", p.cfg.ModelPath,
		"-p", prompt,
		"-n", fmt.Sprintf("%d", maxTok),
		"-t", fmt.Sprintf("%d", threads),
		"-c", fmt.Sprintf("%d", ctxSize),
		"--temp", fmt.Sprintf("%.2f", temp),
		"--no-display-prompt",
		"--log-disable",
	}

	if p.cfg.GPULayers > 0 {
		args = append(args, "-ngl", fmt.Sprintf("%d", p.cfg.GPULayers))
	}

	// Binary mode timeout: generous for Pi 5 (small models ~10-60s)
	timeout := 120 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	binCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	logger.InfoCF("llamacpp", "Running inference", map[string]interface{}{
		"model":    p.cfg.ModelPath,
		"tokens":   maxTok,
		"threads":  threads,
		"ctx_size": ctxSize,
	})

	cmd := exec.CommandContext(binCtx, p.cfg.BinaryPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	if err := cmd.Run(); err != nil {
		errMsg := stderr.String()
		if binCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("llamacpp binary timed out after %v", timeout)
		}
		return nil, fmt.Errorf("llamacpp binary failed: %w\nstderr: %s", err, errMsg)
	}
	elapsed := time.Since(start)

	output := strings.TrimSpace(stdout.String())

	// Strip any trailing <|im_end|> or <|endoftext|> tokens
	output = stripSpecialTokens(output)

	logger.InfoCF("llamacpp", "Inference complete", map[string]interface{}{
		"elapsed":     elapsed.String(),
		"output_len":  len(output),
		"output_runes": len([]rune(output)),
	})

	return &LLMResponse{
		Content:      output,
		FinishReason: "stop",
		Usage: &UsageInfo{
			CompletionTokens: estimateTokens(output),
			TotalTokens:      estimateTokens(output),
		},
	}, nil
}

// GetDefaultModel implements LLMProvider.
func (p *LlamaCppProvider) GetDefaultModel() string {
	return p.defaultMdl
}

// Ping checks if the llama-server is reachable (server mode only).
func (p *LlamaCppProvider) Ping(ctx context.Context) error {
	if p.cfg.Mode != "server" {
		// Binary mode: check that file exists
		if _, err := exec.LookPath(p.cfg.BinaryPath); err != nil {
			return fmt.Errorf("llama-cli binary not found: %s", p.cfg.BinaryPath)
		}
		return nil
	}

	// Server mode: hit /health endpoint
	healthURL := strings.TrimSuffix(p.cfg.APIBase, "/v1") + "/health"
	resp, err := p.httpProv.httpClient.Get(healthURL)
	if err != nil {
		return fmt.Errorf("llamacpp server unreachable at %s: %w", healthURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("llamacpp server unhealthy (status %d)", resp.StatusCode)
	}
	return nil
}

// buildChatMLPrompt converts messages to ChatML format (Qwen uses this natively).
func buildChatMLPrompt(messages []Message) string {
	var sb strings.Builder
	for _, msg := range messages {
		sb.WriteString("<|im_start|>")
		sb.WriteString(msg.Role)
		sb.WriteString("\n")

		content := msg.Content
		if content == "" && len(msg.Parts) > 0 {
			// Extract text from multimodal parts (skip images for local model)
			for _, part := range msg.Parts {
				if part.Type == "text" && part.Text != "" {
					content += part.Text + "\n"
				}
			}
			content = strings.TrimSpace(content)
		}

		// Append tool call results
		if msg.ToolCallID != "" {
			content = fmt.Sprintf("[Tool Result %s]: %s", msg.ToolCallID, content)
		}

		sb.WriteString(content)
		sb.WriteString("<|im_end|>\n")
	}

	// Prompt the assistant to respond
	sb.WriteString("<|im_start|>assistant\n")
	return sb.String()
}

// stripSpecialTokens removes llama.cpp special tokens from output.
func stripSpecialTokens(s string) string {
	for _, tok := range []string{"<|im_end|>", "<|endoftext|>", "<|im_start|>", "</s>"} {
		s = strings.ReplaceAll(s, tok, "")
	}
	return strings.TrimSpace(s)
}

// estimateTokens gives a rough token count (~4 chars per token for English/Spanish).
func estimateTokens(s string) int {
	n := len([]rune(s)) / 3
	if n == 0 && len(s) > 0 {
		n = 1
	}
	return n
}

// maxTokens returns the configured max or a sensible default for small models.
func (p *LlamaCppProvider) maxTokens() int {
	if p.cfg.MaxTokens > 0 {
		return p.cfg.MaxTokens
	}
	return 512
}

// CreateLlamaCppProvider is the factory used by CreateProvider.
func CreateLlamaCppProvider(cfg *config.Config) (LLMProvider, error) {
	lcfg := cfg.Providers.LlamaCpp
	if !lcfg.Enabled {
		return nil, fmt.Errorf("llamacpp provider is not enabled")
	}

	provider, err := NewLlamaCppProvider(lcfg)
	if err != nil {
		return nil, err
	}

	// Optional: ping on creation to catch misconfig early
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := provider.Ping(ctx); err != nil {
		logger.WarnCF("llamacpp", "Provider created but health check failed", map[string]interface{}{
			"error": err.Error(),
			"mode":  lcfg.Mode,
		})
	}

	return provider, nil
}

// LlamaCppModelInfo returns info about recommended models for display.
func LlamaCppModelInfo() []map[string]string {
	return []map[string]string{
		{
			"name": "Qwen2.5-1.5B-Instruct",
			"file": "qwen2.5-1.5b-instruct-q4_k_m.gguf",
			"size": "~1.0 GB",
			"use":  "Intent routing, short replies, summarization",
			"url":  "huggingface.co/Qwen/Qwen2.5-1.5B-Instruct-GGUF",
		},
		{
			"name": "Qwen2.5-0.5B-Instruct",
			"file": "qwen2.5-0.5b-instruct-q4_k_m.gguf",
			"size": "~400 MB",
			"use":  "Fast intent classification, yes/no, simple extraction",
			"url":  "huggingface.co/Qwen/Qwen2.5-0.5B-Instruct-GGUF",
		},
	}
}

// --- Fallback support ---

// FallbackProvider wraps a primary and local fallback provider.
// If the primary fails (network error, timeout), it falls back to local.
type FallbackProvider struct {
	Primary  LLMProvider
	Fallback LLMProvider
}

func NewFallbackProvider(primary, fallback LLMProvider) *FallbackProvider {
	return &FallbackProvider{Primary: primary, Fallback: fallback}
}

func (f *FallbackProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	resp, err := f.Primary.Chat(ctx, messages, tools, model, options)
	if err == nil {
		return resp, nil
	}

	// Only fallback on network/server errors, not on bad requests
	errStr := err.Error()
	isNetworkError := strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "deadline exceeded") ||
		strings.Contains(errStr, "status 5") ||
		strings.Contains(errStr, "status 429")

	if !isNetworkError {
		return nil, err
	}

	logger.WarnCF("fallback", "Primary provider failed, falling back to local", map[string]interface{}{
		"error": errStr,
	})

	// Strip tools for small local models (unreliable tool calling)
	return f.Fallback.Chat(ctx, messages, nil, "", options)
}

func (f *FallbackProvider) GetDefaultModel() string {
	return f.Primary.GetDefaultModel()
}

// MarshalJSON prevents logging sensitive data.
func (p *LlamaCppProvider) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]string{
		"type": "llamacpp",
		"mode": p.cfg.Mode,
	})
}
