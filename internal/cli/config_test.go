package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/moveeeax/runbook-lint/internal/linter"
)

const customRunbook = `---
owner: payments
severity: sev1
service: ledger
last-reviewed: 2000-01-01
---

# Ledger writes failing

## Symptoms

## Impact

## Rollback
`

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lintJSON(t *testing.T, args ...string) (int, []linter.Finding) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(append([]string{"--json"}, args...), nil, &out, &errb)
	var findings []linter.Finding
	if code != 2 {
		if err := json.Unmarshal(out.Bytes(), &findings); err != nil {
			t.Fatalf("json: %v\n%s\nstderr=%s", err, out.String(), errb.String())
		}
	}
	return code, findings
}

func TestConfigFileFoundAboveRunbook(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".runbook-lint.yaml"), "required_sections: [Symptoms, Impact, Rollback]\nmax_age_days: 0\n")
	rb := filepath.Join(root, "team", "ledger.md")
	writeFile(t, rb, customRunbook)

	if code, findings := lintJSON(t, rb); code != 0 || len(findings) != 0 {
		t.Fatalf("exit = %d, findings = %+v; want clean with the custom sections", code, findings)
	}

	// Without the config the default sections apply.
	other := filepath.Join(t.TempDir(), "ledger.md")
	writeFile(t, other, customRunbook)
	if code, _ := lintJSON(t, "--max-age-days", "0", other); code != 1 {
		t.Fatalf("exit = %d, want 1 without config", code)
	}
}

func TestExplicitConfigAndFlagPrecedence(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "rules.yaml")
	writeFile(t, cfgPath, "required_sections: [Symptoms]\nseverity_rules:\n  sev1:\n    max_age_days: 90\n")
	rb := filepath.Join(t.TempDir(), "ledger.md")
	writeFile(t, rb, customRunbook)

	_, findings := lintJSON(t, "--config", cfgPath, rb)
	if n := countLevel(findings, "stale"); n != 1 {
		t.Fatalf("stale warnings = %d, want 1 from the sev1 rule: %+v", n, findings)
	}
	// The flag wins over the file, severity rules included.
	if _, findings := lintJSON(t, "--config", cfgPath, "--max-age-days", "0", rb); len(findings) != 0 {
		t.Fatalf("--max-age-days 0 should disable staleness, got %+v", findings)
	}
}

func TestBadConfigIsUsageError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".runbook-lint.yaml"), "max_age_dayz: 3\n")
	writeFile(t, filepath.Join(dir, "x.md"), customRunbook)
	if code, _ := lintJSON(t, dir); code != 2 {
		t.Fatalf("exit = %d, want 2 for an unknown config key", code)
	}
	if code, _ := lintJSON(t, "--config", filepath.Join(dir, "missing.yaml"), dir); code != 2 {
		t.Fatalf("exit = %d, want 2 for a missing --config file", code)
	}
}

func countLevel(findings []linter.Finding, rule string) int {
	n := 0
	for _, f := range findings {
		if f.Rule == rule {
			n++
		}
	}
	return n
}
