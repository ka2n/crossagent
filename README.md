# crossagent

`crossagent` is a small, dependency-free Go library for tooling that spans
Claude Code, Codex, and pi.

It covers three fact-oriented capabilities:

1. **Detect** installed agents and their versions.
2. **Resolve** session, transcript, and configuration locations without
   filesystem discovery.
3. **Normalize** hook events, payload fields, output channels, and async
   semantics.

The pure path and hook packages provide the facts needed for session-location
discovery and hook configuration management. Writing configuration files and
installing hooks are deliberately left to a later mutation layer.

## Scope

Process liveness and cross-namespace PID resolution are deliberately out of
scope: they are Linux-only, depend on undocumented `/proc` details, and are
impossible from inside a sandbox without cooperation from outside it.

Message delivery is also not an operation in this library. Claude's socket,
Codex's queue command, and pi's in-process extensions have different
authentication, lifecycle, and safety requirements; shelling out or mutating a
live session would make this facts library responsible for side effects it
cannot safely coordinate. Codex's documented command is retained as exported
capability data only: the library never executes it.

The encoded facts are based on `ka2n/agents-runtime-notes`, with comments and
API metadata distinguishing documented, OSS-confirmed, and locally observed
behavior.

## Detection

`crossagent.Detect` checks PATH for Claude Code, Codex, and pi, runs each found
executable with `--version`, and parses its version output leniently. Missing
executables are reported as `Found: false` rather than returned as errors. The
detector is injectable, so tests and embedding applications can provide their
own PATH lookup and command runner.

| Agent | Hooks | External command data | JS extensions | RPC mode |
| --- | ---: | ---: | ---: | ---: |
| Claude Code | yes | no | no | no |
| Codex | yes | `codex queue --thread {thread_id} --message {message}` | no | no |
| pi | no | no | yes | yes |

The `ExternalMessageCommand` on the Codex `Agent` only describes an argv
template. Its `Command` and `Arguments` methods build slices and do not start a
process.

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

`hooks.ParsePayload` accepts sparse JSON objects and an agent name. Unknown
fields and event names are preserved/tolerated, while malformed JSON and
non-object JSON are errors. Missing fields remain zero values; callers merging
a payload into state should update fields individually rather than replacing a
whole record.

## Platform and dependencies

The library uses only the Go standard library and `go.mod` has no third-party
dependencies. It is intended to build with `CGO_ENABLED=0`.
