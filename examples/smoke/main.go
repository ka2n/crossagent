package main

import (
	"context"
	"fmt"
	"os"

	"github.com/ka2n/crossagent"
)

func main() {
	agents, err := crossagent.Detect(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Detect warning: %v\n", err)
	}
	for _, agent := range agents {
		fmt.Printf("agent=%s found=%t version=%s capabilities=%s path=%s\n", agent.Name, agent.Found, agent.Version, agent.Capabilities, agent.Path)
	}
}
