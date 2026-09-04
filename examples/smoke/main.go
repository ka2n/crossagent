package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ka2n/crossagent"
	"github.com/ka2n/crossagent/paths"
)

func main() {
	agents, err := crossagent.Detect(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Detect warning: %v\n", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Getwd warning: %v\n", err)
		cwd = ""
	}
	resolver := paths.DefaultResolver()
	for _, agent := range agents {
		configPath, sessionDir := agentPaths(resolver, agent.Name, cwd)
		fmt.Printf("agent=%s found=%t version=%s capabilities=%s path=%s config=%s session_dir=%s\n",
			agent.Name, agent.Found, agent.Version, agent.Capabilities, agent.Path, configPath, sessionDir)
	}
}

func agentPaths(resolver paths.Resolver, name crossagent.Name, cwd string) (configPath, sessionDir string) {
	switch name {
	case crossagent.Claude:
		return resolver.ClaudeConfigPath(paths.User, cwd), resolver.ClaudeSessionDir(cwd)
	case crossagent.Codex:
		return resolver.CodexConfigPath(paths.User, cwd), resolver.CodexSessionRoot()
	case crossagent.Pi:
		return resolver.PiConfigPath(paths.User, cwd), resolver.PiSessionDir(cwd)
	default:
		return "", ""
	}
}
