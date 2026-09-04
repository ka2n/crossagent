package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/ka2n/crossagent"
	"github.com/ka2n/crossagent/hostpid"
)

func main() {
	agents, err := crossagent.Detect(context.Background())
	if err != nil {
		fmt.Printf("Detect warning: %v\n", err)
	}
	for _, agent := range agents {
		fmt.Printf("agent=%s found=%t version=%s capabilities=%s path=%s\n", agent.Name, agent.Found, agent.Version, agent.Capabilities, agent.Path)
	}

	process := exec.Command("sleep", "2")
	if err := process.Start(); err != nil {
		panic(err)
	}
	defer func() {
		_ = process.Process.Kill()
		_ = process.Wait()
	}()

	identity, err := hostpid.Collect(process.Process.Pid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "collect sleep identity: %v\n", err)
		os.Exit(1)
	}
	hostPID, ok := hostpid.DefaultResolver().Resolve(identity)
	fmt.Printf("sleep identity_pid=%d namespace_inode=%d start_time_ticks=%d resolved_host_pid=%d resolved=%t\n", identity.PID, identity.NamespaceInode, identity.StartTimeTicks, hostPID, ok)
	if !ok {
		os.Exit(1)
	}
}
