package crossagent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Capabilities is a bitset of capabilities exposed by a coding-agent CLI.
// A bit describes an observable integration surface, not an estimate of what
// could be implemented by driving the agent's terminal UI.
type Capabilities uint32

// Capability is an alias that makes APIs accepting one capability read
// naturally while retaining the compact Capabilities bitset type.
type Capability = Capabilities

const (
	// CapabilityHooks means that the agent can invoke configured lifecycle
	// hooks. Claude Code and Codex both document hook configuration that runs
	// external commands for agent events; pi does not expose such hooks.
	CapabilityHooks Capabilities = 1 << iota

	// CapabilityExternalMessageQueue means that the CLI documents a command an
	// external process can use to message a running session. Codex documents
	// such a command; the other two CLIs do not. See the agent's own
	// documentation for the exact syntax, which this library does not encode.
	CapabilityExternalMessageQueue

	// CapabilityExtensions means that the CLI can load JavaScript extensions.
	// pi exposes extension discovery and the repeatable --extension/-e option.
	CapabilityExtensions

	// CapabilityRPCMode means that the CLI has an RPC output/transport mode.
	// pi exposes --mode rpc in its command-line interface.
	CapabilityRPCMode
)

// Has reports whether c contains capability.
func (c Capabilities) Has(capability Capability) bool {
	return capability != 0 && c&capability == capability
}

// Contains is an alias for Has.
func (c Capabilities) Contains(capability Capability) bool {
	return c.Has(capability)
}

// String returns the names of the set capabilities, separated by commas.
func (c Capabilities) String() string {
	var names []string
	for _, capability := range []struct {
		bit  Capabilities
		name string
	}{
		{CapabilityHooks, "hooks"},
		{CapabilityExternalMessageQueue, "external_message_queue"},
		{CapabilityExtensions, "extensions"},
		{CapabilityRPCMode, "rpc_mode"},
	} {
		if c.Has(capability.bit) {
			names = append(names, capability.name)
		}
	}
	return strings.Join(names, ",")
}

// Agent describes one of the coding-agent CLIs known to this package.
type Agent struct {
	// Name is the canonical crossagent name: claude, codex, or pi.
	Name string
	// Binary is the executable name looked up on PATH.
	Binary string
	// Path is the resolved executable path. It is empty when Found is false.
	Path string
	// Found reports whether Binary was found on PATH.
	Found bool
	// Version is the first version-like value printed by --version.
	// It is empty when the executable does not print a parseable version.
	Version string
	// Capabilities describes the known integration surfaces of the agent.
	Capabilities Capabilities
}

// LookPathFunc looks up an executable. It defaults to exec.LookPath.
type LookPathFunc func(file string) (string, error)

// CommandRunner runs a command and returns its combined stdout and stderr.
// It defaults to the standard library's exec.CommandContext runner.
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Detector detects the known coding-agent CLIs. Set LookPath and Run to inject
// a filesystem and command runner in tests or in an embedding application.
type Detector struct {
	LookPath LookPathFunc
	Run      CommandRunner
}

// NewDetector returns a detector using the host's PATH and os/exec.
func NewDetector() Detector {
	return Detector{
		LookPath: exec.LookPath,
		Run:      runCommand,
	}
}

// DefaultDetector is used by the package-level Detect and DetectOne helpers.
// Applications that need custom lookup or command execution can use Detector
// directly instead of changing this variable.
var DefaultDetector = NewDetector()

// ErrUnknownAgent reports a name that is not part of the first-pass agent set.
var ErrUnknownAgent = errors.New("unknown agent")

type agentSpec struct {
	name         string
	binary       string
	capabilities Capabilities
}

var agentSpecs = []agentSpec{
	{
		name:   "claude",
		binary: "claude",
		// Claude's hook support is documented by its settings/hook events.
		// Its CLI help has no external queue command, so no queue bit is set
		// here.
		capabilities: CapabilityHooks,
	},
	{
		name:   "codex",
		binary: "codex",
		// Codex has documented hook configuration and a documented command
		// that lets an external process message a running session.
		capabilities: CapabilityHooks | CapabilityExternalMessageQueue,
	},
	{
		name:   "pi",
		binary: "pi",
		// pi's --extension/-e and --mode rpc flags establish these two
		// surfaces. It has neither hooks nor an external queue CLI.
		capabilities: CapabilityExtensions | CapabilityRPCMode,
	},
}

// Detect reports all known agents in stable order. A missing executable is a
// normal result and is returned as Agent{Found:false}; errors from a version
// command are returned after all agents have been attempted.
func (d Detector) Detect(ctx context.Context) ([]Agent, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	agents := make([]Agent, 0, len(agentSpecs))
	var errs []error
	for _, spec := range agentSpecs {
		agent, err := d.detectOne(ctx, spec)
		agents = append(agents, agent)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return agents, errors.Join(errs...)
}

// DetectOne reports one known agent. Missing executables are not errors.
func (d Detector) DetectOne(ctx context.Context, name string) (Agent, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	spec, ok := specForName(name)
	if !ok {
		return Agent{}, fmt.Errorf("%w: %q", ErrUnknownAgent, name)
	}
	return d.detectOne(ctx, spec)
}

// Detect reports all known agents using DefaultDetector.
func Detect(ctx context.Context) ([]Agent, error) {
	return DefaultDetector.Detect(ctx)
}

// DetectOne reports one known agent using DefaultDetector.
func DetectOne(ctx context.Context, name string) (Agent, error) {
	return DefaultDetector.DetectOne(ctx, name)
}

func (d Detector) detectOne(ctx context.Context, spec agentSpec) (Agent, error) {
	agent := Agent{
		Name:         spec.name,
		Binary:       spec.binary,
		Capabilities: spec.capabilities,
	}
	lookPath := d.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	path, err := lookPath(spec.binary)
	if err != nil || strings.TrimSpace(path) == "" {
		// LookPath errors are deliberately treated as "not found". The
		// standard implementation uses exec.ErrNotFound, and an injected
		// lookup has no useful distinction for the detector to expose.
		return agent, nil
	}
	agent.Path = path
	agent.Found = true

	run := d.Run
	if run == nil {
		run = runCommand
	}
	output, err := run(ctx, path, "--version")
	if err != nil {
		return agent, fmt.Errorf("%s --version: %w", spec.binary, err)
	}
	agent.Version = ParseVersion(output)
	return agent, nil
}

func specForName(name string) (agentSpec, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, spec := range agentSpecs {
		if spec.name == name {
			return spec, true
		}
	}
	return agentSpec{}, false
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

var versionPattern = regexp.MustCompile(`(?i)(?:^|[^[:alnum:]])v?([0-9]+(?:\.[0-9]+)+(?:[-+][0-9A-Za-z.-]+)?)`)

// ParseVersion extracts the first version-like value from CLI output. It is
// intentionally lenient about prefixes such as "codex-cli" and "v" because
// the three supported CLIs do not use the same --version presentation.
func ParseVersion(output []byte) string {
	match := versionPattern.FindSubmatch(output)
	if len(match) < 2 {
		return ""
	}
	return string(match[1])
}
