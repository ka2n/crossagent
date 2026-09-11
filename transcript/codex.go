package transcript

import (
	"bytes"
	"encoding/json"
)

func parseCodex(raw map[string]json.RawMessage, state *State, entry *Entry) {
	var payload map[string]json.RawMessage
	_ = json.Unmarshal(raw["payload"], &payload)
	topType, payloadType := text(raw["type"]), text(payload["type"])

	if topType == "session_meta" {
		state.SessionID = first(text(payload["id"]), text(payload["session_id"]))
		// A new session header replaces all prior header state. Do not inherit a
		// cwd merely because this particular header omitted it.
		state.Cwd = text(payload["cwd"])
	} else if topType == "turn_context" {
		if value := text(payload["cwd"]); value != "" {
			state.Cwd = value
		}
	}

	entry.Timestamp = parseTime(raw["timestamp"])
	if entry.Timestamp.IsZero() {
		entry.Timestamp = parseTime(payload["timestamp"])
	}
	entry.Envelope = Envelope{
		Type:      topType,
		Subtype:   payloadType,
		Role:      text(payload["role"]),
		ID:        first(text(raw["id"]), text(raw["uuid"]), text(payload["id"])),
		ParentID:  first(text(raw["parent_id"]), text(raw["parentId"])),
		RequestID: first(text(raw["request_id"]), text(payload["request_id"])),
		Model:     first(text(raw["model"]), text(payload["model"])),
		Provider:  first(text(raw["provider"]), text(raw["model_provider"]), text(payload["provider"]), text(payload["model_provider"])),
		Fields:    raw,
	}

	defaults := blockDefaults{role: entry.Envelope.Role}
	if topType == "event_msg" {
		// Event payloads carry useful native fields beyond their primary text
		// (for example command output, image paths, and timing). Keep the whole
		// payload as one structural block for every current and future variant.
		entry.Blocks = codexPayloadBlock(raw["payload"], payloadType, topType, defaults)
		return
	}

	switch payloadType {
	case "message":
		entry.Blocks = contentBlocks(payload["content"], "payload.content", "text", defaults)
		if len(entry.Blocks) == 0 {
			entry.Blocks = codexPayloadBlock(raw["payload"], payloadType, topType, defaults)
		}
	case "reasoning":
		defaults.signature = text(payload["encrypted_content"])
		entry.Blocks = append(entry.Blocks, contentBlocks(payload["summary"], "payload.summary", "summary", defaults)...)
		entry.Blocks = append(entry.Blocks, contentBlocks(payload["content"], "payload.content", "reasoning", defaults)...)
		if len(entry.Blocks) == 0 {
			entry.Blocks = codexPayloadBlock(raw["payload"], payloadType, topType, defaults)
		}
	default:
		if topType == "response_item" {
			// Codex adds response variants regularly. Preserve an immediately
			// useful structural block instead of requiring library updates before
			// consumers can inspect a new item.
			entry.Blocks = codexPayloadBlock(raw["payload"], payloadType, topType, defaults)
		}
	}
}

func codexPayloadBlock(raw json.RawMessage, payloadType, topType string, defaults blockDefaults) []Block {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	return []Block{nativeBlock(raw, "payload", first(payloadType, topType), defaults)}
}
