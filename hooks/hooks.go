// Package hooks contains the normalized lifecycle-event and command-payload
// facts for Claude Code and Codex. It does not install, run, or write hooks.
//
// pi is represented explicitly as having no declarative hook system: its
// supported integration surface is JavaScript extensions.
package hooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Agent names are the canonical names used by crossagent.Detector.
const (
	// AgentClaude is Claude Code's canonical name.
	AgentClaude = "claude"
	// AgentCodex is Codex's canonical name.
	AgentCodex = "codex"
	// AgentPi is pi's canonical name.
	AgentPi = "pi"
)

// Event is a canonical lifecycle event name. Claude Code's spelling is used
// as the base vocabulary; Codex uses the same names for its declared events.
type Event string

const (
	// EventSessionStart fires when a session starts or resumes.
	EventSessionStart Event = "SessionStart"
	// EventSetup fires during session setup.
	EventSetup Event = "Setup"
	// EventUserPromptSubmit fires when a user prompt is submitted.
	EventUserPromptSubmit Event = "UserPromptSubmit"
	// EventUserPromptExpansion fires while expanding a user prompt command.
	EventUserPromptExpansion Event = "UserPromptExpansion"
	// EventPreToolUse fires before a tool runs.
	EventPreToolUse Event = "PreToolUse"
	// EventPermissionRequest fires when a permission decision is requested.
	EventPermissionRequest Event = "PermissionRequest"
	// EventPermissionDenied fires after a permission denial.
	EventPermissionDenied Event = "PermissionDenied"
	// EventPostToolUse fires after a tool runs.
	EventPostToolUse Event = "PostToolUse"
	// EventPostToolUseFailure fires after a failed tool run.
	EventPostToolUseFailure Event = "PostToolUseFailure"
	// EventPostToolBatch fires after a batch of tool runs.
	EventPostToolBatch Event = "PostToolBatch"
	// EventNotification fires for an agent notification.
	EventNotification Event = "Notification"
	// EventMessageDisplay fires when agent output is displayed.
	EventMessageDisplay Event = "MessageDisplay"
	// EventSubagentStart fires when a subagent starts.
	EventSubagentStart Event = "SubagentStart"
	// EventSubagentStop fires when a subagent stops.
	EventSubagentStop Event = "SubagentStop"
	// EventTaskCreated fires when a task is created.
	EventTaskCreated Event = "TaskCreated"
	// EventTaskCompleted fires when a task is completed.
	EventTaskCompleted Event = "TaskCompleted"
	// EventStop fires when the agent is about to stop.
	EventStop Event = "Stop"
	// EventStopFailure fires when stopping fails.
	EventStopFailure Event = "StopFailure"
	// EventTeammateIdle fires when a teammate becomes idle.
	EventTeammateIdle Event = "TeammateIdle"
	// EventInstructionsLoaded fires when instructions are loaded.
	EventInstructionsLoaded Event = "InstructionsLoaded"
	// EventConfigChange fires when configuration changes.
	EventConfigChange Event = "ConfigChange"
	// EventCWDChanged fires when the working directory changes.
	EventCWDChanged Event = "CwdChanged"
	// EventDirectoryAdded fires when a directory is added.
	EventDirectoryAdded Event = "DirectoryAdded"
	// EventFileChanged fires when a file changes.
	EventFileChanged Event = "FileChanged"
	// EventWorktreeCreate fires when a worktree is created.
	EventWorktreeCreate Event = "WorktreeCreate"
	// EventWorktreeRemove fires when a worktree is removed.
	EventWorktreeRemove Event = "WorktreeRemove"
	// EventPreCompact fires before context compaction.
	EventPreCompact Event = "PreCompact"
	// EventPostCompact fires after context compaction.
	EventPostCompact Event = "PostCompact"
	// EventPreModelSwitch fires before switching models.
	EventPreModelSwitch Event = "PreModelSwitch"
	// EventPostModelSwitch fires after switching models.
	EventPostModelSwitch Event = "PostModelSwitch"
	// EventElicitation fires when an MCP server requests elicitation.
	EventElicitation Event = "Elicitation"
	// EventElicitationResult fires after an elicitation response.
	EventElicitationResult Event = "ElicitationResult"
	// EventSessionEnd fires when a session ends.
	EventSessionEnd Event = "SessionEnd"
	// EventInterrupt is Codex's additional interrupt event. It is part of the
	// canonical union even though Claude Code does not have this event name.
	EventInterrupt Event = "Interrupt"
)

// UnknownEvent means that a payload did not contain a known event name. It is
// returned instead of rejecting a payload so forward-compatible callers can
// inspect RawEventName.
const UnknownEvent Event = ""

const (
	// SourceStartup identifies a newly started session.
	SourceStartup = "startup"
	// SourceResume identifies a resumed session.
	SourceResume = "resume"
	// SourceClear identifies a session cleared into a new conversation.
	SourceClear = "clear"
	// SourceCompact identifies a session created or resumed through compaction.
	SourceCompact = "compact"
	// SourceFork identifies a session fork.
	SourceFork = "fork"
)

// Short aliases make event tables convenient to read while Event-prefixed
// constants remain available for codebases that prefer explicit names.
const (
	SessionStart        = EventSessionStart
	Setup               = EventSetup
	UserPromptSubmit    = EventUserPromptSubmit
	UserPromptExpansion = EventUserPromptExpansion
	PreToolUse          = EventPreToolUse
	PermissionRequest   = EventPermissionRequest
	PermissionDenied    = EventPermissionDenied
	PostToolUse         = EventPostToolUse
	PostToolUseFailure  = EventPostToolUseFailure
	PostToolBatch       = EventPostToolBatch
	Notification        = EventNotification
	MessageDisplay      = EventMessageDisplay
	SubagentStart       = EventSubagentStart
	SubagentStop        = EventSubagentStop
	TaskCreated         = EventTaskCreated
	TaskCompleted       = EventTaskCompleted
	Stop                = EventStop
	StopFailure         = EventStopFailure
	TeammateIdle        = EventTeammateIdle
	InstructionsLoaded  = EventInstructionsLoaded
	ConfigChange        = EventConfigChange
	CWDChanged          = EventCWDChanged
	DirectoryAdded      = EventDirectoryAdded
	FileChanged         = EventFileChanged
	WorktreeCreate      = EventWorktreeCreate
	WorktreeRemove      = EventWorktreeRemove
	PreCompact          = EventPreCompact
	PostCompact         = EventPostCompact
	PreModelSwitch      = EventPreModelSwitch
	PostModelSwitch     = EventPostModelSwitch
	Elicitation         = EventElicitation
	ElicitationResult   = EventElicitationResult
	SessionEnd          = EventSessionEnd
	Interrupt           = EventInterrupt
)

// Evidence labels the kind of support behind an exported fact.
type Evidence string

const (
	// EvidenceDocumented means the fact is in the agent's public
	// documentation.
	EvidenceDocumented Evidence = "documented"
	// EvidenceConfirmedOSS means the fact was confirmed in an inspected
	// open-source implementation.
	EvidenceConfirmedOSS Evidence = "confirmed-in-oss"
	// EvidenceObserved means the fact was observed in a local installation.
	EvidenceObserved Evidence = "observed"
)

var claudeEvents = []Event{
	EventSessionStart,
	EventSetup,
	EventUserPromptSubmit,
	EventUserPromptExpansion,
	EventPreToolUse,
	EventPermissionRequest,
	EventPermissionDenied,
	EventPostToolUse,
	EventPostToolUseFailure,
	EventPostToolBatch,
	EventNotification,
	EventMessageDisplay,
	EventSubagentStart,
	EventSubagentStop,
	EventTaskCreated,
	EventTaskCompleted,
	EventStop,
	EventStopFailure,
	EventTeammateIdle,
	EventInstructionsLoaded,
	EventConfigChange,
	EventCWDChanged,
	EventDirectoryAdded,
	EventFileChanged,
	EventWorktreeCreate,
	EventWorktreeRemove,
	EventPreCompact,
	EventPostCompact,
	EventPreModelSwitch,
	EventPostModelSwitch,
	EventElicitation,
	EventElicitationResult,
	EventSessionEnd,
}

var codexEvents = []Event{
	EventPreToolUse,
	EventPermissionRequest,
	EventPostToolUse,
	EventPreCompact,
	EventPostCompact,
	EventSessionStart,
	EventSessionEnd,
	EventUserPromptSubmit,
	EventSubagentStart,
	EventSubagentStop,
	EventStop,
	EventInterrupt,
}

var codexOperationalEvents = []Event{
	EventPreToolUse,
	EventPostToolUse,
	EventSessionStart,
	EventUserPromptSubmit,
	EventStop,
}

// canonicalEvents is the union in stable Claude-first order. Interrupt is
// appended because it is present in Codex's OSS list but not Claude's list.
var canonicalEvents = append(append([]Event(nil), claudeEvents...), EventInterrupt)

// Surface describes an agent's hook configuration surface. Events are the
// documented/declared vocabulary. OperationalEvents is narrower where the
// inspected implementation exposes an event in protocol/config data before
// the command engine dispatches it.
type Surface struct {
	// HasHooks distinguishes pi's explicit no-hooks answer from an empty
	// event map caused by a lookup failure.
	HasHooks bool
	// Events is a copy of the agent's declared event vocabulary.
	Events []Event
	// OperationalEvents is a copy of events currently dispatched by the
	// inspected command-hook engine, when that distinction is known.
	OperationalEvents []Event
	// Note records an important evidence or compatibility qualification.
	Note string
}

// SurfaceFor returns the hook surface for agent. An unknown agent has
// HasHooks=false and no events.
func SurfaceFor(agent string) Surface {
	switch normalizeAgent(agent) {
	case AgentClaude:
		return Surface{
			HasHooks:          true,
			Events:            cloneEvents(claudeEvents),
			OperationalEvents: cloneEvents(claudeEvents),
			Note:              "Claude Code event names are documented; hook output details are version-sensitive.",
		}
	case AgentCodex:
		return Surface{
			HasHooks:          true,
			Events:            cloneEvents(codexEvents),
			OperationalEvents: cloneEvents(codexOperationalEvents),
			Note:              "The 12-event list is confirmed in OSS; the inspected command engine currently dispatches five command-hook events.",
		}
	case AgentPi:
		return Surface{
			HasHooks: false,
			Note:     "pi has no declarative hook system; its supported integration surface is JavaScript extensions.",
		}
	default:
		return Surface{}
	}
}

// CanonicalEvents returns the complete normalized vocabulary. The returned
// slice is independent and may be modified by the caller.
func CanonicalEvents() []Event {
	return cloneEvents(canonicalEvents)
}

// Events returns the declared hook events for agent. For pi it returns nil and
// SurfaceFor explicitly reports HasHooks=false.
func Events(agent string) []Event {
	return cloneEvents(SurfaceFor(agent).Events)
}

// SessionStartSources returns the known values of a SessionStart source field.
// These values are event-payload vocabulary, not configuration scopes. The
// list is based on the inspected Claude and Codex schemas and may grow with a
// vendor release.
func SessionStartSources(agent string) []string {
	switch normalizeAgent(agent) {
	case AgentClaude:
		return []string{SourceStartup, SourceResume, SourceClear, SourceCompact, SourceFork}
	case AgentCodex:
		return []string{SourceStartup, SourceResume, SourceClear, SourceCompact}
	default:
		return nil
	}
}

// OperationalEvents returns events dispatched by the inspected command-hook
// implementation. It is useful when declared protocol vocabulary is wider
// than the current executable's command runner.
func OperationalEvents(agent string) []Event {
	return cloneEvents(SurfaceFor(agent).OperationalEvents)
}

// SupportsEvent reports whether event is in the agent's declared hook
// vocabulary. It returns false for pi and unknown agents.
func SupportsEvent(agent string, event Event) bool {
	for _, candidate := range SurfaceFor(agent).Events {
		if candidate == event {
			return true
		}
	}
	return false
}

// IsOperationalEvent reports whether event is currently dispatched by the
// inspected command-hook engine. This does not promise that a future agent
// release will keep the same subset.
func IsOperationalEvent(agent string, event Event) bool {
	for _, candidate := range SurfaceFor(agent).OperationalEvents {
		if candidate == event {
			return true
		}
	}
	return false
}

// Mapping is one direction of an agent-to-canonical event mapping.
type Mapping struct {
	// Canonical is the normalized event.
	Canonical Event
	// AgentName is the exact event spelling accepted by the agent.
	AgentName string
}

// Mappings returns all mappings for agent. Claude and Codex currently use the
// same spelling as the canonical event; keeping the mapping explicit allows
// future agent-specific spellings without changing downstream tables.
func Mappings(agent string) []Mapping {
	events := Events(agent)
	if len(events) == 0 {
		return nil
	}
	mappings := make([]Mapping, len(events))
	for i, event := range events {
		mappings[i] = Mapping{Canonical: event, AgentName: string(event)}
	}
	return mappings
}

// ToCanonical converts an agent event name to the shared vocabulary. Unknown
// or unsupported names return UnknownEvent and false; the original name is
// not discarded by ParsePayload, which stores it in RawEventName.
func ToCanonical(agent, eventName string) (Event, bool) {
	event := Event(eventName)
	if !SupportsEvent(agent, event) {
		return UnknownEvent, false
	}
	return event, true
}

// FromCanonical converts a canonical event to the exact agent spelling.
// Claude and Codex are identity mappings; pi and unknown agents have no
// mapping.
func FromCanonical(agent string, event Event) (string, bool) {
	if !SupportsEvent(agent, event) {
		return "", false
	}
	return string(event), true
}

// TextReturn describes whether synchronous hook output can reach the model
// and the wire field(s) carrying that text. Fields are dotted JSON paths, or
// "stdout" for an output channel rather than a JSON field.
type TextReturn struct {
	// CanReturnText is false when the event's command output is not a
	// model-visible text channel.
	CanReturnText bool
	// Field is the primary text field. It is empty when CanReturnText is
	// false; Fields contains all accepted channels when there is more than
	// one.
	Field string
	// Fields contains all accepted text channels in stable order.
	Fields []string
	// Evidence identifies the source strength of this event-specific fact.
	Evidence Evidence
	// Note records version or synchronization qualifications.
	Note string
}

const (
	// FieldStdout is the command's captured standard output.
	FieldStdout = "stdout"
	// FieldHookSpecificAdditionalContext is the JSON context field used by
	// Claude Code and Codex on context-bearing events.
	FieldHookSpecificAdditionalContext = "hookSpecificOutput.additionalContext"
	// FieldDecision is a top-level output decision field on stop events.
	FieldDecision = "decision"
	// FieldReason is a top-level output reason field on stop events.
	FieldReason = "reason"
)

// TextReturnFor returns the model-visible text contract for an agent/event.
// The table describes synchronous command output. AsyncSemanticsFor explains
// why an async handler must not be used when delivery at the event boundary is
// required.
func TextReturnFor(agent string, event Event) TextReturn {
	switch normalizeAgent(agent) {
	case AgentClaude:
		return claudeTextReturn(event)
	case AgentCodex:
		return codexTextReturn(event)
	default:
		return TextReturn{}
	}
}

func claudeTextReturn(event Event) TextReturn {
	const documented = EvidenceDocumented
	context := func(note string) TextReturn {
		return TextReturn{
			CanReturnText: true,
			Field:         FieldHookSpecificAdditionalContext,
			Fields:        []string{FieldHookSpecificAdditionalContext},
			Evidence:      documented,
			Note:          note,
		}
	}
	switch event {
	case EventSessionStart:
		return context("Context is placed at the start of the conversation.")
	case EventUserPromptSubmit:
		return TextReturn{
			CanReturnText: true,
			Field:         FieldHookSpecificAdditionalContext,
			Fields:        []string{FieldHookSpecificAdditionalContext, FieldStdout},
			Evidence:      documented,
			Note:          "Plain stdout is also a documented context channel for UserPromptSubmit.",
		}
	case EventUserPromptExpansion:
		return context("Context is placed alongside the submitted prompt.")
	case EventPreToolUse, EventPostToolUse, EventPostToolUseFailure, EventPostToolBatch:
		return context("Context is placed next to the tool result.")
	case EventSubagentStart:
		return context("Context is placed at the start of the subagent conversation.")
	case EventSubagentStop:
		return context("Context is feedback to the subagent at the end of its turn.")
	case EventStop:
		return context("Context is stop feedback; it keeps the conversation loop going.")
	case EventPostModelSwitch:
		return context("Context is included with the next request after the model switch.")
	case EventPreCompact:
		// The current installed 2.1.259 implementation consumes successful
		// stdout as custom compaction instructions. The notes' claim that
		// hookSpecificOutput.additionalContext is verified for PreCompact is
		// not supported by that implementation, so do not encode it as a
		// stable JSON contract.
		return TextReturn{
			CanReturnText: true,
			Field:         FieldStdout,
			Fields:        []string{FieldStdout},
			Evidence:      EvidenceObserved,
			Note:          "Observed in Claude Code 2.1.259: non-empty successful stdout becomes custom compaction instructions; version-sensitive and not the documented additionalContext field.",
		}
	default:
		return TextReturn{}
	}
}

func codexTextReturn(event Event) TextReturn {
	context := func(note string) TextReturn {
		return TextReturn{
			CanReturnText: true,
			Field:         FieldHookSpecificAdditionalContext,
			Fields:        []string{FieldHookSpecificAdditionalContext},
			Evidence:      EvidenceConfirmedOSS,
			Note:          note,
		}
	}
	switch event {
	case EventSessionStart:
		return context("The OSS output contract names hookSpecificOutput.additionalContext.")
	case EventSubagentStart:
		return context("The OSS event/output contract lists additionalContext for SubagentStart; command dispatch is version-sensitive.")
	case EventUserPromptSubmit:
		return context("The OSS output contract names hookSpecificOutput.additionalContext.")
	case EventPreToolUse:
		return context("The OSS schema declares additionalContext alongside the permission fields; current engine validation may restrict which combinations are accepted.")
	case EventPostToolUse:
		return context("The OSS output contract names hookSpecificOutput.additionalContext.")
	default:
		return TextReturn{}
	}
}

// OutputContract describes important JSON output placement facts. Decision and
// reason are deliberately exposed separately from TextReturn because stop
// decisions are top-level, not nested in hookSpecificOutput.
type OutputContract struct {
	// Text is the model-visible text contract for the event.
	Text TextReturn
	// DecisionField is the JSON path for a decision, if this event has one.
	DecisionField string
	// ReasonField is the JSON path for the decision reason, if this event has
	// one.
	ReasonField string
	// Evidence identifies the source strength of the placement fact.
	Evidence Evidence
}

// OutputFor returns output placement facts for agent/event.
func OutputFor(agent string, event Event) OutputContract {
	normalizedAgent := normalizeAgent(agent)
	text := TextReturnFor(normalizedAgent, event)
	contract := OutputContract{Text: text, Evidence: text.Evidence}
	switch normalizedAgent {
	case AgentClaude, AgentCodex:
		switch event {
		case EventStop, EventSubagentStop:
			contract.DecisionField = FieldDecision
			contract.ReasonField = FieldReason
			if normalizedAgent == AgentClaude {
				contract.Evidence = EvidenceDocumented
			} else {
				contract.Evidence = EvidenceConfirmedOSS
			}
		}
	}
	return contract
}

// AsyncSemantics describes the meaning of an async command handler. It is
// deliberately descriptive rather than an execution API.
type AsyncSemantics struct {
	// Supported reports whether the agent's command-hook configuration has an
	// async handler setting.
	Supported bool
	// FireAndForget reports that an async handler runs without waiting at the
	// event boundary.
	FireAndForget bool
	// ControlRequiresSynchronous reports that decisions and blocking effects
	// require a synchronous handler.
	ControlRequiresSynchronous bool
	// TextMayArriveLater reports the documented Claude behavior for completed
	// async context output. It is false when the agent discards async effects.
	TextMayArriveLater bool
	// Evidence identifies the source strength of the semantics.
	Evidence Evidence
	// Note gives practical interpretation.
	Note string
}

// AsyncSemanticsFor returns async-handler semantics for agent/event. Claude
// and Codex values describe command handlers; pi has no hook handler setting.
func AsyncSemanticsFor(agent string, event Event) AsyncSemantics {
	switch normalizeAgent(agent) {
	case AgentClaude:
		text := TextReturnFor(agent, event)
		return AsyncSemantics{
			Supported:                  true,
			FireAndForget:              true,
			ControlRequiresSynchronous: true,
			TextMayArriveLater:         text.Field == FieldHookSpecificAdditionalContext,
			Evidence:                   EvidenceDocumented,
			Note:                       "async:true backgrounds the command; it cannot control the action already continued. Required context should use a synchronous handler.",
		}
	case AgentCodex:
		return AsyncSemantics{
			Supported:                  true,
			FireAndForget:              true,
			ControlRequiresSynchronous: true,
			Evidence:                   EvidenceConfirmedOSS,
			Note:                       "An async Codex handler's decision is parsed and discarded; control effects require synchronous execution.",
		}
	default:
		return AsyncSemantics{}
	}
}

// PayloadFields names the normalized fields a command hook may receive for an
// event. Empty strings mean that the field is not part of that agent/event's
// known schema.
type PayloadFields struct {
	// SessionID is the session identifier field.
	SessionID string
	// CWD is the current working directory field.
	CWD string
	// TranscriptPath is the nullable transcript path field.
	TranscriptPath string
	// EventName is the field carrying the agent event name.
	EventName string
	// Source is the SessionStart source field.
	Source string
	// StopHookActive guards against repeated stop-hook continuation loops.
	StopHookActive string
	// TurnID is Codex's turn-scoped identifier when present.
	TurnID string
	// AgentID and AgentType identify a subagent when present.
	AgentID   string
	AgentType string
	// AgentTranscriptPath is a subagent transcript path field (Claude and
	// Codex use it on SubagentStop).
	AgentTranscriptPath string
	// LastAssistantMessage is the stop payload's last message field.
	LastAssistantMessage string
	// Trigger is the compaction trigger field.
	Trigger string
	// Prompt is the submitted prompt field.
	Prompt string
	// ToolName and ToolUseID are tool-event fields.
	ToolName  string
	ToolUseID string
	// Reason is an event-specific reason field, such as SessionEnd.
	Reason string
	// Model and PermissionMode are Codex command-payload fields.
	Model          string
	PermissionMode string
}

// PayloadFieldsFor returns per-agent field names. Claude and Codex payloads
// are sparse and event-dependent; callers should not assume every non-empty
// field is present on every invocation.
func PayloadFieldsFor(agent string, event Event) PayloadFields {
	switch normalizeAgent(agent) {
	case AgentClaude:
		fields := PayloadFields{
			SessionID:      "session_id",
			CWD:            "cwd",
			TranscriptPath: "transcript_path",
			EventName:      "hook_event_name",
		}
		switch event {
		case EventSessionStart:
			fields.Source = "source"
		case EventStop:
			fields.StopHookActive = "stop_hook_active"
			fields.LastAssistantMessage = "last_assistant_message"
		case EventSubagentStart:
			fields.AgentID = "agent_id"
			fields.AgentType = "agent_type"
		case EventSubagentStop:
			fields.StopHookActive = "stop_hook_active"
			fields.AgentID = "agent_id"
			fields.AgentType = "agent_type"
			fields.AgentTranscriptPath = "agent_transcript_path"
			fields.LastAssistantMessage = "last_assistant_message"
		case EventPreCompact, EventPostCompact:
			fields.Trigger = "trigger"
		}
		return fields
	case AgentCodex:
		fields := PayloadFields{
			SessionID:      "session_id",
			CWD:            "cwd",
			TranscriptPath: "transcript_path",
			EventName:      "hook_event_name",
		}
		switch event {
		case EventSessionStart:
			fields.Source = "source"
		case EventStop:
			fields.TurnID = "turn_id"
			fields.StopHookActive = "stop_hook_active"
			fields.LastAssistantMessage = "last_assistant_message"
		case EventSubagentStart:
			fields.TurnID = "turn_id"
			fields.AgentID = "agent_id"
			fields.AgentType = "agent_type"
		case EventSubagentStop:
			fields.TurnID = "turn_id"
			fields.AgentID = "agent_id"
			fields.AgentType = "agent_type"
			fields.AgentTranscriptPath = "agent_transcript_path"
			fields.StopHookActive = "stop_hook_active"
			fields.LastAssistantMessage = "last_assistant_message"
		case EventPreCompact, EventPostCompact:
			fields.TurnID = "turn_id"
			fields.Trigger = "trigger"
		case EventUserPromptSubmit:
			fields.TurnID = "turn_id"
			fields.Prompt = "prompt"
		case EventPreToolUse:
			fields.TurnID = "turn_id"
			fields.ToolName = "tool_name"
			fields.ToolUseID = "tool_use_id"
		case EventPostToolUse:
			fields.TurnID = "turn_id"
			fields.ToolName = "tool_name"
			fields.ToolUseID = "tool_use_id"
		case EventSessionEnd:
			fields.Reason = "reason"
		default:
			fields.TurnID = "turn_id"
		}
		if event != EventSessionEnd {
			fields.Model = "model"
		}
		if event != EventPreCompact && event != EventPostCompact && event != EventSessionEnd {
			fields.PermissionMode = "permission_mode"
		}
		return fields
	default:
		// pi has no hook payload schema; this explicit zero result is not
		// an accidental empty mapping.
		return PayloadFields{}
	}
}

// Environment describes variables relevant to hook/integration child
// processes. For pi, HookSupported is false and Variables are the documented
// variables available to its shell-tool/extension integration surface, not a
// declarative hook environment.
type Environment struct {
	// HookSupported reports whether the agent has a declarative command-hook
	// process to which these variables can be attributed.
	HookSupported bool
	// Variables contains names, not values. Values are session-dependent.
	Variables []string
	// Evidence identifies the source strength of the variable list.
	Evidence Evidence
	// Note records whether the variables are hook-specific or a nearby
	// integration surface.
	Note string
}

const (
	// EnvClaudeSessionID is Claude Code's session id environment variable.
	EnvClaudeSessionID = "CLAUDE_CODE_SESSION_ID"
	// EnvClaudeMessagingSocket is Claude Code's session inbox socket path.
	EnvClaudeMessagingSocket = "CLAUDE_CODE_MESSAGING_SOCKET"
	// EnvClaudeMessagingToken is Claude Code's session inbox token.
	EnvClaudeMessagingToken = "CLAUDE_CODE_MESSAGING_TOKEN"
	// EnvClaudeChildSession marks a Claude child session.
	EnvClaudeChildSession = "CLAUDE_CODE_CHILD_SESSION"
	// EnvCodexThreadID is injected for Codex shell/exec children. The
	// inspected hook command runner does not add it specifically to hooks.
	EnvCodexThreadID = "CODEX_THREAD_ID"
	// EnvPiSessionID is pi's current session id for shell tools.
	EnvPiSessionID = "PI_SESSION_ID"
	// EnvPiSessionFile is pi's current session JSONL path for shell tools.
	EnvPiSessionFile = "PI_SESSION_FILE"
	// EnvPiProvider is pi's active provider for shell tools.
	EnvPiProvider = "PI_PROVIDER"
	// EnvPiModel is pi's active model for shell tools.
	EnvPiModel = "PI_MODEL"
	// EnvPiReasoningLevel is pi's effective reasoning level for shell tools.
	EnvPiReasoningLevel = "PI_REASONING_LEVEL"
	// EnvAIAgent is pi's generic child-process marker.
	EnvAIAgent = "AI_AGENT"
	// EnvPiCodingAgent is pi's child-process marker.
	EnvPiCodingAgent = "PI_CODING_AGENT"
)

var claudeEnvironmentVariables = []string{
	EnvClaudeSessionID,
	EnvClaudeMessagingSocket,
	EnvClaudeMessagingToken,
	EnvClaudeChildSession,
}

var piEnvironmentVariables = []string{
	EnvPiSessionID,
	EnvPiSessionFile,
	EnvPiProvider,
	EnvPiModel,
	EnvPiReasoningLevel,
	EnvAIAgent,
	EnvPiCodingAgent,
}

// EnvironmentFor returns the known environment-variable names for agent.
// Codex intentionally returns no hook-specific variables: the inspected OSS
// hook command runner inherits its environment but does not explicitly export
// a Codex hook variable. CODEX_THREAD_ID is a shell/exec-child fact, not a
// verified hook contract.
func EnvironmentFor(agent string) Environment {
	switch normalizeAgent(agent) {
	case AgentClaude:
		return Environment{
			HookSupported: true,
			Variables:     cloneStrings(claudeEnvironmentVariables),
			Evidence:      EvidenceObserved,
			Note:          "Verified present in the local Claude Code hook environment.",
		}
	case AgentCodex:
		return Environment{
			HookSupported: true,
			Evidence:      EvidenceConfirmedOSS,
			Note:          "No Codex-specific hook environment export was found in the inspected command runner; payload fields carry session identity. CODEX_THREAD_ID is injected for shell/exec children, not encoded here as a hook variable.",
		}
	case AgentPi:
		return Environment{
			HookSupported: false,
			Variables:     cloneStrings(piEnvironmentVariables),
			Evidence:      EvidenceDocumented,
			Note:          "pi has no hooks; these variables belong to shell tools and extension-created child processes.",
		}
	default:
		return Environment{}
	}
}

// EnvironmentVariables returns a copy of the known variable names for agent.
// For Codex this is empty because no hook-specific export was verified; use
// EnvironmentFor for the evidence note and HookSupported flag.
func EnvironmentVariables(agent string) []string {
	return cloneStrings(EnvironmentFor(agent).Variables)
}

// Payload is a sparse, normalized view of a command-hook JSON object.
//
// Hook payloads are event-dependent and frequently omit fields. Merge a
// Payload into caller-owned state field by field; do not treat an empty field
// as an instruction to overwrite an existing value. Unknown JSON fields and
// unknown event names are tolerated and RawEventName preserves the latter.
type Payload struct {
	// Agent is the normalized agent name supplied to ParsePayload.
	Agent string
	// Event is the canonical event, or UnknownEvent for an unknown or
	// unsupported event name.
	Event Event
	// RawEventName is the original hook_event_name (or event fallback) from
	// the payload.
	RawEventName string
	// SessionID is session_id when present.
	SessionID string
	// CWD is cwd when present.
	CWD string
	// TranscriptPath is transcript_path when it is a string. A JSON null or
	// missing value is represented by an empty string.
	TranscriptPath string
	// Source is the event source, normally on SessionStart.
	Source string
	// StopHookActive is the stop-loop guard when present.
	StopHookActive bool
	// StopHookActiveSet distinguishes an absent boolean from an explicit false.
	StopHookActiveSet bool
	// TurnID is Codex's optional turn-scoped identifier.
	TurnID string
	// AgentID and AgentType identify a subagent.
	AgentID   string
	AgentType string
	// AgentTranscriptPath is the Codex subagent transcript path.
	AgentTranscriptPath string
	// LastAssistantMessage is available on stop-shaped payloads.
	LastAssistantMessage string
	// Trigger is available on compaction-shaped payloads.
	Trigger string
	// Prompt is available on UserPromptSubmit payloads.
	Prompt string
	// ToolName and ToolUseID are available on tool-shaped payloads.
	ToolName  string
	ToolUseID string
	// Reason is available on session-end payloads and some stop-shaped
	// payloads.
	Reason string
	// Model and PermissionMode are common Codex command-payload extras.
	Model          string
	PermissionMode string
}

// ErrPayloadNotObject indicates that the raw hook payload was valid JSON but
// was not a JSON object. Missing object fields are not errors.
var ErrPayloadNotObject = errors.New("hook payload is not a JSON object")

// ParsePayload parses one raw JSON hook payload for agent and normalizes its
// known fields. It only errors for malformed JSON or a valid non-object JSON
// value; missing, unknown, and incorrectly typed optional fields are ignored.
//
// The payload is intentionally sparse: callers merging it into a session
// record must update only fields that are known to be present, rather than
// replacing a whole record with zero values.
func ParsePayload(agent string, raw []byte) (Payload, error) {
	normalizedAgent := normalizeAgent(agent)
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return Payload{}, fmt.Errorf("parse hook payload: %w", err)
	}
	if object == nil {
		return Payload{}, ErrPayloadNotObject
	}

	payload := Payload{Agent: normalizedAgent}
	payload.SessionID = rawString(object, "session_id")
	payload.CWD = rawString(object, "cwd")
	payload.TranscriptPath = rawString(object, "transcript_path")
	payload.Source = rawString(object, "source")
	payload.TurnID = rawString(object, "turn_id")
	payload.AgentID = rawString(object, "agent_id")
	payload.AgentType = rawString(object, "agent_type")
	payload.AgentTranscriptPath = rawString(object, "agent_transcript_path")
	payload.LastAssistantMessage = rawString(object, "last_assistant_message")
	payload.Trigger = rawString(object, "trigger")
	payload.Prompt = rawString(object, "prompt")
	payload.ToolName = rawString(object, "tool_name")
	payload.ToolUseID = rawString(object, "tool_use_id")
	payload.Reason = rawString(object, "reason")
	payload.Model = rawString(object, "model")
	payload.PermissionMode = rawString(object, "permission_mode")
	if value, ok := rawBool(object, "stop_hook_active"); ok {
		payload.StopHookActive = value
		payload.StopHookActiveSet = true
	}

	payload.RawEventName = rawString(object, "hook_event_name")
	if payload.RawEventName == "" {
		// This fallback is deliberately permissive for forward-compatible
		// payloads from integrations that call the discriminator event.
		payload.RawEventName = rawString(object, "event")
	}
	if payload.RawEventName != "" {
		payload.Event, _ = ToCanonical(normalizedAgent, payload.RawEventName)
	}
	return payload, nil
}

// Parse is a short alias for ParsePayload.
func Parse(agent string, raw []byte) (Payload, error) {
	return ParsePayload(agent, raw)
}

// ParsePayloadFor is the same parser with raw first, which reads naturally at
// call sites that receive stdin bytes before selecting an agent.
func ParsePayloadFor(raw []byte, agent string) (Payload, error) {
	return ParsePayload(agent, raw)
}

func normalizeAgent(agent string) string {
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case AgentClaude, "claude-code":
		return AgentClaude
	case AgentCodex, "codex-cli":
		return AgentCodex
	case AgentPi, "pi-coding-agent":
		return AgentPi
	default:
		return strings.ToLower(strings.TrimSpace(agent))
	}
}

func cloneEvents(events []Event) []Event {
	if len(events) == 0 {
		return nil
	}
	return append([]Event(nil), events...)
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
}

func rawString(object map[string]json.RawMessage, key string) string {
	value, ok := object[key]
	if !ok {
		return ""
	}
	var result string
	if err := json.Unmarshal(value, &result); err != nil {
		return ""
	}
	return result
}

func rawBool(object map[string]json.RawMessage, key string) (bool, bool) {
	value, ok := object[key]
	if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return false, false
	}
	var result bool
	if err := json.Unmarshal(value, &result); err != nil {
		return false, false
	}
	return result, true
}
