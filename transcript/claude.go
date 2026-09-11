package transcript

import "encoding/json"

func parseClaude(raw map[string]json.RawMessage, state *State, entry *Entry) {
	if value := text(raw["sessionId"]); value != "" {
		state.SessionID = value
	}
	if value := text(raw["cwd"]); value != "" {
		state.Cwd = value
	}

	entry.Timestamp = parseTime(raw["timestamp"])
	entry.Envelope = Envelope{
		Type:      text(raw["type"]),
		Subtype:   text(raw["subtype"]),
		ID:        first(text(raw["uuid"]), text(raw["id"])),
		ParentID:  first(text(raw["parentUuid"]), text(raw["parentId"])),
		RequestID: text(raw["requestId"]),
		Model:     text(raw["model"]),
		Provider:  text(raw["provider"]),
		Fields:    raw,
	}

	var message map[string]json.RawMessage
	if json.Unmarshal(raw["message"], &message) == nil {
		entry.Envelope.Role = text(message["role"])
		defaults := blockDefaults{
			role: entry.Envelope.Role,
			id:   first(text(message["tool_use_id"]), text(message["toolUseId"])),
			name: text(message["toolName"]),
		}
		entry.Blocks = contentBlocks(message["content"], "message.content", "text", defaults)
		return
	}

	for _, field := range []string{"message", "data", "content"} {
		if value := raw[field]; len(value) != 0 {
			entry.Blocks = contentBlocks(value, field, first(entry.Envelope.Subtype, entry.Envelope.Type), blockDefaults{})
			return
		}
	}
}
