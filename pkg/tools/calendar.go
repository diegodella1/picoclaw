package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

type CalendarTool struct {
	opt option.ClientOption
}

func NewCalendarTool(saFile, email string) *CalendarTool {
	opt, err := googleClientOption(saFile, email,
		calendar.CalendarScope,
	)
	if err != nil {
		return nil
	}
	return &CalendarTool{opt: opt}
}

func (t *CalendarTool) Name() string { return "calendar" }

func (t *CalendarTool) Description() string {
	return "Google Calendar: list, create, update and delete events. Actions: list_events, create_event, update_event, delete_event, list_calendars."
}

func (t *CalendarTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform: list_events, create_event, update_event, delete_event, list_calendars",
				"enum":        []string{"list_events", "create_event", "update_event", "delete_event", "list_calendars"},
			},
			"calendar_id": map[string]interface{}{
				"type":        "string",
				"description": "Calendar ID (default: primary)",
			},
			"event_id": map[string]interface{}{
				"type":        "string",
				"description": "Event ID (for update/delete)",
			},
			"time_min": map[string]interface{}{
				"type":        "string",
				"description": "Start time filter in RFC3339 (e.g. 2026-02-20T00:00:00-03:00). Default: now",
			},
			"time_max": map[string]interface{}{
				"type":        "string",
				"description": "End time filter in RFC3339. Default: 7 days from now",
			},
			"max_results": map[string]interface{}{
				"type":        "number",
				"description": "Maximum events to return (default 10)",
			},
			"summary": map[string]interface{}{
				"type":        "string",
				"description": "Event title/summary",
			},
			"description": map[string]interface{}{
				"type":        "string",
				"description": "Event description",
			},
			"location": map[string]interface{}{
				"type":        "string",
				"description": "Event location",
			},
			"start": map[string]interface{}{
				"type":        "string",
				"description": "Event start time in RFC3339",
			},
			"end": map[string]interface{}{
				"type":        "string",
				"description": "Event end time in RFC3339",
			},
			"attendees": map[string]interface{}{
				"type":        "string",
				"description": "Comma-separated email addresses of attendees",
			},
		},
		"required": []string{"action"},
	}
}

func (t *CalendarTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	action, _ := args["action"].(string)

	srv, err := calendar.NewService(ctx, t.opt)
	if err != nil {
		return ErrorResult(fmt.Sprintf("calendar service error: %v", err))
	}

	switch action {
	case "list_events":
		return t.listEvents(srv, args)
	case "create_event":
		return t.createEvent(srv, args)
	case "update_event":
		return t.updateEvent(srv, args)
	case "delete_event":
		return t.deleteEvent(srv, args)
	case "list_calendars":
		return t.listCalendars(srv)
	default:
		return ErrorResult(fmt.Sprintf("unknown calendar action: %s", action))
	}
}

func (t *CalendarTool) calendarID(args map[string]interface{}) string {
	if id, ok := args["calendar_id"].(string); ok && id != "" {
		return id
	}
	return "primary"
}

func (t *CalendarTool) listEvents(srv *calendar.Service, args map[string]interface{}) *ToolResult {
	calID := t.calendarID(args)

	now := time.Now()
	timeMin := now.Format(time.RFC3339)
	timeMax := now.AddDate(0, 0, 7).Format(time.RFC3339)

	if v, ok := args["time_min"].(string); ok && v != "" {
		timeMin = v
	}
	if v, ok := args["time_max"].(string); ok && v != "" {
		timeMax = v
	}

	maxResults := int64(10)
	if n, ok := args["max_results"].(float64); ok && n > 0 {
		maxResults = int64(n)
	}

	events, err := srv.Events.List(calID).
		TimeMin(timeMin).
		TimeMax(timeMax).
		MaxResults(maxResults).
		SingleEvents(true).
		OrderBy("startTime").
		Do()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to list events: %v", err))
	}

	if len(events.Items) == 0 {
		return SilentResult("No events found in that range.")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Events (%d):\n\n", len(events.Items)))

	for i, ev := range events.Items {
		start := ev.Start.DateTime
		if start == "" {
			start = ev.Start.Date // all-day event
		}
		end := ev.End.DateTime
		if end == "" {
			end = ev.End.Date
		}

		sb.WriteString(fmt.Sprintf("%d. %s\n   ID: %s\n   Start: %s\n   End: %s\n",
			i+1, ev.Summary, ev.Id, start, end))
		if ev.Location != "" {
			sb.WriteString(fmt.Sprintf("   Location: %s\n", ev.Location))
		}
		if ev.Description != "" {
			desc := ev.Description
			if len(desc) > 200 {
				desc = desc[:200] + "..."
			}
			sb.WriteString(fmt.Sprintf("   Description: %s\n", desc))
		}
		sb.WriteString("\n")
	}

	return SilentResult(sb.String())
}

func (t *CalendarTool) createEvent(srv *calendar.Service, args map[string]interface{}) *ToolResult {
	calID := t.calendarID(args)

	summary, _ := args["summary"].(string)
	start, _ := args["start"].(string)
	end, _ := args["end"].(string)

	if summary == "" || start == "" || end == "" {
		return ErrorResult("summary, start, and end are required for create_event")
	}

	ev := &calendar.Event{
		Summary: summary,
		Start:   &calendar.EventDateTime{DateTime: start},
		End:     &calendar.EventDateTime{DateTime: end},
	}

	if desc, ok := args["description"].(string); ok && desc != "" {
		ev.Description = desc
	}
	if loc, ok := args["location"].(string); ok && loc != "" {
		ev.Location = loc
	}
	if attendeesStr, ok := args["attendees"].(string); ok && attendeesStr != "" {
		for _, email := range strings.Split(attendeesStr, ",") {
			email = strings.TrimSpace(email)
			if email != "" {
				ev.Attendees = append(ev.Attendees, &calendar.EventAttendee{Email: email})
			}
		}
	}

	created, err := srv.Events.Insert(calID, ev).Do()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create event: %v", err))
	}

	return SilentResult(fmt.Sprintf("Event created: %s\nID: %s\nLink: %s", created.Summary, created.Id, created.HtmlLink))
}

func (t *CalendarTool) updateEvent(srv *calendar.Service, args map[string]interface{}) *ToolResult {
	calID := t.calendarID(args)
	eventID, _ := args["event_id"].(string)

	if eventID == "" {
		return ErrorResult("event_id is required for update_event")
	}

	// Get existing event
	ev, err := srv.Events.Get(calID, eventID).Do()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to get event: %v", err))
	}

	// Apply updates
	if v, ok := args["summary"].(string); ok && v != "" {
		ev.Summary = v
	}
	if v, ok := args["description"].(string); ok && v != "" {
		ev.Description = v
	}
	if v, ok := args["location"].(string); ok && v != "" {
		ev.Location = v
	}
	if v, ok := args["start"].(string); ok && v != "" {
		ev.Start = &calendar.EventDateTime{DateTime: v}
	}
	if v, ok := args["end"].(string); ok && v != "" {
		ev.End = &calendar.EventDateTime{DateTime: v}
	}
	if attendeesStr, ok := args["attendees"].(string); ok && attendeesStr != "" {
		ev.Attendees = nil
		for _, email := range strings.Split(attendeesStr, ",") {
			email = strings.TrimSpace(email)
			if email != "" {
				ev.Attendees = append(ev.Attendees, &calendar.EventAttendee{Email: email})
			}
		}
	}

	updated, err := srv.Events.Update(calID, eventID, ev).Do()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to update event: %v", err))
	}

	return SilentResult(fmt.Sprintf("Event updated: %s (ID: %s)", updated.Summary, updated.Id))
}

func (t *CalendarTool) deleteEvent(srv *calendar.Service, args map[string]interface{}) *ToolResult {
	calID := t.calendarID(args)
	eventID, _ := args["event_id"].(string)

	if eventID == "" {
		return ErrorResult("event_id is required for delete_event")
	}

	if err := srv.Events.Delete(calID, eventID).Do(); err != nil {
		return ErrorResult(fmt.Sprintf("failed to delete event: %v", err))
	}

	return SilentResult(fmt.Sprintf("Event deleted (ID: %s)", eventID))
}

func (t *CalendarTool) listCalendars(srv *calendar.Service) *ToolResult {
	list, err := srv.CalendarList.List().Do()
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to list calendars: %v", err))
	}

	var sb strings.Builder
	sb.WriteString("Calendars:\n")
	for _, cal := range list.Items {
		primary := ""
		if cal.Primary {
			primary = " (primary)"
		}
		sb.WriteString(fmt.Sprintf("- %s%s (ID: %s)\n", cal.Summary, primary, cal.Id))
	}

	return SilentResult(sb.String())
}
