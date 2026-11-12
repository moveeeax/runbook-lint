// Package cli wires the runbook linter to a command line: it collects markdown
// files, runs the linter, and renders results as text or JSON.
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/moveeeax/runbook-lint/internal/config"
	"github.com/moveeeax/runbook-lint/internal/linter"
)

// Version is the released binary version.
const Version = "0.3.0"

// Run executes the CLI and returns a process exit code:
//
//	0 = clean, 1 = at least one error-level finding, 2 = usage/IO error.
//
// `runbook-lint run ...` executes a runbook instead; see runRunbook.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "run" {
		return runRunbook(args[1:], stdin, stdout, stderr)
	}

	fs := flag.NewFlagSet("runbook-lint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "emit findings as JSON")
	maxAge := fs.Int("max-age-days", 180, "warn on runbooks not reviewed within N days (0 disables)")
	configPath := fs.String("config", "", "config file to use for every runbook (default: nearest "+config.FileName+" above each file)")
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: runbook-lint [flags] <path>...\n")
		fmt.Fprintf(stderr, "       runbook-lint run [flags] <runbook.md>\n\n")
		fmt.Fprintf(stderr, "Lint operational runbooks for required structure, frontmatter,\n")
		fmt.Fprintf(stderr, "staleness and broken internal links. Paths may be files or directories.\n")
		fmt.Fprintf(stderr, "See 'runbook-lint run -h' for executing a runbook step by step.\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "runbook-lint "+Version)
		return 0
	}
	paths := fs.Args()
	if len(paths) == 0 {
		fs.Usage()
		return 2
	}

	maxAgeSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "max-age-days" {
			maxAgeSet = true
		}
	})

	base := linter.DefaultConfig()
	base.Now = time.Now()
	rules, err := newResolver(*configPath, base)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}

	files, err := collect(paths)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if len(files) == 0 {
		fmt.Fprintln(stderr, "no markdown files found in", strings.Join(paths, ", "))
		return 2
	}

	var all []linter.Finding
	for _, f := range files {
		cfg, err := rules.forFile(f)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
		// Flags win over the config file, including its severity rules.
		if maxAgeSet {
			cfg.MaxAgeDays = *maxAge
			cfg.SeverityRules = withoutMaxAge(cfg.SeverityRules)
		}
		found, err := linter.LintFile(f, cfg)
		if err != nil {
			fmt.Fprintf(stderr, "error reading %s: %v\n", f, err)
			return 2
		}
		all = append(all, found...)
	}

	if *jsonOut {
		return renderJSON(stdout, all)
	}
	return renderText(stdout, len(files), all)
}

// resolver picks the config file for each runbook: the one given with
// --config, or the nearest .runbook-lint.yaml walking up from the file.
type resolver struct {
	base   linter.Config
	fixed  *config.File
	byDir  map[string]*config.File // nil value: no config file above this dir
	loaded map[string]*config.File
}

func newResolver(path string, base linter.Config) (*resolver, error) {
	r := &resolver{base: base, byDir: map[string]*config.File{}, loaded: map[string]*config.File{}}
	if path != "" {
		f, err := config.Load(path)
		if err != nil {
			return nil, err
		}
		r.fixed = f
	}
	return r, nil
}

func (r *resolver) forFile(file string) (linter.Config, error) {
	f := r.fixed
	if f == nil {
		dir := filepath.Dir(file)
		cached, ok := r.byDir[dir]
		if !ok {
			p, err := config.Find(dir)
			if err != nil {
				return r.base, err
			}
			if p != "" {
				if cached = r.loaded[p]; cached == nil {
					if cached, err = config.Load(p); err != nil {
						return r.base, err
					}
					r.loaded[p] = cached
				}
			}
			r.byDir[dir] = cached
		}
		f = cached
	}
	if f == nil {
		return r.base, nil
	}
	return f.Resolve(r.base, file)
}

func withoutMaxAge(rules map[string]linter.Rules) map[string]linter.Rules {
	if rules == nil {
		return nil
	}
	out := make(map[string]linter.Rules, len(rules))
	for k, v := range rules {
		v.MaxAgeDays = nil
		out[k] = v
	}
	return out
}

func renderJSON(w io.Writer, findings []linter.Finding) int {
	if findings == nil {
		findings = []linter.Finding{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(findings)
	return exitCode(findings)
}

func renderText(w io.Writer, nFiles int, findings []linter.Finding) int {
	if len(findings) == 0 {
		fmt.Fprintf(w, "✓ %d runbook(s) OK\n", nFiles)
		return 0
	}
	byFile := map[string][]linter.Finding{}
	var order []string
	for _, f := range findings {
		if _, seen := byFile[f.File]; !seen {
			order = append(order, f.File)
		}
		byFile[f.File] = append(byFile[f.File], f)
	}
	sort.Strings(order)

	var errs, warns int
	for _, file := range order {
		fmt.Fprintf(w, "%s\n", file)
		for _, f := range byFile[file] {
			mark := "warning"
			if f.Level == linter.Error {
				mark = "error"
				errs++
			} else {
				warns++
			}
			fmt.Fprintf(w, "  %-7s [%s] %s\n", mark, f.Rule, f.Message)
		}
	}
	fmt.Fprintf(w, "\n%d error(s), %d warning(s) across %d file(s)\n", errs, warns, nFiles)
	return exitCode(findings)
}

func exitCode(findings []linter.Finding) int {
	for _, f := range findings {
		if f.Level == linter.Error {
			return 1
		}
	}
	return 0
}

// collect expands the given paths into a sorted, de-duplicated list of markdown
// files. Directories are walked recursively.
func collect(paths []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	addFile := func(p string) {
		if isMarkdown(p) && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			addFile(p)
			continue
		}
		err = filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				addFile(path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

func isMarkdown(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	return ext == ".md" || ext == ".markdown"
}
