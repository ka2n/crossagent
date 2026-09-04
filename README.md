# crossagent

`crossagent` is a small Go library for tooling that spans coding-agent CLIs
such as Claude Code, Codex, and pi.

The long-term surface has four capabilities:

1. **Detect** which agents are installed.
2. **Discover** their sessions.
3. **Inject** a message into a running session.
4. **Install and manage** hook configuration.

This first pass ships **detection and process identity only**. Session
discovery, message injection, and hook management are deliberately not
implemented yet.

## The PID namespace problem

A hook can run inside an agent's PID namespace. In that process,
`os.Getppid()` is a namespace-local PID: a Claude Code process can be PID `2`
inside its namespace, while host PID `2` is `kthreadd`. Treating that number as
a host PID can therefore report that the agent is dead or monitor an unrelated
process.

The hook must collect the identity while it is still inside the namespace:

- the parent PID as seen there;
- the inode from `/proc/<pid>/ns/pid`; and
- the process start-time ticks from `/proc/<pid>/stat`.

The host-side resolver uses `/proc/<host-pid>/status`'s `NSpid:` line as the
translation table. It then checks the namespace inode and start-time ticks.
Start-time ticks make a reused numeric PID fail closed. These facts cannot be
reconstructed reliably after the hook has left the namespace, which is why
`hostpid.Collect` belongs in the hook producer and `hostpid.Resolver` belongs
in the host observer.

The stat parser deliberately finds the text after the **last** `)` before
splitting fields. Linux permits spaces and parentheses in a command name, so
splitting after the first `)` shifts `ppid` and `starttime`.

## Detection

Detection currently describes these observed integration surfaces:

| Agent | Hooks | External message queue | JS extensions | RPC mode |
| --- | ---: | ---: | ---: | ---: |
| Claude Code | yes | no | no | no |
| Codex | yes | yes | no | no |
| pi | no | no | yes | yes |

Claude and Codex hook support and the Codex queue command are established by
their documented CLI integration surfaces. pi's `--extension` and `--mode rpc`
flags establish its two capabilities. The library does not infer capabilities
from terminal scraping.

```go
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ka2n/crossagent"
	"github.com/ka2n/crossagent/hostpid"
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

	identity, err := hostpid.Collect(os.Getpid())
	if err != nil {
		panic(err)
	}
	hostPID, ok := hostpid.DefaultResolver().Resolve(identity)
	fmt.Printf("identity=%+v host_pid=%d resolved=%t\n", identity, hostPID, ok)
}
```

A real-world smoke program is also available:

```sh
go run ./examples/smoke
```

## Platform and scope

The package uses the standard library and builds with `CGO_ENABLED=0`.
Linux-specific PID-namespace inode and pidfd code is isolated in `procfs` and
`hostpid`; non-Linux builds return explicit unsupported-operation errors for
those pieces rather than panicking. Detection itself is platform-independent.

Credit for the ported process-identity and pidfd patterns goes to
[`way-island`](https://github.com/ka2n/way-island), an earlier project by the
same owner.
