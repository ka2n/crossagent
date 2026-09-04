package hooks

import (
	"reflect"
	"testing"
)

func TestEventMappingsRoundTrip(t *testing.T) {
	for _, agent := range []string{AgentClaude, AgentCodex} {
		t.Run(agent, func(t *testing.T) {
			for _, event := range Events(agent) {
				name, ok := FromCanonical(agent, event)
				if !ok {
					t.Fatalf("FromCanonical(%q, %q) failed", agent, event)
				}
				got, ok := ToCanonical(agent, name)
				if !ok || got != event {
					t.Fatalf("round trip %q -> %q -> %q (ok=%v)", event, name, got, ok)
				}
			}
		})
	}

	if got, ok := ToCanonical(AgentClaude, "Interrupt"); ok || got != UnknownEvent {
		t.Fatalf("Claude unknown event = %q, ok=%v", got, ok)
	}
	if got, ok := FromCanonical(AgentPi, EventSessionStart); ok || got != "" {
		t.Fatalf("pi mapping = %q, ok=%v", got, ok)
	}
	if surface := SurfaceFor(AgentPi); surface.HasHooks || len(surface.Events) != 0 || surface.Note == "" {
		t.Fatalf("pi surface does not explicitly report no hooks: %+v", surface)
	}
}

func TestCanonicalVocabularyAndCodexOperationalSubset(t *testing.T) {
	if got := len(CanonicalEvents()); got != 34 {
		t.Fatalf("canonical event count = %d, want 34", got)
	}
	if got := len(Events(AgentClaude)); got != 33 {
		t.Fatalf("Claude event count = %d, want 33", got)
	}
	if got := len(Events(AgentCodex)); got != 12 {
		t.Fatalf("Codex event count = %d, want 12", got)
	}
	if got := len(OperationalEvents(AgentCodex)); got != 5 {
		t.Fatalf("Codex operational event count = %d, want 5", got)
	}
	if !SupportsEvent(AgentCodex, EventInterrupt) {
		t.Fatal("Codex Interrupt should be in the declared vocabulary")
	}
	if IsOperationalEvent(AgentCodex, EventInterrupt) {
		t.Fatal("Codex Interrupt should not be reported as command-engine operational")
	}
	if got, want := SessionStartSources(AgentClaude), []string{SourceStartup, SourceResume, SourceClear, SourceCompact, SourceFork}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Claude SessionStart sources = %v, want %v", got, want)
	}
	if got := SessionStartSources(AgentPi); got != nil {
		t.Fatalf("pi SessionStart sources = %v, want nil", got)
	}
}

func TestTextReturnTable(t *testing.T) {
	claudeTextEvents := map[Event]bool{
		EventSessionStart:        true,
		EventUserPromptSubmit:    true,
		EventUserPromptExpansion: true,
		EventPreToolUse:          true,
		EventPostToolUse:         true,
		EventPostToolUseFailure:  true,
		EventPostToolBatch:       true,
		EventSubagentStart:       true,
		EventSubagentStop:        true,
		EventStop:                true,
		EventPostModelSwitch:     true,
		EventPreCompact:          true,
	}
	for _, event := range Events(AgentClaude) {
		got := TextReturnFor(AgentClaude, event)
		if got.CanReturnText != claudeTextEvents[event] {
			t.Errorf("Claude %s CanReturnText = %v, want %v", event, got.CanReturnText, claudeTextEvents[event])
		}
		if got.CanReturnText && (got.Field == "" || len(got.Fields) == 0) {
			t.Errorf("Claude %s has no text field: %+v", event, got)
		}
	}
	if got := TextReturnFor(AgentClaude, EventPreCompact); got.Field != FieldStdout || got.Evidence != EvidenceObserved {
		t.Fatalf("Claude PreCompact contract = %+v", got)
	}
	if got := TextReturnFor(AgentClaude, EventUserPromptSubmit); !reflect.DeepEqual(got.Fields, []string{FieldHookSpecificAdditionalContext, FieldStdout}) {
		t.Fatalf("Claude UserPromptSubmit fields = %v", got.Fields)
	}
	codexTextEvents := map[Event]bool{
		EventSessionStart:     true,
		EventSubagentStart:    true,
		EventUserPromptSubmit: true,
		EventPreToolUse:       true,
		EventPostToolUse:      true,
	}
	for _, event := range Events(AgentCodex) {
		got := TextReturnFor(AgentCodex, event)
		if got.CanReturnText != codexTextEvents[event] {
			t.Errorf("Codex %s CanReturnText = %v, want %v", event, got.CanReturnText, codexTextEvents[event])
		}
		if got.CanReturnText && got.Field != FieldHookSpecificAdditionalContext {
			t.Errorf("Codex %s text contract = %+v", event, got)
		}
	}
	if got := OutputFor(AgentClaude, EventStop); got.DecisionField != FieldDecision || got.ReasonField != FieldReason {
		t.Fatalf("Claude Stop output placement = %+v", got)
	}
	if got := OutputFor(AgentCodex, EventSubagentStop); got.DecisionField != FieldDecision || got.ReasonField != FieldReason || got.Evidence != EvidenceConfirmedOSS {
		t.Fatalf("Codex SubagentStop output placement = %+v", got)
	}
}

func TestPayloadFieldsAndEnvironment(t *testing.T) {
	claude := PayloadFieldsFor(AgentClaude, EventSessionStart)
	if claude.SessionID != "session_id" || claude.CWD != "cwd" || claude.TranscriptPath != "transcript_path" || claude.Source != "source" {
		t.Fatalf("Claude SessionStart fields = %+v", claude)
	}
	claudeStop := PayloadFieldsFor(AgentClaude, EventStop)
	if claudeStop.StopHookActive != "stop_hook_active" || claudeStop.LastAssistantMessage != "last_assistant_message" {
		t.Fatalf("Claude Stop fields = %+v", claudeStop)
	}
	codexSubagentStop := PayloadFieldsFor(AgentCodex, EventSubagentStop)
	if codexSubagentStop.AgentTranscriptPath != "agent_transcript_path" || codexSubagentStop.StopHookActive != "stop_hook_active" {
		t.Fatalf("Codex SubagentStop fields = %+v", codexSubagentStop)
	}
	if got := EnvironmentVariables(AgentClaude); !reflect.DeepEqual(got, []string{
		EnvClaudeSessionID,
		EnvClaudeMessagingSocket,
		EnvClaudeMessagingToken,
		EnvClaudeChildSession,
	}) {
		t.Fatalf("Claude environment variables = %v", got)
	}
	codexEnv := EnvironmentFor(AgentCodex)
	if !codexEnv.HookSupported || len(codexEnv.Variables) != 0 || codexEnv.Note == "" {
		t.Fatalf("Codex environment facts = %+v", codexEnv)
	}
	piEnv := EnvironmentFor(AgentPi)
	if piEnv.HookSupported || len(piEnv.Variables) != 7 {
		t.Fatalf("pi environment facts = %+v", piEnv)
	}
}

func TestAsyncSemantics(t *testing.T) {
	claude := AsyncSemanticsFor(AgentClaude, EventSessionStart)
	if !claude.Supported || !claude.FireAndForget || !claude.ControlRequiresSynchronous || !claude.TextMayArriveLater {
		t.Fatalf("Claude async semantics = %+v", claude)
	}
	codex := AsyncSemanticsFor(AgentCodex, EventStop)
	if !codex.Supported || !codex.FireAndForget || !codex.ControlRequiresSynchronous || codex.TextMayArriveLater {
		t.Fatalf("Codex async semantics = %+v", codex)
	}
	if pi := AsyncSemanticsFor(AgentPi, EventSessionStart); pi.Supported {
		t.Fatalf("pi async semantics = %+v", pi)
	}
}

func TestParsePayloads(t *testing.T) {
	tests := []struct {
		name  string
		agent string
		raw   string
		check func(t *testing.T, payload Payload)
	}{
		{
			name:  "Claude SessionStart",
			agent: AgentClaude,
			raw: `{
				"session_id":"claude-1",
				"cwd":"/work/project",
				"transcript_path":"/home/u/.claude/projects/-work-project/claude-1.jsonl",
				"hook_event_name":"SessionStart",
				"source":"resume",
				"unexpected":{"future":true}
			}`,
			check: func(t *testing.T, got Payload) {
				if got.Event != EventSessionStart || got.SessionID != "claude-1" || got.CWD != "/work/project" || got.Source != "resume" || got.TranscriptPath == "" {
					t.Fatalf("payload = %+v", got)
				}
			},
		},
		{
			name:  "Claude Stop",
			agent: AgentClaude,
			raw:   `{"hook_event_name":"Stop","session_id":"claude-1","stop_hook_active":false,"last_assistant_message":"done"}`,
			check: func(t *testing.T, got Payload) {
				if got.Event != EventStop || !got.StopHookActiveSet || got.StopHookActive || got.LastAssistantMessage != "done" {
					t.Fatalf("payload = %+v", got)
				}
			},
		},
		{
			name:  "Claude SubagentStop",
			agent: AgentClaude,
			raw:   `{"hook_event_name":"SubagentStop","agent_id":"a-1","agent_transcript_path":"/tmp/a-1.jsonl","agent_type":"Explore","stop_hook_active":true}`,
			check: func(t *testing.T, got Payload) {
				if got.Event != EventSubagentStop || got.AgentID != "a-1" || got.AgentType != "Explore" || got.AgentTranscriptPath != "/tmp/a-1.jsonl" || !got.StopHookActive {
					t.Fatalf("payload = %+v", got)
				}
			},
		},
		{
			name:  "Codex SessionStart",
			agent: AgentCodex,
			raw:   `{"session_id":"codex-1","cwd":"/work/project","transcript_path":null,"hook_event_name":"SessionStart","source":"startup","model":"gpt-5","permission_mode":"default"}`,
			check: func(t *testing.T, got Payload) {
				if got.Event != EventSessionStart || got.SessionID != "codex-1" || got.TurnID != "" || got.TranscriptPath != "" || got.Source != "startup" || got.Model != "gpt-5" {
					t.Fatalf("payload = %+v", got)
				}
			},
		},
		{
			name:  "Codex PreToolUse",
			agent: AgentCodex,
			raw:   `{"session_id":"codex-1","cwd":"/work/project","hook_event_name":"PreToolUse","tool_name":"Bash","tool_use_id":"tool-1","tool_input":{"command":"go test ./..."}}`,
			check: func(t *testing.T, got Payload) {
				if got.Event != EventPreToolUse || got.SessionID != "codex-1" || got.CWD != "/work/project" || got.ToolName != "Bash" || got.ToolUseID != "tool-1" {
					t.Fatalf("payload = %+v", got)
				}
			},
		},
		{
			name:  "Codex PostToolUse",
			agent: AgentCodex,
			raw:   `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"tool-1","tool_response":{"output":"ok"}}`,
			check: func(t *testing.T, got Payload) {
				if got.Event != EventPostToolUse || got.RawEventName != "PostToolUse" || got.ToolName != "Bash" || got.ToolUseID != "tool-1" {
					t.Fatalf("payload = %+v", got)
				}
			},
		},
		{
			name:  "Codex PreCompact",
			agent: AgentCodex,
			raw:   `{"hook_event_name":"PreCompact","trigger":"manual"}`,
			check: func(t *testing.T, got Payload) {
				if got.Event != EventPreCompact || got.Trigger != "manual" {
					t.Fatalf("payload = %+v", got)
				}
			},
		},
		{
			name:  "Codex SubagentStop",
			agent: AgentCodex,
			raw:   `{"hook_event_name":"SubagentStop","agent_id":"a-2","agent_type":"worker","agent_transcript_path":"/tmp/a-2.jsonl","stop_hook_active":true,"last_assistant_message":"finished"}`,
			check: func(t *testing.T, got Payload) {
				if got.Event != EventSubagentStop || got.AgentTranscriptPath != "/tmp/a-2.jsonl" || !got.StopHookActive || got.LastAssistantMessage != "finished" {
					t.Fatalf("payload = %+v", got)
				}
			},
		},
		{
			name:  "Codex SessionEnd",
			agent: AgentCodex,
			raw:   `{"hook_event_name":"SessionEnd","session_id":"codex-1","reason":"other"}`,
			check: func(t *testing.T, got Payload) {
				if got.Event != EventSessionEnd || got.SessionID != "codex-1" {
					t.Fatalf("payload = %+v", got)
				}
			},
		},
		{
			name:  "missing and wrong optional fields",
			agent: AgentClaude,
			raw:   `{"hook_event_name":"Stop","stop_hook_active":"not-a-bool","cwd":42}`,
			check: func(t *testing.T, got Payload) {
				if got.Event != EventStop || got.StopHookActiveSet || got.CWD != "" {
					t.Fatalf("payload = %+v", got)
				}
			},
		},
		{
			name:  "unknown Claude event",
			agent: AgentClaude,
			raw:   `{"hook_event_name":"FutureEvent","session_id":"future"}`,
			check: func(t *testing.T, got Payload) {
				if got.Event != UnknownEvent || got.RawEventName != "FutureEvent" || got.SessionID != "future" {
					t.Fatalf("payload = %+v", got)
				}
			},
		},
		{
			name:  "event unsupported by agent",
			agent: AgentClaude,
			raw:   `{"hook_event_name":"Interrupt"}`,
			check: func(t *testing.T, got Payload) {
				if got.Event != UnknownEvent || got.RawEventName != "Interrupt" {
					t.Fatalf("payload = %+v", got)
				}
			},
		},
		{
			name:  "pi has no hook mapping",
			agent: AgentPi,
			raw:   `{"hook_event_name":"SessionStart","session_id":"pi-1","cwd":"/work/project"}`,
			check: func(t *testing.T, got Payload) {
				if got.Event != UnknownEvent || got.RawEventName != "SessionStart" || got.SessionID != "pi-1" {
					t.Fatalf("payload = %+v", got)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePayload(tt.agent, []byte(tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, got)
		})
	}
}

func TestParsePayloadErrorsOnlyForOuterJSON(t *testing.T) {
	for _, raw := range []string{`{`, `[]`, `null`, `"text"`, `42`} {
		if _, err := ParsePayload(AgentClaude, []byte(raw)); err == nil {
			t.Errorf("ParsePayload(%s) returned nil error", raw)
		}
	}
	got, err := ParsePayload(AgentClaude, []byte(`{"hook_event_name":"SessionStart","unknown":[1,2,3]}`))
	if err != nil || got.Event != EventSessionStart {
		t.Fatalf("unknown-field payload = %+v, err=%v", got, err)
	}
}
