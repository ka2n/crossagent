package hooks

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/ka2n/crossagent/agent"
)

// MarkerStyle selects how a ConfigManager records ownership. The zero value
// of ConfigManager selects Auto: it resolves to a command suffix when the
// selected agent's hook commands are known to run through a shell, and to None
// otherwise. The only written styles are CommandSuffix and None.
type MarkerStyle string

const (
	// MarkerStyleAuto selects the per-agent default. An empty MarkerStyle has
	// the same meaning for a ConfigManager.
	MarkerStyleAuto MarkerStyle = "auto"
	// MarkerStyleCommandSuffix appends a shell-comment ownership marker to
	// each installed command.
	MarkerStyleCommandSuffix MarkerStyle = "command-suffix"
	// MarkerStyleSuffix is a concise alias for MarkerStyleCommandSuffix.
	MarkerStyleSuffix = MarkerStyleCommandSuffix
	// MarkerStyleNone records no new marker and uses the ownership predicate.
	MarkerStyleNone MarkerStyle = "none"
)

const commandSuffixMarkerPrefix = "#crossagent:v1:"

// BuildCommandSuffixMarker appends a versioned, unquoted shell-comment marker
// to command. The tool and id are encoded independently, so delimiters in
// either value cannot make the marker ambiguous. ConfigManager calls this
// with a clean command; surrounding command whitespace is normalized here too.
func BuildCommandSuffixMarker(command, toolName, id string) string {
	command = strings.TrimSpace(command)
	marker := commandSuffixMarkerPrefix + encodeMarkerPart(toolName) + ":" + encodeMarkerPart(id)
	return command + " " + marker
}

// ParseCommandSuffixMarker recognizes only a marker in the exact format this
// package writes at the end of an unquoted shell comment. A # inside a quoted
// argument, an escaped #, an ordinary comment, or a marker-like string that is
// not the final comment is not recognized. cleanCommand is returned without
// the marker when marked is true.
func ParseCommandSuffixMarker(command string) (cleanCommand, toolName, id string, marked bool) {
	end := len(command)
	for end > 0 && isShellWhitespace(command[end-1]) {
		end--
	}
	if end == 0 {
		return command, "", "", false
	}

	quote := byte(0)
	comment := false
	wordStart := true
	for i := 0; i < end; i++ {
		if comment {
			if command[i] == '\n' {
				comment = false
				wordStart = true
			}
			continue
		}
		switch quote {
		case '\'':
			if command[i] == '\'' {
				quote = 0
			}
		case '"':
			switch command[i] {
			case '\\':
				if i+1 < end {
					i++
				}
			case '"':
				quote = 0
			}
		default:
			switch command[i] {
			case '\\':
				wordStart = false
				if i+1 < end {
					i++
				}
			case '\'':
				quote = '\''
				wordStart = false
			case '"':
				quote = '"'
				wordStart = false
			case ' ', '\t', '\n', '\r':
				wordStart = true
			case '#':
				if !wordStart {
					continue
				}
				if i == 0 || !isShellWhitespace(command[i-1]) {
					comment = true
					continue
				}
				if marker, ok := decodeCommandSuffixMarker(command[i:end]); ok {
					clean := strings.TrimRight(command[:i], " \t\n\r")
					return clean, marker.toolName, marker.id, true
				}
				// This is an ordinary shell comment. Text after it cannot
				// contain another command marker until a newline.
				comment = true
			default:
				wordStart = false
			}
		}
	}
	return command, "", "", false
}

// StripCommandSuffixMarker removes a marker written by this package and
// reports whether one was removed. It never changes a command containing only
// a quoted or otherwise marker-like # string.
func StripCommandSuffixMarker(command string) (cleanCommand string, stripped bool) {
	cleanCommand, _, _, stripped = ParseCommandSuffixMarker(command)
	return cleanCommand, stripped
}

type decodedCommandSuffixMarker struct {
	toolName string
	id       string
}

func decodeCommandSuffixMarker(value string) (decodedCommandSuffixMarker, bool) {
	if !strings.HasPrefix(value, commandSuffixMarkerPrefix) {
		return decodedCommandSuffixMarker{}, false
	}
	parts := strings.Split(value[len(commandSuffixMarkerPrefix):], ":")
	if len(parts) != 2 {
		return decodedCommandSuffixMarker{}, false
	}
	toolName, ok := decodeMarkerPart(parts[0])
	if !ok {
		return decodedCommandSuffixMarker{}, false
	}
	id, ok := decodeMarkerPart(parts[1])
	if !ok {
		return decodedCommandSuffixMarker{}, false
	}
	return decodedCommandSuffixMarker{toolName: toolName, id: id}, true
}

func encodeMarkerPart(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeMarkerPart(value string) (string, bool) {
	if value == "" {
		return "", false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) == 0 || encodeMarkerPart(string(decoded)) != value {
		return "", false
	}
	return string(decoded), true
}

func isShellWhitespace(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\r':
		return true
	default:
		return false
	}
}

const legacyMarkerOwnerField = "installedBy"

// legacyMarkerIDField is retained solely to read and remove metadata written
// by pre-command-suffix versions. It is never used to write a new entry.
func legacyMarkerIDField(toolName string) string {
	name := markerToken(toolName)
	if name == "" {
		name = "tool"
	}
	return "x-" + name + "-id"
}

func looksLikeLegacyMarkerIDField(key string) bool {
	return strings.HasPrefix(key, "x-") && strings.HasSuffix(key, "-id") && len(key) > len("x--id")
}

func legacyMarkerFor(entry map[string]any, toolName string) (id string, marked bool, hasMarker bool) {
	owner, ownerPresent := entry[legacyMarkerOwnerField]
	idValue, idPresent := entry[legacyMarkerIDField(toolName)]
	hasMarker = ownerPresent || idPresent
	if !hasMarker {
		for key := range entry {
			if looksLikeLegacyMarkerIDField(key) {
				hasMarker = true
				break
			}
		}
	}
	if !hasMarker {
		return "", false, false
	}
	ownerName, ownerOK := owner.(string)
	id, idOK := idValue.(string)
	if ownerOK && ownerName == toolName && idOK && strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id), true, true
	}
	return "", false, true
}

func commandForMarkerStyle(command string, toolName, id string, style MarkerStyle) string {
	if style == MarkerStyleCommandSuffix {
		return BuildCommandSuffixMarker(command, toolName, id)
	}
	return strings.TrimSpace(command)
}

func resolveMarkerStyle(requested MarkerStyle, name agent.Name) (MarkerStyle, error) {
	if requested == "" || requested == MarkerStyleAuto {
		if MarkerSupportFor(name).Supported {
			return MarkerStyleCommandSuffix, nil
		}
		return MarkerStyleNone, nil
	}
	switch requested {
	case MarkerStyleCommandSuffix:
		if !MarkerSupportFor(name).Supported {
			return "", fmt.Errorf("command-suffix markers are unsafe for %s: shell execution of hook commands is not established", name)
		}
		return requested, nil
	case MarkerStyleNone:
		return requested, nil
	default:
		return "", fmt.Errorf("unknown marker style %q: want auto, command-suffix, or none", requested)
	}
}
