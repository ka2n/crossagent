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

### Resolution direction

PID namespace translation is directional:

- In the **same namespace**, no translation is needed.
- A **host consumer with an agent inside a namespace** (Docker, jai,
  systemd-nspawn, or similar) can resolve it through `NSpid`. This is the case
  this package exists for.
- A **consumer inside a sandbox with an agent outside it** cannot resolve the
  host PID: the outer process is not visible through that consumer's `/proc`.
  This is an unsupported direction and fails closed.
- A consumer in **container A with an agent in container B** likewise cannot
  resolve the sibling namespace.

`NSpid` is directional too: read inside a namespace it usually has only the
local column, while an outer consumer sees the columns needed for translation.
The field was introduced in Linux 4.1. `/proc/<pid>/status` may also be
unreadable for another user's process, so this library reports translation as
unavailable rather than guessing. Use `hostpid.Resolver.CanResolve` to
separate an unsupported direction from a namespace that is visible but whose
individual process is not found.

No environment variable belonging to a particular sandbox tool is consulted;
in particular, the library does not read `JAI_JAIL`, `/.dockerenv`,
`/run/.containerenv`, `$container`, or cgroup paths. Those generic markers are
not reliable: on the measured machine the marker files and variable were
absent, while `/proc/1/cgroup` exposed the host cgroup path because the cgroup
namespace was not unshared. The recorded namespace inode is authoritative.
If it is unavailable, a low PID (below
`hostpid.SuspiciousPIDThreshold`) is only a documented fallback heuristic, and
callers can use `ProcessIdentity.AssumeNamespaced` when they genuinely know
more.

### Recovering the impossible direction

`hostpid.ErrUnsupportedDirection` is the signal to try an environment-specific
backend, not to treat the process as merely missing. The possible ways to
restore that direction all require cooperation from outside the sandbox:

- A monitoring container can mount the host procfs, conventionally at
  `/host/proc`. `hostpid.NewResolver(procfs.New("/host/proc"))` is the whole
  configuration-only fix; the constructor wires all five readers from that
  `ProcFS`, while its public reader fields remain replaceable for a specialized
  backend.
- A Docker or podman socket can provide the host PID directly, for example
  with `docker inspect --format '{{.State.Pid}}'`.
- Kubernetes `hostPID: true` makes the consumer share the host PID namespace,
  so no translation is needed.
- A host-side helper reached over a bind-mounted socket can perform the lookup.
  This is the architecture used by the source `way-island` project: a daemon
  on the host and hooks inside the agent namespace.

If there is no mounted host procfs, socket, host-PID sharing, or other channel
outside the sandbox, the information simply does not exist inside it. That is
not an implementation gap, and this library does not implement any of those
backends in this pass.

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
