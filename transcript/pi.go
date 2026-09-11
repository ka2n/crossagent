package transcript

import "encoding/json"

func parsePi(raw map[string]json.RawMessage, state *State, entry *Entry) {
	entryType := text(raw["type"])
	if entryType == "session" {
		state.SessionID = text(raw["id"])
		state.Cwd = text(raw["cwd"])
	}

	var message map[string]json.RawMessage
	_ = json.Unmarshal(raw["message"], &message)

	entry.Timestamp = parseTime(raw["timestamp"])
	entry.Envelope = Envelope{
		Type:     entryType,
		Subtype:  first(text(raw["subtype"]), text(raw["customType"])),
		Role:     text(message["role"]),
		ID:       first(text(raw["id"]), text(raw["uuid"])),
		ParentID: first(text(raw["parentId"]), text(raw["parent_id"])),
		Model:    first(text(raw["modelId"]), text(raw["model"]), text(message["model"])),
		Provider: first(text(raw["provider"]), text(message["provider"])),
		Fields:   raw,
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = parseTime(message["timestamp"])
	}

	switch entryType {
	case "message":
		defaults := blockDefaults{
			role: entry.Envelope.Role,
			id:   first(text(message["toolCallId"]), text(message["tool_use_id"])),
			name: text(message["toolName"]),
		}
		if entry.Envelope.Role == "bashExecution" {
			defaults.name = first(defaults.name, "bash")
			entry.Blocks = []Block{nativeBlock(raw["message"], "message", "bashExecution", defaults)}
			return
		}
		entry.Blocks = contentBlocks(message["content"], "message.content", "text", defaults)
	case "compaction":
		entry.Blocks = contentBlocks(raw["summary"], "summary", "compaction_summary", blockDefaults{})
		entry.Blocks = append(entry.Blocks, piRetainedTailBlocks(raw["retainedTail"])...)
	case "branch_summary":
		entry.Blocks = contentBlocks(raw["summary"], "summary", "branch_summary", blockDefaults{id: text(raw["fromId"])})
	case "custom_message":
		entry.Envelope.Role = "custom"
		entry.Blocks = contentBlocks(raw["content"], "content", "custom_message", blockDefaults{role: "custom", name: text(raw["customType"])})
	}
}

func piRetainedTailBlocks(raw json.RawMessage) []Block {
	var messages []json.RawMessage
	if json.Unmarshal(raw, &messages) != nil {
		return nil
	}
	blocks := make([]Block, 0, len(messages))
	for _, message := range messages {
		block := nativeBlock(message, "retainedTail", "retainedTail", blockDefaults{})
		if block.Type == "retainedTail" && block.Role != "" {
			block.Type = block.Role
		}
		blocks = append(blocks, block)
	}
	return blocks
}
