package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func allowAll(Action) (bool, string) { return true, "" }
func denyAll(Action) (bool, string)  { return false, "no" }

func scripted(steps ...string) (CompleteFunc, *int) {
	i := 0
	fn := func(_ context.Context, _, _ string) (string, error) {
		if i >= len(steps) {
			return "done.", nil // safety: no more scripted actions
		}
		s := steps[i]
		i++
		return s, nil
	}
	return fn, &i
}

func writeAction(path, content string) string {
	return "ok\n```prismag\n{\"tool\":\"write_file\",\"path\":\"" + path + "\",\"content\":\"" + content + "\"}\n```"
}

func TestParseAction(t *testing.T) {
	a, ok := ParseAction(writeAction("/tmp/x.txt", "hi"))
	if !ok || a.Tool != ToolWriteFile || a.Path != "/tmp/x.txt" || a.Content != "hi" {
		t.Fatalf("parse = %+v ok=%v", a, ok)
	}
	if _, ok := ParseAction("just prose, no block"); ok {
		t.Fatal("should not parse without a block")
	}
	if _, ok := ParseAction("```prismag\nnot json\n```"); ok {
		t.Fatal("malformed json should not parse")
	}
}

func TestRunWritesFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sub", "poem.txt")
	complete, _ := scripted(writeAction(target, "roses are red"), "Done — poem saved.")

	final, recs, err := Run(context.Background(), complete, "", "write a poem to "+target, Policy{Approve: allowAll})
	if err != nil {
		t.Fatal(err)
	}
	got, rerr := os.ReadFile(target)
	if rerr != nil || string(got) != "roses are red" {
		t.Fatalf("file = %q err=%v", got, rerr)
	}
	if len(recs) != 1 || recs[0].Denied {
		t.Fatalf("records = %+v", recs)
	}
	if !strings.Contains(final, "Done") {
		t.Fatalf("final = %q", final)
	}
}

func TestRunDeniedDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "poem.txt")
	complete, _ := scripted(writeAction(target, "x"), "ok, stopping.")

	_, recs, err := Run(context.Background(), complete, "", "write", Policy{Approve: denyAll})
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(target); statErr == nil {
		t.Fatal("file should not exist after denial")
	}
	if len(recs) != 1 || !recs[0].Denied {
		t.Fatalf("expected one denied record, got %+v", recs)
	}
}

func TestRunShellDisabled(t *testing.T) {
	complete, _ := scripted("go\n```prismag\n{\"tool\":\"run_shell\",\"command\":\"echo hi\"}\n```", "done")
	_, recs, err := Run(context.Background(), complete, "", "run", Policy{Approve: allowAll, AllowShell: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || !recs[0].Denied {
		t.Fatalf("shell should be denied when disabled: %+v", recs)
	}
}

func TestIsDestructive(t *testing.T) {
	destructive := []string{
		"rm -rf /",
		"rm -rf ~",
		"rm -rf ~/",
		"rm -rf /*",
		"rm -fr / ",
		"sudo rm -rf /",
		"rm   -r  -f   ~",
		"mkfs.ext4 /dev/sda1",
		"dd if=/dev/zero of=/dev/sda",
		"echo x > /dev/sda",
		"shutdown -h now",
		"reboot",
		":(){ :|:& };:",
		"chmod -R 777 /",
		"mv important /dev/null",
	}
	for _, c := range destructive {
		if !IsDestructive(c) {
			t.Errorf("IsDestructive(%q) = false, want true", c)
		}
	}

	safe := []string{
		"rm file.txt",
		"rm -rf ./build",
		"rm -rf node_modules",
		"rm -rf /home/me/project/dist",
		"ls -la",
		"echo hello",
		"git status",
		"mkdir -p ~/Desktop/poems",
		"cat README.md",
	}
	for _, c := range safe {
		if IsDestructive(c) {
			t.Errorf("IsDestructive(%q) = true, want false", c)
		}
	}
}

func shellAction(command string) string {
	return "go\n```prismag\n{\"tool\":\"run_shell\",\"command\":\"" + command + "\"}\n```"
}

func TestRunRefusesDestructiveEvenWhenApproved(t *testing.T) {
	complete, _ := scripted(shellAction("rm -rf /"), "stopped")
	_, recs, err := Run(context.Background(), complete, "", "wipe", Policy{
		Approve: allowAll, AllowShell: true, // approve everything, shell on
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || !recs[0].Denied {
		t.Fatalf("destructive command must be denied, got %+v", recs)
	}
	if !strings.Contains(recs[0].Result, "destructive") {
		t.Errorf("denial reason = %q", recs[0].Result)
	}
}

func TestRunAllowsDestructiveWhenOptedIn(t *testing.T) {
	// "echo reboot" matches the denylist but is harmless to actually run, so it
	// safely exercises the opt-in bypass.
	complete, _ := scripted(shellAction("echo reboot"), "done")
	_, recs, err := Run(context.Background(), complete, "", "run", Policy{
		Approve: allowAll, AllowShell: true, AllowDestructive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Denied {
		t.Fatalf("opted-in command should run, got %+v", recs)
	}
	if !strings.Contains(recs[0].Result, "reboot") {
		t.Errorf("expected command output, got %q", recs[0].Result)
	}
}

func TestResolvePathConfinement(t *testing.T) {
	root := t.TempDir()
	if _, err := resolvePath(filepath.Join(root, "ok.txt"), root); err != nil {
		t.Fatalf("in-root path should resolve: %v", err)
	}
	if _, err := resolvePath("/etc/passwd", root); err == nil {
		t.Fatal("out-of-root path should be rejected")
	}
}

func TestRunConfinementBlocksEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escape.txt")
	complete, _ := scripted(writeAction(outside, "x"), "stopped")
	_, recs, err := Run(context.Background(), complete, "", "write outside", Policy{Approve: allowAll, Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(outside); statErr == nil {
		t.Fatal("write outside root must fail")
	}
	if len(recs) != 1 || !strings.Contains(recs[0].Result, "outside the allowed root") {
		t.Fatalf("expected confinement error, got %+v", recs)
	}
}

func TestRunStepLimit(t *testing.T) {
	dir := t.TempDir()
	// Always returns an action → never finishes.
	complete := func(_ context.Context, _, _ string) (string, error) {
		return writeAction(filepath.Join(dir, "loop.txt"), "x"), nil
	}
	_, _, err := Run(context.Background(), complete, "", "loop", Policy{Approve: allowAll, MaxSteps: 3})
	if err == nil || !strings.Contains(err.Error(), "step limit") {
		t.Fatalf("expected step-limit error, got %v", err)
	}
}
