package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/logger"
)

type reminder struct {
	ID        string `json:"id"`
	Message   string `json:"message"`
	DueAt     string `json:"due_at"`
	Channel   string `json:"channel"`
	ChatID    string `json:"chat_id"`
	CreatedAt string `json:"created_at"`
	Fired     bool   `json:"fired"`
}

type ReminderTool struct {
	filePath string
	msgBus   *bus.MessageBus
	channel  string
	chatID   string
	mu       sync.Mutex
	nextID   int
	timers   map[string]*time.Timer
}

func NewReminderTool(workspace string, msgBus *bus.MessageBus) *ReminderTool {
	return &ReminderTool{
		filePath: filepath.Join(workspace, "reminders.json"),
		msgBus:   msgBus,
		timers:   make(map[string]*time.Timer),
	}
}

func (t *ReminderTool) Name() string { return "reminder" }

func (t *ReminderTool) Description() string {
	return "Set, list, or cancel reminders. The bot will send you a message when the reminder is due. Use duration strings like '30m', '2h', '1d', '1h30m'."
}

func (t *ReminderTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"set", "list", "cancel"},
				"description": "Action to perform",
			},
			"message": map[string]interface{}{
				"type":        "string",
				"description": "Reminder message (required for set)",
			},
			"duration": map[string]interface{}{
				"type":        "string",
				"description": "Time until reminder fires: '30m', '2h', '1d', '1h30m', etc. (required for set)",
			},
			"id": map[string]interface{}{
				"type":        "string",
				"description": "Reminder ID (required for cancel)",
			},
		},
		"required": []string{"action"},
	}
}

// SetContext implements ContextualTool
func (t *ReminderTool) SetContext(channel, chatID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.channel = channel
	t.chatID = chatID
}

func (t *ReminderTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	action, _ := args["action"].(string)
	switch action {
	case "set":
		return t.set(args)
	case "list":
		return t.list()
	case "cancel":
		return t.cancel(args)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s", action))
	}
}

// StartPendingReminders reloads unfired reminders and schedules them.
// Call this at startup after creating the tool.
func (t *ReminderTool) StartPendingReminders() {
	t.mu.Lock()
	defer t.mu.Unlock()

	reminders := t.loadRemindersLocked()
	maxID := 0

	// Purge fired reminders older than 24h
	cutoff := time.Now().Add(-24 * time.Hour)
	kept := reminders[:0]
	purged := 0
	for _, r := range reminders {
		if r.Fired {
			if firedAt, err := time.Parse(time.RFC3339, r.DueAt); err == nil && firedAt.Before(cutoff) {
				purged++
				continue
			}
		}
		kept = append(kept, r)
	}
	reminders = kept
	if purged > 0 {
		t.saveRemindersLocked(reminders)
		logger.InfoCF("reminder", "Purged old fired reminders", map[string]interface{}{
			"purged": purged,
			"remaining": len(reminders),
		})
	}

	for i := range reminders {
		r := &reminders[i]

		// Track maxID across ALL reminders (fired + unfired) for unique IDs
		if id, err := strconv.Atoi(r.ID); err == nil && id > maxID {
			maxID = id
		}

		if r.Fired {
			continue
		}

		dueAt, err := time.Parse(time.RFC3339, r.DueAt)
		if err != nil {
			continue
		}

		delay := time.Until(dueAt)
		if delay <= 0 {
			// Already past due — fire immediately
			go t.fireReminder(r.ID, r.Message, r.Channel, r.ChatID)
			continue
		}

		timer := time.AfterFunc(delay, func() {
			t.fireReminder(r.ID, r.Message, r.Channel, r.ChatID)
		})
		t.timers[r.ID] = timer
	}

	t.nextID = maxID + 1
}

func (t *ReminderTool) set(args map[string]interface{}) *ToolResult {
	message, _ := args["message"].(string)
	durationStr, _ := args["duration"].(string)
	if message == "" || durationStr == "" {
		return ErrorResult("message and duration are required for set")
	}

	dur, err := parseDuration(durationStr)
	if err != nil {
		return ErrorResult(fmt.Sprintf("invalid duration '%s': %v", durationStr, err))
	}

	t.mu.Lock()
	channel := t.channel
	chatID := t.chatID

	id := fmt.Sprintf("%d", t.nextID)
	t.nextID++

	dueAt := time.Now().Add(dur)

	r := reminder{
		ID:        id,
		Message:   message,
		DueAt:     dueAt.Format(time.RFC3339),
		Channel:   channel,
		ChatID:    chatID,
		CreatedAt: time.Now().Format(time.RFC3339),
		Fired:     false,
	}

	reminders := t.loadRemindersLocked()
	reminders = append(reminders, r)
	t.saveRemindersLocked(reminders)

	timer := time.AfterFunc(dur, func() {
		t.fireReminder(r.ID, r.Message, r.Channel, r.ChatID)
	})
	t.timers[id] = timer
	t.mu.Unlock()

	return SilentResult(fmt.Sprintf("Reminder #%s set for %s (%s from now): %s",
		id, dueAt.Format("15:04"), durationStr, message))
}

func (t *ReminderTool) list() *ToolResult {
	t.mu.Lock()
	reminders := t.loadRemindersLocked()
	t.mu.Unlock()

	var pending []string
	for _, r := range reminders {
		if r.Fired {
			continue
		}
		pending = append(pending, fmt.Sprintf("- #%s: %s (due: %s)", r.ID, r.Message, r.DueAt))
	}

	if len(pending) == 0 {
		return SilentResult("No pending reminders")
	}
	return SilentResult(fmt.Sprintf("Pending reminders:\n%s", strings.Join(pending, "\n")))
}

func (t *ReminderTool) cancel(args map[string]interface{}) *ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return ErrorResult("id is required for cancel")
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if timer, ok := t.timers[id]; ok {
		timer.Stop()
		delete(t.timers, id)
	}

	reminders := t.loadRemindersLocked()
	found := false
	for i := range reminders {
		if reminders[i].ID == id && !reminders[i].Fired {
			reminders[i].Fired = true
			found = true
			break
		}
	}

	if !found {
		return SilentResult(fmt.Sprintf("Reminder #%s not found or already fired", id))
	}

	t.saveRemindersLocked(reminders)
	return SilentResult(fmt.Sprintf("Reminder #%s cancelled", id))
}

func (t *ReminderTool) fireReminder(id, message, channel, chatID string) {
	// Send notification
	if t.msgBus != nil && channel != "" && chatID != "" {
		t.msgBus.PublishOutbound(bus.OutboundMessage{
			Channel: channel,
			ChatID:  chatID,
			Content: fmt.Sprintf("Recordatorio: %s", message),
		})
	}

	// Mark as fired
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.timers, id)

	reminders := t.loadRemindersLocked()
	for i := range reminders {
		if reminders[i].ID == id {
			reminders[i].Fired = true
			break
		}
	}
	t.saveRemindersLocked(reminders)
}

func (t *ReminderTool) loadRemindersLocked() []reminder {
	data, err := os.ReadFile(t.filePath)
	if err != nil {
		return nil
	}
	var reminders []reminder
	if err := json.Unmarshal(data, &reminders); err != nil {
		logger.ErrorCF("reminder", "Failed to parse reminders file", map[string]interface{}{
			"error": err.Error(),
			"path":  t.filePath,
		})
		return nil
	}
	return reminders
}

func (t *ReminderTool) saveRemindersLocked(reminders []reminder) {
	data, err := json.MarshalIndent(reminders, "", "  ")
	if err != nil {
		logger.ErrorCF("reminder", "Failed to marshal reminders", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}
	// Atomic write
	tmpPath := t.filePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		logger.ErrorCF("reminder", "Failed to write reminders file", map[string]interface{}{
			"error": err.Error(),
		})
		return
	}
	if err := os.Rename(tmpPath, t.filePath); err != nil {
		logger.ErrorCF("reminder", "Failed to rename reminders file", map[string]interface{}{
			"error": err.Error(),
		})
	}
}

// parseDuration parses duration strings like "30m", "2h", "1d", "1h30m"
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))

	// Handle "d" for days by converting to hours
	if strings.Contains(s, "d") {
		parts := strings.SplitN(s, "d", 2)
		days, err := strconv.Atoi(parts[0])
		if err != nil {
			return 0, fmt.Errorf("invalid days: %s", parts[0])
		}
		remainder := strings.TrimSpace(parts[1])
		dur := time.Duration(days) * 24 * time.Hour
		if remainder != "" {
			extra, err := time.ParseDuration(remainder)
			if err != nil {
				return 0, err
			}
			dur += extra
		}
		return dur, nil
	}

	return time.ParseDuration(s)
}
