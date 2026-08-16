// Package linter checks operational runbooks for the things you always wish
// you had checked before an incident: required sections, frontmatter metadata,
// staleness, and broken internal links.
package linter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"
)

// Level is the severity of a finding. Errors fail the run; warnings do not.
type Level string

const (
	Error   Level = "error"
	Warning Level = "warning"
)

// Finding is a single problem detected in a runbook.
type Finding struct {
	File    string `json:"file"`
	Level   Level  `json:"level"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// Config controls which rules run and how strict they are.
type Config struct {
	// RequiredSections are heading titles every runbook must contain (case-insensitive).
	RequiredSections []string
	// RequiredFrontmatter are YAML keys every runbook must set to a non-empty value.
	RequiredFrontmatter []string
	// MaxAgeDays flags a runbook whose last-reviewed date is older than this. 0 disables the check.
	MaxAgeDays int
	// Now is the reference time for staleness. Injectable so tests are deterministic.
	Now time.Time
}

// DefaultConfig is the opinionated baseline: the four sections every on-call
// engineer wants at 3am, plus ownership metadata.
func DefaultConfig() Config {
	return Config{
		RequiredSections:    []string{"Symptoms", "Diagnosis", "Mitigation", "Escalation"},
		RequiredFrontmatter: []string{"owner", "severity", "service"},
		MaxAgeDays:          180,
		Now:                 time.Now(),
	}
}

// LintFile reads path and lints its contents.
func LintFile(path string, cfg Config) ([]Finding, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LintSource(path, src, cfg), nil
}

// LintSource lints an in-memory runbook. path is used for relative link
// resolution and in finding messages.
func LintSource(path string, src []byte, cfg Config) []Finding {
	var findings []Finding
	add := func(level Level, rule, msg string) {
		findings = append(findings, Finding{File: path, Level: level, Rule: rule, Message: msg})
	}

	fm, body, hasFM, fmErr := splitFrontmatter(src)
	switch {
	case !hasFM:
		add(Error, "frontmatter", "missing YAML frontmatter block")
	case fmErr != nil:
		add(Error, "frontmatter", fmt.Sprintf("invalid YAML frontmatter: %v", fmErr))
	default:
		for _, key := range cfg.RequiredFrontmatter {
			if v, ok := fm[key]; !ok || isEmpty(v) {
				add(Error, "frontmatter", fmt.Sprintf("missing required frontmatter field %q", key))
			}
		}
		if cfg.MaxAgeDays > 0 {
			checkStale(fm, cfg, add)
		}
	}

	headings, links := parseMarkdown(body)
	present := map[string]bool{}
	for _, h := range headings {
		present[strings.ToLower(strings.TrimSpace(h))] = true
	}
	for _, sec := range cfg.RequiredSections {
		if !present[strings.ToLower(sec)] {
			add(Error, "section", fmt.Sprintf("missing required section %q", sec))
		}
	}

	dir := filepath.Dir(path)
	for _, l := range links {
		if !isLocalLink(l) {
			continue
		}
		target := l
		if i := strings.IndexByte(target, '#'); i >= 0 {
			target = target[:i]
		}
		if target == "" { // pure in-page anchor
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, target)); err != nil {
			add(Error, "link", fmt.Sprintf("broken internal link %q", l))
		}
	}

	return findings
}

func checkStale(fm map[string]any, cfg Config, add func(Level, string, string)) {
	lr, ok := fm["last-reviewed"]
	if !ok {
		add(Warning, "stale", "missing last-reviewed date")
		return
	}
	d, ok := parseDate(lr)
	if !ok {
		add(Warning, "stale", "last-reviewed is not a valid date (want YYYY-MM-DD)")
		return
	}
	age := cfg.Now.Sub(d)
	maxAge := time.Duration(cfg.MaxAgeDays) * 24 * time.Hour
	if age > maxAge {
		add(Warning, "stale", fmt.Sprintf("last reviewed %s (%d days ago, max %d)",
			d.Format("2006-01-02"), int(age.Hours()/24), cfg.MaxAgeDays))
	}
}

// splitFrontmatter separates a leading `---`-fenced YAML block from the body.
// It returns hasFM=false when no block is present, and a non-nil error when the
// block is present but not valid YAML.
func splitFrontmatter(src []byte) (fm map[string]any, body []byte, hasFM bool, err error) {
	s := string(src)
	if !strings.HasPrefix(s, "---") {
		return nil, src, false, nil
	}
	lines := strings.Split(s, "\n")
	if strings.TrimSpace(lines[0]) != "---" {
		return nil, src, false, nil
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return nil, src, false, nil
	}
	fmText := strings.Join(lines[1:end], "\n")
	rest := []byte(strings.Join(lines[end+1:], "\n"))
	var m map[string]any
	if e := yaml.Unmarshal([]byte(fmText), &m); e != nil {
		return nil, rest, true, e
	}
	return m, rest, true, nil
}

func parseMarkdown(body []byte) (headings, links []string) {
	doc := goldmark.New().Parser().Parse(text.NewReader(body))
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Heading:
			headings = append(headings, string(node.Text(body)))
		case *ast.Link:
			links = append(links, string(node.Destination))
		}
		return ast.WalkContinue, nil
	})
	return headings, links
}

func isLocalLink(l string) bool {
	if l == "" || strings.HasPrefix(l, "#") {
		return false
	}
	lower := strings.ToLower(l)
	for _, p := range []string{"http://", "https://", "mailto:", "tel:", "ftp://", "//"} {
		if strings.HasPrefix(lower, p) {
			return false
		}
	}
	return true
}

func parseDate(v any) (time.Time, bool) {
	switch x := v.(type) {
	case time.Time:
		return x, true
	case string:
		for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02T15:04:05"} {
			if d, err := time.Parse(layout, x); err == nil {
				return d, true
			}
		}
	}
	return time.Time{}, false
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) == ""
	}
	return false
}
