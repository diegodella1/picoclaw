package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/skills"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type ContextBuilder struct {
	workspace    string
	skillsLoader *skills.SkillsLoader
	memory       *MemoryStore
	tools        *tools.ToolRegistry // Direct reference to tool registry
	model        string
}

func getGlobalConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".picoclaw")
}

func NewContextBuilder(workspace string) *ContextBuilder {
	// builtin skills: skills directory in current project
	// Use the skills/ directory under the current working directory
	wd, _ := os.Getwd()
	builtinSkillsDir := filepath.Join(wd, "skills")
	globalSkillsDir := filepath.Join(getGlobalConfigDir(), "skills")

	return &ContextBuilder{
		workspace:    workspace,
		skillsLoader: skills.NewSkillsLoader(workspace, globalSkillsDir, builtinSkillsDir),
		memory:       NewMemoryStore(workspace),
	}
}

// SetToolsRegistry sets the tools registry for dynamic tool summary generation.
func (cb *ContextBuilder) SetToolsRegistry(registry *tools.ToolRegistry) {
	cb.tools = registry
}

// SetModel sets the current model name for inclusion in the system prompt.
func (cb *ContextBuilder) SetModel(model string) {
	cb.model = model
}

func (cb *ContextBuilder) getIdentity() string {
	now := time.Now().Format("2006-01-02 15:04 (Monday)")
	workspacePath, _ := filepath.Abs(filepath.Join(cb.workspace))
	runtime := fmt.Sprintf("%s %s, Go %s", runtime.GOOS, runtime.GOARCH, runtime.Version())

	// Build tools section dynamically
	toolsSection := cb.buildToolsSection()

	return fmt.Sprintf(`# picoclaw 🦞

You are picoclaw, a helpful AI assistant.

## Current Time
%s

## Model
%s

## Runtime
%s

## Workspace
Your workspace is at: %s
- Memory: %s/memory/MEMORY.md
- Daily Notes: %s/memory/YYYYMM/YYYYMMDD.md
- Skills: %s/skills/{skill-name}/SKILL.md

%s

## Important Rules

1. **ALWAYS use tools** - When you need to perform an action (schedule reminders, send messages, execute commands, etc.), you MUST call the appropriate tool. Do NOT just say you'll do it or pretend to do it.

2. **Be helpful and accurate** - When using tools, briefly explain what you're doing.

3. **Memory** - When remembering something, write to %s/memory/MEMORY.md`,
		now, cb.model, runtime, workspacePath, workspacePath, workspacePath, workspacePath, toolsSection, workspacePath)
}

func (cb *ContextBuilder) buildToolsSection() string {
	if cb.tools == nil {
		return ""
	}

	summaries := cb.tools.GetSummaries()
	if len(summaries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Available Tools\n\n")
	sb.WriteString("**CRITICAL**: You MUST use tools to perform actions. Do NOT pretend to execute commands or schedule tasks.\n\n")
	sb.WriteString("You have access to the following tools:\n\n")
	for _, s := range summaries {
		sb.WriteString(s)
		sb.WriteString("\n")
	}

	return sb.String()
}

func (cb *ContextBuilder) BuildSystemPrompt() string {
	parts := []string{}

	// Core identity section
	parts = append(parts, cb.getIdentity())

	// Bootstrap files
	bootstrapContent := cb.LoadBootstrapFiles()
	if bootstrapContent != "" {
		parts = append(parts, bootstrapContent)
	}

	// Skills - show summary, AI can read full content with read_file tool
	skillsSummary := cb.skillsLoader.BuildSkillsSummary()
	if skillsSummary != "" {
		parts = append(parts, fmt.Sprintf(`# Skills

The following skills extend your capabilities. To use a skill, read its SKILL.md file using the read_file tool.

%s`, skillsSummary))
	}

	// Memory context
	memoryContext := cb.memory.GetMemoryContext()
	if memoryContext != "" {
		parts = append(parts, "# Memory\n\n"+memoryContext)
	}

	// Join with "---" separator
	return strings.Join(parts, "\n\n---\n\n")
}

func (cb *ContextBuilder) LoadBootstrapFiles() string {
	bootstrapFiles := []string{
		"AGENTS.md",
		"SOUL.md",
		"USER.md",
		"IDENTITY.md",
	}

	var result string
	for _, filename := range bootstrapFiles {
		filePath := filepath.Join(cb.workspace, filename)
		if data, err := os.ReadFile(filePath); err == nil {
			result += fmt.Sprintf("## %s\n\n%s\n\n", filename, string(data))
		}
	}

	return result
}

func (cb *ContextBuilder) BuildMessages(history []providers.Message, summary string, currentMessage string, media []string, channel, chatID string) []providers.Message {
	messages := []providers.Message{}

	systemPrompt := cb.BuildSystemPrompt()

	// Add Current Session info if provided
	if channel != "" && chatID != "" {
		systemPrompt += fmt.Sprintf("\n\n## Current Session\nChannel: %s\nChat ID: %s", channel, chatID)
	}

	// Log system prompt summary for debugging (debug mode only)
	logger.DebugCF("agent", "System prompt built",
		map[string]interface{}{
			"total_chars":   len(systemPrompt),
			"total_lines":   strings.Count(systemPrompt, "\n") + 1,
			"section_count": strings.Count(systemPrompt, "\n\n---\n\n") + 1,
		})

	// Log preview of system prompt (avoid logging huge content)
	preview := systemPrompt
	if len(preview) > 500 {
		preview = preview[:500] + "... (truncated)"
	}
	logger.DebugCF("agent", "System prompt preview",
		map[string]interface{}{
			"preview": preview,
		})

	if summary != "" {
		systemPrompt += "\n\n## Summary of Previous Conversation\n\n" + summary
	}

	// Sanitize history: ensure every assistant with tool_calls has matching tool responses,
	// and every tool message follows its corresponding assistant message.
	history = sanitizeHistory(history)

	messages = append(messages, providers.Message{
		Role:    "system",
		Content: systemPrompt,
	})

	messages = append(messages, history...)

	// Build user message — multimodal if media is present
	userMsg := providers.Message{
		Role:    "user",
		Content: currentMessage,
	}

	if len(media) > 0 {
		// Build multimodal content parts
		parts := []providers.ContentPart{
			{Type: "text", Text: currentMessage},
		}
		for _, dataURI := range media {
			if strings.HasPrefix(dataURI, "data:image/") {
				parts = append(parts, providers.ContentPart{
					Type: "image_url",
					ImageURL: &providers.ImageURL{
						URL:    dataURI,
						Detail: "auto",
					},
				})
			}
		}
		userMsg.Parts = parts
	}

	messages = append(messages, userMsg)

	return messages
}

// GetSkillsInfo returns information about loaded skills.
func (cb *ContextBuilder) GetSkillsInfo() map[string]interface{} {
	allSkills := cb.skillsLoader.ListSkills()
	skillNames := make([]string, 0, len(allSkills))
	for _, s := range allSkills {
		skillNames = append(skillNames, s.Name)
	}
	return map[string]interface{}{
		"total": len(allSkills),
		"names": skillNames,
	}
}

// sanitizeHistory ensures the message history is valid for OpenAI-compatible APIs:
// 1. Every assistant message with tool_calls must be followed by tool messages for ALL tool_call IDs
// 2. Every tool message must follow an assistant message that requested it
// 3. No orphaned tool messages at the start or middle of history
func sanitizeHistory(history []providers.Message) []providers.Message {
	if len(history) == 0 {
		return history
	}

	result := make([]providers.Message, 0, len(history))
	i := 0

	for i < len(history) {
		msg := history[i]

		// Skip orphaned tool messages (no preceding assistant with matching tool_calls)
		if msg.Role == "tool" {
			logger.DebugCF("agent", "Removing orphaned tool message from history",
				map[string]interface{}{"tool_call_id": msg.ToolCallID, "index": i})
			i++
			continue
		}

		// For assistant messages with tool_calls, validate the complete block
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			// Collect expected tool_call IDs
			expectedIDs := make(map[string]bool)
			for _, tc := range msg.ToolCalls {
				expectedIDs[tc.ID] = false // false = not yet found
			}

			// Look ahead for matching tool response messages
			j := i + 1
			for j < len(history) && history[j].Role == "tool" {
				if _, ok := expectedIDs[history[j].ToolCallID]; ok {
					expectedIDs[history[j].ToolCallID] = true // mark as found
				}
				j++
			}

			// Check if ALL tool_calls have responses
			allFound := true
			for _, found := range expectedIDs {
				if !found {
					allFound = false
					break
				}
			}

			if allFound {
				// Valid block: add assistant + all tool responses
				result = append(result, msg)
				for k := i + 1; k < j; k++ {
					result = append(result, history[k])
				}
				i = j
			} else {
				// Incomplete block: skip assistant and its tool responses entirely
				logger.DebugCF("agent", "Removing incomplete tool call block from history",
					map[string]interface{}{"tool_calls": len(msg.ToolCalls), "index": i})
				i = j
			}
			continue
		}

		// Normal user/assistant message without tool_calls: keep as-is
		result = append(result, msg)
		i++
	}

	return result
}
