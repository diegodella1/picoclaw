package council

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// MemberResponse holds a single council member's response.
type MemberResponse struct {
	Name     string
	Response string
}

// Member represents a single council advisor with its own Telegram bot.
type Member struct {
	Name        string
	Bot         *telego.Bot
	Personality string // loaded system prompt
	Model       string
}

// Council orchestrates deliberation among multiple AI advisors.
type Council struct {
	members     []Member
	groupChatID int64
	provider    providers.LLMProvider
	defaultModel string
}

// NewCouncil creates a Council from config. Each member gets a send-only Telegram bot.
func NewCouncil(cfg config.CouncilConfig, provider providers.LLMProvider, defaultModel, workspace string) (*Council, error) {
	groupID, err := strconv.ParseInt(cfg.GroupID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid group_id %q: %w", cfg.GroupID, err)
	}

	members := make([]Member, 0, len(cfg.Members))
	for _, mc := range cfg.Members {
		bot, err := telego.NewBot(mc.Token, telego.WithDiscardLogger())
		if err != nil {
			logger.ErrorCF("council", "Failed to create bot for member",
				map[string]interface{}{"name": mc.Name, "error": err.Error()})
			continue
		}

		// Load personality from workspace/council/{personality}.md
		personalityPath := filepath.Join(workspace, "council", mc.Personality+".md")
		data, err := os.ReadFile(personalityPath)
		if err != nil {
			// Try embedded fallback — caller should ensure files exist
			logger.ErrorCF("council", "Failed to load personality",
				map[string]interface{}{"name": mc.Name, "path": personalityPath, "error": err.Error()})
			continue
		}

		model := mc.Model
		if model == "" {
			model = defaultModel
		}

		members = append(members, Member{
			Name:        mc.Name,
			Bot:         bot,
			Personality: string(data),
			Model:       model,
		})
	}

	if len(members) == 0 {
		return nil, fmt.Errorf("no council members initialized")
	}

	return &Council{
		members:      members,
		groupChatID:  groupID,
		provider:     provider,
		defaultModel: defaultModel,
	}, nil
}

// Deliberate runs a sequential deliberation: each member sees prior responses.
// Returns all responses in order.
func (c *Council) Deliberate(ctx context.Context, question string) ([]MemberResponse, error) {
	// Own timeout: 3 LLM calls + posting = up to 4 minutes
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	var responses []MemberResponse

	for _, member := range c.members {
		select {
		case <-ctx.Done():
			return responses, ctx.Err()
		default:
		}

		// Build messages for this member
		systemPrompt := member.Personality + "\n\n" +
			"INSTRUCCIONES DE DELIBERACIÓN:\n" +
			"- Respondé en español argentino, máximo 200 palabras.\n" +
			"- Sé directo y conciso. No repitas lo que ya dijeron otros.\n" +
			"- Aportá tu perspectiva única según tu rol."

		userContent := fmt.Sprintf("PREGUNTA: %s", question)
		if len(responses) > 0 {
			userContent += "\n\nRESPUESTAS ANTERIORES:"
			for _, r := range responses {
				userContent += fmt.Sprintf("\n\n**%s**:\n%s", r.Name, r.Response)
			}
		}

		msgs := []providers.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userContent},
		}

		resp, err := c.provider.Chat(ctx, msgs, nil, member.Model, nil)
		if err != nil {
			logger.ErrorCF("council", "LLM call failed for member",
				map[string]interface{}{"name": member.Name, "error": err.Error()})
			responses = append(responses, MemberResponse{
				Name:     member.Name,
				Response: fmt.Sprintf("[Error: %v]", err),
			})
			continue
		}

		response := strings.TrimSpace(resp.Content)
		responses = append(responses, MemberResponse{
			Name:     member.Name,
			Response: response,
		})

		// Post to Telegram group
		c.postToGroup(ctx, member, response)
	}

	return responses, nil
}

// postToGroup sends a member's response to the Telegram group.
func (c *Council) postToGroup(ctx context.Context, member Member, content string) {
	text := fmt.Sprintf("<b>%s</b>\n\n%s", member.Name, content)

	// Telegram message limit is 4096 chars
	if len(text) > 4096 {
		text = text[:4093] + "..."
	}

	msg := tu.Message(tu.ID(c.groupChatID), text)
	msg.ParseMode = telego.ModeHTML

	if _, err := member.Bot.SendMessage(ctx, msg); err != nil {
		logger.ErrorCF("council", "Failed to post to group",
			map[string]interface{}{
				"member": member.Name,
				"error":  err.Error(),
			})
		// Retry without HTML parse mode
		msg.ParseMode = ""
		msg.Text = fmt.Sprintf("%s\n\n%s", member.Name, content)
		if len(msg.Text) > 4096 {
			msg.Text = msg.Text[:4093] + "..."
		}
		member.Bot.SendMessage(ctx, msg)
	}
}
