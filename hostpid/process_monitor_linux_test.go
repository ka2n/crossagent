//go:build linux

package hostpid

import (
	"os/exec"
	"testing"
	"time"
)

func TestProcessMonitorReportsExit(t *testing.T) {
	cmd := exec.Command("sleep", "0.2")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	identity, err := Collect(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	monitor, err := NewProcessMonitor(identity, func() { close(exited) })
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Close()

	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("process monitor did not report exit")
	}
}

func TestProcessMonitorCloseSuppressesExit(t *testing.T) {
	cmd := exec.Command("sleep", "0.2")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	identity, err := Collect(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	monitor, err := NewProcessMonitor(identity, func() { close(exited) })
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.Close(); err != nil {
		t.Fatal(err)
	}
	if err := monitor.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-exited:
		t.Fatal("process monitor invoked callback after Close")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestPIDFDMonitorCloseToleratesEBADF(t *testing.T) {
	monitor := &pidfdProcessMonitor{
		fd:   -1,
		done: make(chan struct{}),
	}
	if err := monitor.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil for EBADF", err)
	}
	if err := monitor.Close(); err != nil {
		t.Fatalf("second Close() = %v, want nil", err)
	}
}
