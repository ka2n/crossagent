# crossagent

`crossagent` is a small Go library for tooling that spans coding-agent CLIs
such as Claude Code, Codex, and pi.

The eventual library covers four capabilities:

1. **Detect** which agents are installed.
2. **Discover** where their sessions live.
3. **Inject** a message into a running session.
4. **Install and manage** hook configuration.

## Scope

Detection is available today, and session-location discovery is the next
addition. Message injection and hook management are later additions. Process
liveness and cross-namespace PID resolution are deliberately out of scope:
they are Linux-only, depend on undocumented `/proc` details, and are impossible
from inside a sandbox without cooperation from outside it. This is intentional,
not a missing implementation.

## Detection

`crossagent.Detect` checks the PATH for Claude Code, Codex, and pi, runs each
found executable with `--version`, and parses its version output leniently.
Missing executables are reported as `Found: false` rather than returned as
errors. The detector is injectable, so tests and embedding applications can
provide their own PATH lookup and command runner.

The known, observable integration surfaces are:

| Agent | Hooks | External message queue | JS extensions | RPC mode |
| --- | ---: | ---: | ---: | ---: |
| Claude Code | yes | no | no | no |
| Codex | yes | yes | no | no |
| pi | no | no | yes | yes |

These capabilities describe documented CLI surfaces: Claude Code and Codex
support hooks; Codex supports
`codex queue --thread <id> --message <text>`; and pi exposes `--extension`/`-e`
and `--mode rpc`. The library does not infer capabilities from terminal
scraping.

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

## Platform and dependencies

The detection package uses only the Go standard library and `go.mod` has no
third-party dependencies. The project is intended to build with
`CGO_ENABLED=0`.
