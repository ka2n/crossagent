package transcript

import (
	"encoding/json"
	"strings"
)

func parsePi(raw map[string]json.RawMessage, state *State) []Record {
	if text(raw["type"]) == "session" {
		state.SessionID, state.Cwd = text(raw["id"]), text(raw["cwd"])
		return nil
	}
	if text(raw["type"]) != "message" {
		return nil
	}
	var message map[string]json.RawMessage
	if json.Unmarshal(raw["message"], &message) != nil {
		return nil
	}
	role := text(message["role"])
	base := Record{
		Timestamp: parseTime(raw["timestamp"]),
		NativeID:  text(raw["id"]),
	}
	if base.Timestamp.IsZero() {
		base.Timestamp = parseTime(message["timestamp"])
	}
	switch role {
	case "user", "assistant":
		return piContent(message["content"], role, base)
	case "toolResult":
		record := base
		record.Role, record.Kind = "tool", "tool_result"
		record.NativeID = first(text(message["toolCallId"]), record.NativeID)
		record.Content = concise(strings.TrimSpace(text(message["toolName"]) + ": " + contentText(message["content"])))
		record.Data = visibleData(map[string]any{"type": "tool_result", "tool_call_id": record.NativeID, "tool_name": text(message["toolName"]), "content": json.RawMessage(message["content"])})
		if strings.TrimSpace(record.Content) != "" {
			return []Record{record}
		}
	default:
		return nil
	}
	return nil
}

func piContent(raw json.RawMessage, role string, base Record) []Record {
	var direct string
	if json.Unmarshal(raw, &direct) == nil {
		if strings.TrimSpace(direct) == "" {
			return nil
		}
		base.Role, base.Kind, base.Content = role, "message", direct
		base.Data = visibleData(map[string]any{"type": "message", "role": role, "content": direct})
		return []Record{base}
	}
	var blocks []struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Text      string          `json:"text"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var records []Record
	var visibleText []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if value := strings.TrimSpace(block.Text); value != "" {
				visibleText = append(visibleText, value)
			}
		case "toolCall":
			record := base
			record.Role, record.Kind, record.NativeID = "assistant", "tool_call", block.ID
			record.Content = concise(strings.TrimSpace(block.Name + " " + string(block.Arguments)))
			record.Data = visibleData(map[string]any{"type": "tool_call", "id": block.ID, "name": block.Name, "arguments": block.Arguments})
			records = append(records, record)
		}
	}
	if len(visibleText) > 0 {
		record := base
		record.Role, record.Kind, record.Content = role, "message", strings.Join(visibleText, "\n")
		record.Data = visibleData(map[string]any{"type": "message", "role": role, "content": record.Content})
		records = append([]Record{record}, records...)
	}
	return records
}
