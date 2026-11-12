// Package runner executes the fenced shell blocks of a runbook one step at a
// time, with confirmation and a transcript, so the documented procedure and
// what was actually run cannot diverge.
package runner

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// Step is one runnable fenced code block.
type Step struct {
	// Index is the 1-based position among the runnable blocks, as used by --from.
	Index int
	// Line is the 1-based line of the block's opening fence in the runbook.
	Line int
	// Lang is the interpreter: "bash" or "sh".
	Lang string
	// Tags are key=value attributes from the fence info string, e.g. step=verify.
	Tags map[string]string
	// Script is the block's content.
	Script string
}

// runnable maps fence languages to the interpreter that runs them. Other
// languages (console, shell sessions with prompts and output, yaml...) are
// documentation, not steps.
var runnable = map[string]string{"bash": "bash", "sh": "sh"}

// ParseSteps returns the runnable fenced blocks of a Markdown runbook in
// document order.
func ParseSteps(src []byte) []Step {
	doc := goldmark.New().Parser().Parse(text.NewReader(src))
	var steps []Step
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		block, ok := n.(*ast.FencedCodeBlock)
		if !entering || !ok || block.Info == nil {
			return ast.WalkContinue, nil
		}
		fields := strings.Fields(string(block.Info.Segment.Value(src)))
		if len(fields) == 0 {
			return ast.WalkContinue, nil
		}
		lang, ok := runnable[strings.ToLower(fields[0])]
		if !ok {
			return ast.WalkContinue, nil
		}
		tags := map[string]string{}
		for _, f := range fields[1:] {
			if k, v, ok := strings.Cut(f, "="); ok && k != "" {
				tags[k] = strings.Trim(v, `"'`)
			}
		}
		var script bytes.Buffer
		lines := block.Lines()
		for i := 0; i < lines.Len(); i++ {
			seg := lines.At(i)
			script.Write(seg.Value(src))
		}
		steps = append(steps, Step{
			Index:  len(steps) + 1,
			Line:   bytes.Count(src[:block.Info.Segment.Start], []byte("\n")) + 1,
			Lang:   lang,
			Tags:   tags,
			Script: script.String(),
		})
		return ast.WalkSkipChildren, nil
	})
	return steps
}

// varRef matches ${NAME} placeholders. Only upper-case names are runbook
// variables; ${lower} and $NAME are left to the shell.
var varRef = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

// Placeholders returns the variable names a script needs, in order of first
// use. Names the script assigns itself (NAME=..., read NAME, for NAME in) are
// shell variables, not placeholders.
func Placeholders(script string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range varRef.FindAllStringSubmatch(script, -1) {
		name := m[1]
		if seen[name] || assignsItself(script, name) {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func assignsItself(script, name string) bool {
	q := regexp.QuoteMeta(name)
	re := regexp.MustCompile(`(^|[\s;&|(])` + q + `=|\b(read|for)\s+(-\w+\s+)*` + q + `\b`)
	return re.MatchString(script)
}

// Substitute replaces ${NAME} placeholders that have a value in vars and
// leaves the rest untouched.
func Substitute(script string, vars map[string]string) string {
	return varRef.ReplaceAllStringFunc(script, func(ref string) string {
		name := ref[2 : len(ref)-1]
		if v, ok := vars[name]; ok && !assignsItself(script, name) {
			return v
		}
		return ref
	})
}
