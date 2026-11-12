package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moveeeax/runbook-lint/internal/linter"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadAndResolve(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, FileName)
	write(t, cfgPath, `
required_sections: [Symptoms, Impact, Mitigation, Rollback]
required_frontmatter: [owner, service]
max_age_days: 365
overrides:
  - glob: "sev1/**"
    required_sections: [Symptoms, Impact, Mitigation, Rollback, Escalation]
  - glob: "**/legacy-*.md"
    max_age_days: 0
severity_rules:
  sev1:
    max_age_days: 90
`)
	f, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	plain, err := f.Resolve(linter.DefaultConfig(), filepath.Join(dir, "db.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plain.RequiredSections, ",") != "Symptoms,Impact,Mitigation,Rollback" {
		t.Errorf("sections = %v", plain.RequiredSections)
	}
	if strings.Join(plain.RequiredFrontmatter, ",") != "owner,service" {
		t.Errorf("frontmatter = %v", plain.RequiredFrontmatter)
	}
	if plain.MaxAgeDays != 365 {
		t.Errorf("max age = %d, want 365", plain.MaxAgeDays)
	}
	if r, ok := plain.SeverityRules["sev1"]; !ok || r.MaxAgeDays == nil || *r.MaxAgeDays != 90 {
		t.Errorf("severity rules not carried: %+v", plain.SeverityRules)
	}

	nested, err := f.Resolve(linter.DefaultConfig(), filepath.Join(dir, "sev1", "payments", "legacy-db.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(nested.RequiredSections) != 5 {
		t.Errorf("sev1/** override not applied: %v", nested.RequiredSections)
	}
	if nested.MaxAgeDays != 0 {
		t.Errorf("legacy override not applied: max age = %d", nested.MaxAgeDays)
	}

	outside, err := f.Resolve(linter.DefaultConfig(), filepath.Join(filepath.Dir(dir), "legacy-x.md"))
	if err != nil {
		t.Fatal(err)
	}
	if outside.MaxAgeDays != 365 {
		t.Errorf("overrides must not match files outside the config dir: max age = %d", outside.MaxAgeDays)
	}
}

func TestLoadRejectsUnknownKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), FileName)
	write(t, p, "required_section: [Impact]\n")
	if _, err := Load(p); err == nil {
		t.Fatal("expected an error for a misspelled key")
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	for name, content := range map[string]string{
		"negative age":   "max_age_days: -1\n",
		"missing glob":   "overrides:\n  - max_age_days: 10\n",
		"bad glob":       "overrides:\n  - glob: \"[\"\n",
		"case duplicate": "severity_rules:\n  sev1: {max_age_days: 1}\n  SEV1: {max_age_days: 2}\n",
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), FileName)
			write(t, p, content)
			if _, err := Load(p); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}

func TestLoadEmptyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), FileName)
	write(t, p, "")
	f, err := Load(p)
	if err != nil {
		t.Fatalf("empty config should load: %v", err)
	}
	cfg, err := f.Resolve(linter.DefaultConfig(), filepath.Join(filepath.Dir(p), "x.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.RequiredSections) != 4 || cfg.MaxAgeDays != 180 {
		t.Errorf("empty config should keep defaults, got %+v", cfg)
	}
}

func TestFindWalksUp(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, FileName), "max_age_days: 30\n")
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Find(deep)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(root, FileName))
	if g, _ := filepath.EvalSymlinks(got); g != want {
		t.Errorf("Find = %q, want %q", got, want)
	}

	// A closer file wins.
	write(t, filepath.Join(root, "a", FileName), "max_age_days: 10\n")
	got, _ = Find(deep)
	if filepath.Base(filepath.Dir(got)) != "a" {
		t.Errorf("Find = %q, want the file in a/", got)
	}
}

func TestMatchGlob(t *testing.T) {
	cases := []struct {
		glob, name string
		want       bool
	}{
		{"sev1/**", "sev1/db.md", true},
		{"sev1/**", "sev1/a/b/db.md", true},
		{"sev1/**", "sev2/db.md", false},
		{"**/*.md", "db.md", true},
		{"**/*.md", "a/b/db.md", true},
		{"*.md", "a/db.md", false},
		{"a/**/z.md", "a/z.md", true},
		{"a/**/z.md", "a/b/c/z.md", true},
	}
	for _, c := range cases {
		if got := matchGlob(c.glob, c.name); got != c.want {
			t.Errorf("matchGlob(%q, %q) = %v, want %v", c.glob, c.name, got, c.want)
		}
	}
}
