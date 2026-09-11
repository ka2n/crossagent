package transcript

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

var claudeNoiseTypes = map[string]bool{
	"progress": true, "file-history-snapshot": true, "queue-operation": true,
	"last-prompt": true, "mode": true, "permission-mode": true,
	"ai-title": true, "attachment": true, "relocated": true,
	"worktree-state": true, "file-history-delta": true, "pr-link": true,
}

func parseClaude(raw map[string]json.RawMessage, state *State) []Record {
	if value := text(raw["sessionId"]); value != "" {
		state.SessionID = value
	}
	if value := text(raw["cwd"]); value != "" {
		state.Cwd = value
	}
	entryType := text(raw["type"])
	var isMeta bool
	_ = json.Unmarshal(raw["isMeta"], &isMeta)
	if isMeta || claudeNoiseTypes[entryType] || strings.Contains(string(raw["message"]), "[Request interrupted by user]") {
		return nil
	}
	timestamp := parseTime(raw["timestamp"])
	if timestamp.IsZero() {
		return nil
	}
	base := Record{Timestamp: timestamp, NativeID: text(raw["uuid"]), Metadata: claudeMetadata(raw)}
	if entryType == "user" || entryType == "assistant" {
		var message map[string]json.RawMessage
		if json.Unmarshal(raw["message"], &message) != nil {
			return nil
		}
		role := first(text(message["role"]), entryType)
		return claudeContent(message["content"], role, base)
	}
	if entryType == "system" || entryType == "custom-title" || entryType == "agent-name" {
		content := first(contentText(raw["message"]), contentText(raw["data"]), entryType)
		base.Role, base.Kind, base.Content = entryType, entryType, content
		base.Data = visibleData(map[string]any{"type": entryType, "subtype": text(raw["subtype"]), "content": content})
		return []Record{base}
	}
	return nil
}

func claudeContent(raw json.RawMessage, role string, base Record) []Record {
	var direct string
	if json.Unmarshal(raw, &direct) == nil {
		if strings.TrimSpace(direct) == "" {
			return nil
		}
		base.Role, base.Kind, base.Content = role, "message", role+": "+direct
		base.NativeID = claudeDerivedID(base.NativeID, "text", 0)
		base.Data = visibleData(map[string]any{"type": "message", "role": role, "content": direct})
		return []Record{base}
	}
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var records []Record
	for index, block := range blocks {
		switch text(block["type"]) {
		case "text":
			if value := strings.TrimSpace(text(block["text"])); value != "" {
				record := base
				record.Role, record.Kind, record.Content = role, "message", role+": "+value
				record.NativeID = claudeDerivedID(base.NativeID, "text", index)
				record.Data = visibleData(map[string]any{"type": "message", "role": role, "content": value})
				records = append(records, record)
			}
		case "tool_use":
			name, id := first(text(block["name"]), "tool"), text(block["id"])
			summary := summarizeClaudeTool(name, block["input"])
			content := "[" + name + "]"
			if summary != "" {
				content = "[" + name + ": " + summary + "]"
			}
			record := base
			record.Role, record.Kind, record.NativeID, record.Content = "assistant", "tool_call", id, content
			record.Data = visibleData(map[string]any{"type": "tool_use", "id": id, "name": name, "summary": summary})
			records = append(records, record)
		case "tool_result":
			id := text(block["tool_use_id"])
			result := contentText(block["content"])
			if len(result) > 200 {
				result = fmt.Sprintf("[result: %d bytes]", len(result))
			}
			if result != "" {
				record := base
				record.Role, record.Kind, record.NativeID, record.Content = "tool", "tool_result", id, result
				record.Data = visibleData(map[string]any{"type": "tool_result", "tool_use_id": id, "content": result})
				records = append(records, record)
			}
		case "document":
			var source map[string]json.RawMessage
			_ = json.Unmarshal(block["source"], &source)
			media, data := text(source["media_type"]), text(source["data"])
			visible := fmt.Sprintf("[document: %s, %d bytes]", media, len(data))
			record := base
			record.Role, record.Kind, record.Content = role, "message", role+": "+visible
			record.NativeID = claudeDerivedID(base.NativeID, "document", index)
			record.Data = visibleData(map[string]any{"type": "document", "role": role, "content": visible})
			records = append(records, record)
		}
	}
	return records
}

func claudeDerivedID(id, kind string, index int) string {
	if id == "" {
		return ""
	}
	return fmt.Sprintf("%s:%s:%d", id, kind, index)
}

func summarizeClaudeTool(name string, input json.RawMessage) string {
	var values map[string]any
	if json.Unmarshal(input, &values) != nil {
		return ""
	}
	stringValue := func(key string) string { value, _ := values[key].(string); return value }
	switch name {
	case "Write", "Edit":
		path, content := stringValue("file_path"), stringValue("content")
		if path != "" && content != "" {
			return fmt.Sprintf("file_path=%s (%d bytes)", path, len(content))
		}
		if path != "" {
			return "file_path=" + path
		}
	case "Read":
		if path := stringValue("file_path"); path != "" {
			return "file_path=" + path
		}
	case "Bash":
		command := stringValue("command")
		if len(command) > 200 {
			command = conciseBytes(command, 200) + "..."
		}
		return command
	case "Grep":
		result := "pattern=" + stringValue("pattern")
		if path := stringValue("path"); path != "" {
			result += ", path=" + path
		}
		return result
	case "Glob":
		return "pattern=" + stringValue("pattern")
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, 3)
	for _, key := range keys {
		parts = append(parts, key+"="+claudeValueShape(values[key]))
		if len(parts) == 3 {
			break
		}
	}
	return strings.Join(parts, ", ")
}

func claudeValueShape(value any) string {
	switch typed := value.(type) {
	case string:
		return fmt.Sprintf("string(%d bytes)", len(typed))
	case []any:
		return fmt.Sprintf("array(%d items)", len(typed))
	case map[string]any:
		return fmt.Sprintf("object(%d keys)", len(typed))
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func claudeMetadata(raw map[string]json.RawMessage) map[string]any {
	metadata := map[string]any{}
	for source, target := range map[string]string{
		"type": "type", "subtype": "subtype", "cwd": "cwd", "gitBranch": "git_branch",
		"version": "version", "userType": "user_type", "requestId": "request_id", "uuid": "uuid",
	} {
		if value := text(raw[source]); value != "" {
			metadata[target] = value
		}
	}
	return metadata
}
