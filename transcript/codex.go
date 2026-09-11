package transcript

import (
	"encoding/json"
	"strings"
)

func parseCodex(raw map[string]json.RawMessage, state *State) []Record {
	topType := text(raw["type"])
	var payload map[string]json.RawMessage
	_ = json.Unmarshal(raw["payload"], &payload)
	if topType == "session_meta" {
		state.SessionID = first(text(payload["id"]), text(payload["session_id"]))
		state.Cwd = text(payload["cwd"])
		return nil
	}
	if topType != "response_item" {
		return nil
	}
	record := Record{Timestamp: parseTime(raw["timestamp"])}
	switch text(payload["type"]) {
	case "message":
		role := text(payload["role"])
		if role != "user" && role != "assistant" {
			return nil
		}
		record.Role, record.Kind = role, "message"
		record.Content = contentText(payload["content"])
		record.Data = visibleData(struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}{"message", role, payload["content"]})
	case "function_call":
		record.Role, record.Kind = "assistant", "tool_call"
		record.NativeID = text(payload["call_id"])
		record.Content = concise(strings.TrimSpace(text(payload["name"]) + " " + text(payload["arguments"])))
		record.Data = visibleData(struct {
			Type      string          `json:"type"`
			Name      string          `json:"name"`
			CallID    string          `json:"call_id"`
			Arguments json.RawMessage `json:"arguments"`
		}{"function_call", text(payload["name"]), record.NativeID, payload["arguments"]})
	case "function_call_output":
		record.Role, record.Kind = "tool", "tool_result"
		record.NativeID = text(payload["call_id"])
		record.Content = concise(text(payload["output"]))
		record.Data = visibleData(struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Output string `json:"output"`
		}{"function_call_output", record.NativeID, text(payload["output"])})
	default:
		return nil
	}
	if strings.TrimSpace(record.Content) == "" {
		return nil
	}
	return []Record{record}
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
