package linter

import (
	"path/filepath"
	"testing"
	"time"
)

// fixedNow keeps staleness deterministic: good fixtures use 2099, bad uses 2020.
func testConfig() Config {
	c := DefaultConfig()
	c.Now = time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
	return c
}

func countRule(findings []Finding, rule string, level Level) int {
	n := 0
	for _, f := range findings {
		if f.Rule == rule && f.Level == level {
			n++
		}
	}
	return n
}

func TestGoodRunbookIsClean(t *testing.T) {
	findings, err := LintFile(filepath.Join("..", "..", "testdata", "runbooks", "good.md"), testConfig())
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected clean runbook, got %d findings: %+v", len(findings), findings)
	}
}

func TestBadRunbookFlagsEverything(t *testing.T) {
	findings, err := LintFile(filepath.Join("..", "..", "testdata", "runbooks", "bad.md"), testConfig())
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	// missing severity + service
	if got := countRule(findings, "frontmatter", Error); got != 2 {
		t.Errorf("frontmatter errors = %d, want 2: %+v", got, findings)
	}
	// missing Mitigation
	if got := countRule(findings, "section", Error); got != 1 {
		t.Errorf("section errors = %d, want 1: %+v", got, findings)
	}
	// does-not-exist.md
	if got := countRule(findings, "link", Error); got != 1 {
		t.Errorf("link errors = %d, want 1: %+v", got, findings)
	}
	// last-reviewed 2020 is well past 180 days from 2026
	if got := countRule(findings, "stale", Warning); got != 1 {
		t.Errorf("stale warnings = %d, want 1: %+v", got, findings)
	}
}

func TestMissingFrontmatterBlock(t *testing.T) {
	src := []byte("# Title\n\n## Symptoms\n\n## Diagnosis\n\n## Mitigation\n\n## Escalation\n")
	findings := LintSource("nofm.md", src, testConfig())
	if countRule(findings, "frontmatter", Error) != 1 {
		t.Fatalf("expected one frontmatter error, got %+v", findings)
	}
}

func TestInvalidFrontmatterYAML(t *testing.T) {
	src := []byte("---\nowner: [unterminated\n---\n\n# T\n")
	findings := LintSource("badyaml.md", src, testConfig())
	found := false
	for _, f := range findings {
		if f.Rule == "frontmatter" && f.Level == Error {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an invalid-frontmatter error, got %+v", findings)
	}
}

func TestStaleDisabledWhenMaxAgeZero(t *testing.T) {
	cfg := testConfig()
	cfg.MaxAgeDays = 0
	findings, err := LintFile(filepath.Join("..", "..", "testdata", "runbooks", "bad.md"), cfg)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	if countRule(findings, "stale", Warning) != 0 {
		t.Fatalf("staleness should be disabled, got %+v", findings)
	}
}

func TestExternalLinksIgnored(t *testing.T) {
	src := []byte("---\nowner: a\nseverity: sev1\nservice: s\nlast-reviewed: 2099-01-01\n---\n\n" +
		"# T\n\n## Symptoms\n[docs](https://example.com/x) and [anchor](#section)\n\n## Diagnosis\n\n## Mitigation\n\n## Escalation\n")
	findings := LintSource("ext.md", src, testConfig())
	if countRule(findings, "link", Error) != 0 {
		t.Fatalf("external and anchor links must be ignored, got %+v", findings)
	}
}

func TestSplitFrontmatter(t *testing.T) {
	fm, body, has, err := splitFrontmatter([]byte("---\nkey: val\n---\nbody line\n"))
	if err != nil || !has {
		t.Fatalf("has=%v err=%v", has, err)
	}
	if fm["key"] != "val" {
		t.Errorf("key = %v", fm["key"])
	}
	if want := "body line\n"; string(body) != want {
		t.Errorf("body = %q want %q", string(body), want)
	}
}

func TestConfigWithLeavesNilFieldsAlone(t *testing.T) {
	ninety := 90
	got := testConfig().With(Rules{RequiredSections: []string{"Impact"}, MaxAgeDays: &ninety})
	if len(got.RequiredSections) != 1 || got.RequiredSections[0] != "Impact" {
		t.Errorf("sections = %v", got.RequiredSections)
	}
	if got.MaxAgeDays != 90 {
		t.Errorf("max age = %d, want 90", got.MaxAgeDays)
	}
	if len(got.RequiredFrontmatter) != 3 {
		t.Errorf("frontmatter should be inherited, got %v", got.RequiredFrontmatter)
	}
	cleared := testConfig().With(Rules{RequiredFrontmatter: []string{}})
	if len(cleared.RequiredFrontmatter) != 0 {
		t.Errorf("empty list should clear frontmatter, got %v", cleared.RequiredFrontmatter)
	}
}

func TestSeverityRulesApplyToMatchingRunbook(t *testing.T) {
	thirty := 30
	cfg := testConfig()
	cfg.SeverityRules = map[string]Rules{
		"sev1": {MaxAgeDays: &thirty, RequiredSections: []string{"Symptoms", "Rollback"}},
	}
	doc := func(sev string) []byte {
		return []byte("---\nowner: a\nseverity: " + sev + "\nservice: s\nlast-reviewed: 2026-07-01\n---\n\n" +
			"# T\n\n## Symptoms\n\n## Diagnosis\n\n## Mitigation\n\n## Escalation\n")
	}

	sev1 := LintSource("sev1.md", doc("SEV1"), cfg)
	if got := countRule(sev1, "stale", Warning); got != 1 {
		t.Errorf("sev1: stale warnings = %d, want 1 (46 days > 30): %+v", got, sev1)
	}
	if got := countRule(sev1, "section", Error); got != 1 {
		t.Errorf("sev1: section errors = %d, want 1 (Rollback): %+v", got, sev1)
	}

	sev2 := LintSource("sev2.md", doc("sev2"), cfg)
	if len(sev2) != 0 {
		t.Errorf("sev2 should use the base rules and be clean, got %+v", sev2)
	}
}
