package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/council"
)

// CouncilTool wraps the council deliberation engine as an LLM-callable tool.
type CouncilTool struct {
	council        *council.Council
	sendCallback   SendCallback
	defaultChannel string
	defaultChatID  string
}

func NewCouncilTool(c *council.Council) *CouncilTool {
	return &CouncilTool{council: c}
}

func (t *CouncilTool) Name() string {
	return "council"
}

func (t *CouncilTool) Description() string {
	return "Convene the advisory council (3 AI advisors: Skeptic, Creative, Pragmatic) to deliberate on a complex question. " +
		"Each advisor responds sequentially in a Telegram group, seeing previous responses. " +
		"Use this when the user asks '/consejo <question>' or needs multiple perspectives on a decision."
}

func (t *CouncilTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"question": map[string]interface{}{
				"type":        "string",
				"description": "The question or topic for the council to deliberate on",
			},
		},
		"required": []string{"question"},
	}
}

func (t *CouncilTool) SetContext(channel, chatID string) {
	t.defaultChannel = channel
	t.defaultChatID = chatID
}

func (t *CouncilTool) SetSendCallback(callback SendCallback) {
	t.sendCallback = callback
}

func (t *CouncilTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	question, ok := args["question"].(string)
	if !ok || question == "" {
		return ErrorResult("question is required")
	}

	// Notify user that deliberation is starting
	if t.sendCallback != nil && t.defaultChannel != "" && t.defaultChatID != "" {
		t.sendCallback(t.defaultChannel, t.defaultChatID, "Convocando al consejo... 🏛️")
	}

	// Use own timeout (4 min) since the global tool timeout (120s) is too short for 3 LLM calls
	deliberateCtx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	responses, err := t.council.Deliberate(deliberateCtx, question)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Council deliberation failed: %v", err))
	}

	// Format responses for the LLM to synthesize
	var sb strings.Builder
	sb.WriteString("DELIBERACIÓN DEL CONSEJO\n\n")
	sb.WriteString(fmt.Sprintf("Pregunta: %s\n\n", question))
	for _, r := range responses {
		sb.WriteString(fmt.Sprintf("== %s ==\n%s\n\n", r.Name, r.Response))
	}
	sb.WriteString("Sintetizá las 3 perspectivas en tu respuesta al usuario.")

	return &ToolResult{
		ForLLM: sb.String(),
		Silent: true,
	}
}
