package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const failover = "../../testdata/run/failover.md"

func runCLI(t *testing.T, input string, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	var out, errb bytes.Buffer
	code := Run(append([]string{"run"}, args...), strings.NewReader(input), &out, &errb)
	return code, out.String(), errb.String()
}

func TestRunDryRun(t *testing.T) {
	code, out, errb := runCLI(t, "", "--dry-run", "--var", "HOST=db-02", "--from", "2", failover)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb)
	}
	if !strings.Contains(out, "3 step(s) selected of 4") || !strings.Contains(out, "will prompt for REGION") {
		t.Errorf("plan = %s", out)
	}
}

func TestRunWritesTranscript(t *testing.T) {
	tr := filepath.Join(t.TempDir(), "inc.log")
	code, _, errb := runCLI(t, "r\nr\n", "--only", "verify", "--var", "HOST=db-02", "--transcript", tr, failover)
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errb)
	}
	info, err := os.Stat(tr)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("transcript mode = %v, want 0600", perm)
	}
	b, _ := os.ReadFile(tr)
	if !strings.Contains(string(b), "checking db-02") || !strings.Contains(string(b), "only: step=verify") {
		t.Errorf("transcript = %s", b)
	}

	// A resumed run appends to the same transcript.
	if code, _, errb := runCLI(t, "s\n", "--from", "4", "--transcript", tr, failover); code != 0 {
		t.Fatalf("resume exit = %d, stderr=%s", code, errb)
	}
	b, _ = os.ReadFile(tr)
	if strings.Count(string(b), "# runbook-lint run transcript") != 2 {
		t.Errorf("expected two runs in the transcript:\n%s", b)
	}
}

func TestRunAbortExits1(t *testing.T) {
	code, _, errb := runCLI(t, "a\n", "--var", "HOST=x", failover)
	if code != 1 || !strings.Contains(errb, "aborted") {
		t.Fatalf("exit = %d, stderr=%s", code, errb)
	}
}

func TestRunFailureSuggestsResume(t *testing.T) {
	rb := filepath.Join(t.TempDir(), "fail.md")
	writeFile(t, rb, "# x\n\n```sh\nexit 4\n```\n")
	code, _, errb := runCLI(t, "r\n", rb)
	if code != 1 || !strings.Contains(errb, "resume with --from 1") {
		t.Fatalf("exit = %d, stderr=%s", code, errb)
	}
}

func TestRunUsageErrors(t *testing.T) {
	noSteps := filepath.Join(t.TempDir(), "plain.md")
	writeFile(t, noSteps, "# nothing to run\n")
	for name, args := range map[string][]string{
		"no file":            {},
		"no steps":           {noSteps},
		"from out of range":  {"--from", "9", failover},
		"only matches none":  {"--only", "nope", failover},
		"bad var":            {"--var", "HOST", failover},
		"dry run transcript": {"--dry-run", "--transcript", "x.log", failover},
		"missing runbook":    {"does-not-exist.md"},
		"two runbooks":       {failover, failover},
	} {
		t.Run(name, func(t *testing.T) {
			if code, _, _ := runCLI(t, "", args...); code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
		})
	}
}
