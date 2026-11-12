// Package config loads .runbook-lint.yaml files and resolves the linter rules
// that apply to a given runbook.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/moveeeax/runbook-lint/internal/linter"
	"go.yaml.in/yaml/v3"
)

// FileName is the config file looked up next to and above each runbook.
const FileName = ".runbook-lint.yaml"

// File is a parsed config file. Top-level rules replace the built-in
// defaults, matching overrides are applied on top in file order, and
// severity rules are applied last, per runbook, by the linter.
type File struct {
	linter.Rules  `yaml:",inline"`
	Overrides     []Override              `yaml:"overrides"`
	SeverityRules map[string]linter.Rules `yaml:"severity_rules"`

	// Path is where the file was loaded from; globs are relative to its directory.
	Path string `yaml:"-"`
}

// Override changes the rules for runbooks whose path, relative to the config
// file's directory, matches Glob. `**` matches any number of directories.
type Override struct {
	Glob         string `yaml:"glob"`
	linter.Rules `yaml:",inline"`
}

// Load reads and validates a config file. Unknown keys are an error so a typo
// does not silently fall back to the defaults.
func Load(p string) (*File, error) {
	src, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	if err := f.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil, err
	}
	f.Path = abs
	return &f, nil
}

func (f *File) validate() error {
	if err := validateRules("max_age_days", f.Rules); err != nil {
		return err
	}
	for i, o := range f.Overrides {
		if o.Glob == "" {
			return fmt.Errorf("overrides[%d]: glob is required", i)
		}
		if err := validGlob(o.Glob); err != nil {
			return fmt.Errorf("overrides[%d]: invalid glob %q: %w", i, o.Glob, err)
		}
		if err := validateRules(fmt.Sprintf("overrides[%d].max_age_days", i), o.Rules); err != nil {
			return err
		}
	}
	seen := map[string]string{}
	for key, r := range f.SeverityRules {
		lower := strings.ToLower(key)
		if prev, dup := seen[lower]; dup {
			return fmt.Errorf("severity_rules: %q and %q differ only in case", prev, key)
		}
		seen[lower] = key
		if err := validateRules(fmt.Sprintf("severity_rules.%s.max_age_days", key), r); err != nil {
			return err
		}
	}
	return nil
}

func validateRules(field string, r linter.Rules) error {
	if r.MaxAgeDays != nil && *r.MaxAgeDays < 0 {
		return fmt.Errorf("%s must be >= 0, got %d", field, *r.MaxAgeDays)
	}
	return nil
}

// Find walks up from dir and returns the path of the first config file found,
// or "" when there is none up to the filesystem root.
func Find(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(dir, FileName)
		info, err := os.Stat(p)
		switch {
		case err == nil && !info.IsDir():
			return p, nil
		case err != nil && !errors.Is(err, os.ErrNotExist):
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// Resolve returns the rules for runbook file: base with the file's top-level
// rules, then every matching override, applied in order. Severity rules are
// carried on the returned config for the linter to apply per runbook.
func (f *File) Resolve(base linter.Config, file string) (linter.Config, error) {
	cfg := base.With(f.Rules)
	if len(f.Overrides) > 0 {
		rel, inside, err := f.rel(file)
		if err != nil {
			return cfg, err
		}
		for _, o := range f.Overrides {
			if inside && matchGlob(o.Glob, rel) {
				cfg = cfg.With(o.Rules)
			}
		}
	}
	if f.SeverityRules != nil {
		cfg.SeverityRules = f.SeverityRules
	}
	return cfg, nil
}

// rel returns file relative to the config file's directory, slash-separated.
// inside is false for files outside that directory: overrides never match them.
func (f *File) rel(file string) (rel string, inside bool, err error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return "", false, err
	}
	rel, err = filepath.Rel(filepath.Dir(f.Path), abs)
	if err != nil {
		return "", false, nil
	}
	rel = filepath.ToSlash(rel)
	return rel, rel != ".." && !strings.HasPrefix(rel, "../"), nil
}

func validGlob(glob string) error {
	for _, seg := range strings.Split(glob, "/") {
		if _, err := path.Match(seg, ""); err != nil {
			return err
		}
	}
	return nil
}

// matchGlob matches a slash-separated path against a glob where `**` as a
// whole segment matches zero or more segments and other segments use
// path.Match syntax.
func matchGlob(glob, name string) bool {
	return matchSegments(strings.Split(glob, "/"), strings.Split(name, "/"))
}

func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(name); i++ {
				if matchSegments(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], name[0]); !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}
