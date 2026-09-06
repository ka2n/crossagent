# crossagent

`crossagent` is a small, dependency-free Go library for tooling that spans
Claude Code, Codex, and pi.

It covers three fact-oriented capabilities:

1. **Detect** installed agents and their versions.
2. **Resolve** session, transcript, and configuration locations without
   filesystem discovery.
3. **Normalize and manage** hook events, payload fields, output channels,
   async semantics, and safe declarative configuration changes.

The path and hook packages provide the facts needed for session-location
discovery and safe hook configuration management. Hook configuration changes
are plan-first, ownership-aware, atomic, and opt-in; live message delivery is
not part of this library.

## Scope

Process liveness and cross-namespace PID resolution are deliberately out of
scope: they are Linux-only, depend on undocumented `/proc` details, and are
impossible from inside a sandbox without cooperation from outside it.

Message delivery is also not an operation in this library. Claude's socket,
Codex's queue command, and pi's in-process extensions have different
authentication, lifecycle, and safety requirements; shelling out or mutating a
live session would make this facts library responsible for side effects it
cannot safely coordinate. There is no delivery API here because only Codex
accepts an external message from an arbitrary process: Claude Code's session
inbox socket accepts a raw write but never delivers it, since a delivered peer
message carries the sender's own socket as identity, and pi offers only
in-process injection from a JS extension. Delivery therefore belongs to the
tool that owns the message store.

The encoded facts are based on `ka2n/agents-runtime-notes`, with comments and
API metadata distinguishing documented, OSS-confirmed, and locally observed
behavior.

## Agent names

`crossagent/agent` is a leaf package holding the name vocabulary as a typed
`agent.Name`, so no other package accepts a bare string for an agent:

```go
import "github.com/ka2n/crossagent/agent"

agent.Claude          // "claude"
agent.Names()         // every canonical name, in a stable order
agent.Parse("Codex")  // agent.Codex, nil
agent.Parse("gemini") // "", wraps agent.ErrUnknown
```

`Parse` is the boundary for external input such as a CLI flag: it trims
surrounding whitespace, folds ASCII case, and accepts one long-form alias per
agent (`claude-code`, `codex-cli`, `pi-coding-agent`). Everything downstream
takes an already-parsed `agent.Name` and does no normalization of its own. The
root package re-exports the type and the three constants, so
`crossagent.Codex` works without a second import; enumerating and parsing stay
in the `agent` package, where the vocabulary lives.

## Detection

`crossagent.Detect` checks PATH for Claude Code, Codex, and pi, runs each found
executable with `--version`, and parses its version output leniently. Missing
executables are reported as `Found: false` rather than returned as errors. The
detector is injectable, so tests and embedding applications can provide their
own PATH lookup and command runner.

The columns below are the `Capabilities` bits, in declaration order:
`CapabilityHooks`, `CapabilityExternalMessageQueue`, `CapabilityExtensions`,
and `CapabilityRPCMode`.

| Agent | Hooks | External message | JS extensions | RPC mode |
| --- | ---: | ---: | ---: | ---: |
| Claude Code | yes | no | no | no |
| Codex | yes | yes | no | no |
| pi | no | no | yes | yes |

```go
package main

import (
	"context"
	"fmt"

	"github.com/ka2n/crossagent"
)

func main() {
	agents, err := crossagent.Detect(context.Background())
	if err != nil {
		panic(err)
	}
	for _, agent := range agents {
		fmt.Printf("%s found=%t version=%s capabilities=%s\n",
			agent.Name, agent.Found, agent.Version, agent.Capabilities)
	}
}
```

A real-world smoke program is also available:

```sh
go run ./examples/smoke
```

## Path facts

`crossagent/paths` is pure path construction. Inject a home directory and
optional environment lookup for deterministic tests:

```go
import "github.com/ka2n/crossagent/paths"

resolver := paths.NewResolver("/home/me")
config := resolver.ClaudeConfigPath(paths.User, "/work/project")
sessions := resolver.ClaudeSessionDir("/work/project")
```

It honors `CLAUDE_CONFIG_DIR`, `CODEX_HOME`,
`PI_CODING_AGENT_DIR`, and `PI_CODING_AGENT_SESSION_DIR`. Claude and pi cwd
encoders are one-way because their on-disk keys are lossy. Codex transcript
paths include a timestamp, so the resolver returns a search pattern unless the
start time is known.

## Hook facts

`crossagent/hooks` maps Claude Code's event vocabulary and Codex's OSS-declared
12-event vocabulary to one canonical `hooks.Event` type. It also reports the
currently inspected Codex command-engine subset, model-visible text fields,
top-level stop `decision`/`reason` placement, payload field names, environment
facts, and async behavior. pi is explicitly represented as having no
declarative hook system; its supported integration surface is JavaScript
extensions.

`hooks.ParsePayload` accepts sparse JSON objects and an `agent.Name`. Unknown
fields and event names are preserved/tolerated, while malformed JSON and
non-object JSON are errors. Missing fields remain zero values; callers merging
a payload into state should update fields individually rather than replacing a
whole record.

`hooks.ConfigManager` is the mutation layer for declarative Claude settings.
Its ownership model has three layers: durable explicit markers are authoritative
where an agent preserves them; the command predicate is authoritative where
markers are not durable; and on marker-durable agents, predicate matches without
a marker are reported as `UnmarkedOwnershipError` until the caller explicitly
sets `AdoptUnmarked`. New entries carry `installedBy` and a stable per-tool ID
marker only when `MarkerSupportFor(agent).Durable` is true.

Claude Code is the measured reason for the distinction: on 2026-09-04,
version 2.1.259 fired entries containing the unknown marker keys; on 2026-09-06,
version 2.1.260 stripped those keys during a `/model` settings write while
known fields (`async`, `timeout`) survived. Claude therefore writes no marker
fields and uses the predicate for ownership,
so marker loss is not adoption churn. Codex's compatible JSON shape is
intentionally gated until its marker tolerance is established. Call
`PlanInstall` or `PlanUninstall`, present the returned summary and unified diff,
then call `Apply` after any caller-owned confirmation. The manager preserves
unrelated keys and wrapper fields, verifies the prospective merge, takes a
per-tool backup before the first write, writes atomically, and can run an
injected self-check probe.

## Platform and dependencies

The library uses only the Go standard library and `go.mod` has no third-party
dependencies. It is intended to build with `CGO_ENABLED=0`.
