package sessioncontext

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func git(t *testing.T, cwd string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", cwd}, args...)...)
	cmd.Env = gitEnvironment(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, output, err)
	}
	return strings.TrimSpace(string(output))
}

func TestGitSnapshots(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init", "-b", "topic")
	snapshot := Collect(context.Background(), root, Options{})
	if snapshot.Git == nil || snapshot.Git.Branch != "topic" || snapshot.Git.HEAD != "" || snapshot.Git.Detached {
		t.Fatalf("unborn: %+v", snapshot.Git)
	}
	git(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "initial")
	head := git(t, root, "rev-parse", "HEAD")
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "missing"))
	snapshot = Collect(context.Background(), sub, Options{})
	if snapshot.Git == nil || snapshot.Git.WorktreeRoot != root || snapshot.Git.HEAD != head || snapshot.Git.Branch != "topic" {
		t.Fatalf("subdir: %+v", snapshot.Git)
	}
	worktree := filepath.Join(t.TempDir(), "linked")
	git(t, root, "worktree", "add", "-b", "linked", worktree)
	snapshot = Collect(context.Background(), worktree, Options{})
	if snapshot.Git == nil || snapshot.Git.WorktreeRoot != worktree || snapshot.Git.Branch != "linked" {
		t.Fatalf("worktree: %+v", snapshot.Git)
	}
	git(t, root, "checkout", "--detach")
	snapshot = Collect(context.Background(), root, Options{})
	if snapshot.Git == nil || !snapshot.Git.Detached || snapshot.Git.Branch != "" || snapshot.Git.HEAD != head {
		t.Fatalf("detached: %+v", snapshot.Git)
	}
	for _, cwd := range []string{"", t.TempDir(), filepath.Join(root, "missing")} {
		if result := Collect(context.Background(), cwd, Options{}); result.Git != nil || result.Cwd != cwd {
			t.Fatalf("invalid %q: %+v", cwd, result)
		}
	}
}

func TestHerdrGatingAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, env, pane, session, response string
		missing, want                      bool
	}{
		{name: "outside", pane: "p", response: `{}`, want: false},
		{name: "no pane", env: "1", response: `{}`},
		{name: "missing binary", env: "1", pane: "p", missing: true},
		{name: "stripped", env: "1", pane: "p", response: `{"result":{"pane":{"terminal_title":"raw","terminal_title_stripped":"clean"}}}`, want: true},
		{name: "fallback", env: "1", pane: "p", response: `{"result":{"pane":{"terminal_title":"raw"}}}`, want: true},
		{name: "bad JSON", env: "1", pane: "p", response: `oops`},
		{name: "missing identity", env: "1", pane: "p", session: "child", response: `{"result":{"pane":{"terminal_title":"parent"}}}`},
		{name: "wrong identity", env: "1", pane: "p", session: "child", response: `{"result":{"pane":{"terminal_title":"parent","agent_session":{"value":"parent"}}}}`},
		{name: "matching identity", env: "1", pane: "p", session: "child", response: `{"result":{"pane":{"terminal_title":"clean","agent_session":{"value":"child"}}}}`, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			result := Collect(context.Background(), "", Options{
				SessionID: tc.session,
				Getenv: func(key string) string {
					if key == "HERDR_ENV" {
						return tc.env
					}
					return tc.pane
				},
				LookPath: func(string) (string, error) {
					if tc.missing {
						return "", errors.New("missing")
					}
					return "herdr", nil
				},
				Run: func(_ context.Context, _ string, args, env []string) ([]byte, error) {
					calls++
					if strings.Join(args, " ") != "pane current --current" {
						t.Fatalf("args: %v", args)
					}
					return []byte(tc.response), nil
				},
			})
			if (result.Terminal != nil) != tc.want {
				t.Fatalf("terminal: %+v", result.Terminal)
			}
			if (tc.env != "1" || tc.pane == "" || tc.missing) && calls != 0 {
				t.Fatal("unexpected command")
			}
			if tc.name == "stripped" && result.Terminal.Title != "clean" {
				t.Fatal(result.Terminal)
			}
		})
	}
}

func TestFailureAndSharedDeadline(t *testing.T) {
	start := time.Now()
	result := Collect(context.Background(), "/repo", Options{
		Timeout:  20 * time.Millisecond,
		Getenv:   func(string) string { return "1" },
		LookPath: func(s string) (string, error) { return s, nil },
		Run: func(ctx context.Context, binary string, args, env []string) ([]byte, error) {
			if binary == "git" {
				switch args[2] {
				case "rev-parse":
					if len(args) > 3 && args[3] == "--show-toplevel" {
						return []byte("/repo"), nil
					}
					return []byte("abc"), nil
				case "symbolic-ref":
					return nil, errors.New("permission denied")
				}
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if result.Git == nil || result.Git.WorktreeRoot != "/repo" || result.Git.Detached || result.Terminal != nil {
		t.Fatalf("partial: %+v", result)
	}
	if time.Since(start) > time.Second {
		t.Fatal("deadline not honored")
	}
}

func TestRunnerBoundsInheritedOutputPipes(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = run(ctx, shell, []string{"-c", "sleep 1 & wait"}, os.Environ())
	if err == nil {
		t.Fatal("expected timeout")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("descendant output pipe exceeded timeout bound")
	}
}
