package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/moveeeax/runbook-lint/internal/linter"
)

func TestRunCleanDir(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"--max-age-days", "0", "../../testdata/runbooks/good.md"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s out=%s", code, errb.String(), out.String())
	}
	if !strings.Contains(out.String(), "OK") {
		t.Errorf("expected OK output, got %q", out.String())
	}
}

func TestRunReportsErrorsWithExit1(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"../../testdata/runbooks/bad.md"}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; out=%s", code, out.String())
	}
}

func TestRunJSONShape(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"--json", "../../testdata/runbooks/bad.md"}, &out, &errb)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	var findings []linter.Finding
	if err := json.Unmarshal(out.Bytes(), &findings); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if len(findings) == 0 {
		t.Fatal("expected findings in JSON output")
	}
}

func TestRunNoArgsIsUsageError(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run(nil, &out, &errb); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"--version"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out.String(), Version) {
		t.Errorf("version output = %q", out.String())
	}
}
