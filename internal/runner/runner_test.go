package runner

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) []Step {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "testdata", "run", "failover.md"))
	if err != nil {
		t.Fatal(err)
	}
	return ParseSteps(src)
}

func fixedClock() func() time.Time {
	t0 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	return func() time.Time { return t0 }
}

func TestParseSteps(t *testing.T) {
	steps := fixture(t)
	if len(steps) != 4 {
		t.Fatalf("steps = %d, want 4 (console block is not a step): %+v", len(steps), steps)
	}
	first := steps[0]
	if first.Index != 1 || first.Lang != "bash" || first.Tags["step"] != "verify" {
		t.Errorf("first step = %+v", first)
	}
	if first.Line != 16 {
		t.Errorf("first step line = %d, want 16", first.Line)
	}
	if first.Script != "echo \"checking ${HOST}\"\n" {
		t.Errorf("first script = %q", first.Script)
	}
	if steps[2].Lang != "sh" {
		t.Errorf("third step lang = %q, want sh", steps[2].Lang)
	}
}

func TestPlaceholders(t *testing.T) {
	got := Placeholders("echo ${HOST} ${REGION} ${HOST} ${lower} $PLAIN\nNS=x; echo ${NS}\nread -r ANSWER; echo ${ANSWER}")
	if strings.Join(got, ",") != "HOST,REGION" {
		t.Errorf("placeholders = %v", got)
	}
	if s := Substitute("ssh ${HOST} && NS=1 && echo ${NS}", map[string]string{"HOST": "db-02", "NS": "no"}); s != "ssh db-02 && NS=1 && echo ${NS}" {
		t.Errorf("substitute = %q", s)
	}
}

func TestSelect(t *testing.T) {
	steps := fixture(t)
	if got := Select(steps, 3, nil); len(got) != 2 || got[0].Index != 3 {
		t.Errorf("from 3 = %+v", got)
	}
	got := Select(steps, 1, []string{"verify"})
	if len(got) != 2 || got[0].Index != 1 || got[1].Index != 3 {
		t.Errorf("only verify = %+v", got)
	}
}

func TestRunConfirmsSubstitutesAndRecords(t *testing.T) {
	steps := fixture(t)
	// step 1: run (prompts for HOST first); step 2: prompts REGION, skip;
	// step 3: run; step 4: run.
	in := strings.NewReader("db-02\nr\neu-1\ns\nr\nrun\n")
	var out, errb, transcript bytes.Buffer
	err := Run(steps, Options{
		Runbook: "failover.md", In: in, Out: &out, Err: &errb,
		Transcript: &transcript, Now: fixedClock(),
	})
	if err != nil {
		t.Fatalf("run: %v\nout=%s\nerr=%s", err, out.String(), errb.String())
	}
	for _, want := range []string{"checking db-02\n", "replica lag ok\n", "node a\nnode b\n"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
	tr := transcript.String()
	if strings.Count(tr, "exit: ") != 3 {
		t.Errorf("skipped step must not run; transcript:\n%s", tr)
	}
	for _, want := range []string{
		"runbook: failover.md",
		"started: 2026-11-12T10:00:00Z",
		"$ echo \"checking db-02\"\nexit: 0",
		"stdout:\n  checking db-02\n",
		"skipped\n$ echo \"promoting db-02 in eu-1\"",
		"var: HOST set at prompt",
		"finished: 2026-11-12T10:00:00Z",
	} {
		if !strings.Contains(tr, want) {
			t.Errorf("transcript missing %q:\n%s", want, tr)
		}
	}
	if strings.Contains(tr, "db-02\nr\n") {
		t.Errorf("transcript should not echo raw input:\n%s", tr)
	}
}

func TestRunStopsOnFailure(t *testing.T) {
	steps := []Step{
		{Index: 1, Lang: "sh", Script: "echo boom >&2; exit 3\n"},
		{Index: 2, Lang: "sh", Script: "echo never\n"},
	}
	var out, errb, transcript bytes.Buffer
	err := Run(steps, Options{In: strings.NewReader("r\nr\n"), Out: &out, Err: &errb, Transcript: &transcript})
	var se *StepError
	if !errors.As(err, &se) || se.ExitCode != 3 || se.Step.Index != 1 {
		t.Fatalf("err = %v, want StepError for step 1 exit 3", err)
	}
	if strings.Contains(out.String(), "never") {
		t.Error("run must stop after a failed step")
	}
	if !strings.Contains(transcript.String(), "exit: 3") || !strings.Contains(transcript.String(), "stderr:\n  boom\n") {
		t.Errorf("transcript = %s", transcript.String())
	}

	out.Reset()
	err = Run(steps, Options{In: strings.NewReader("r\nr\n"), Out: &out, Err: &errb, KeepGoing: true})
	if !errors.As(err, &se) {
		t.Fatalf("keep-going should still report the failure, got %v", err)
	}
	if !strings.Contains(out.String(), "never") {
		t.Error("keep-going should run the next step")
	}
}

func TestRunAbortAndEOF(t *testing.T) {
	steps := []Step{{Index: 1, Lang: "sh", Script: "echo ran\n"}}
	for name, input := range map[string]string{"abort": "a\n", "eof": ""} {
		t.Run(name, func(t *testing.T) {
			var out, errb, transcript bytes.Buffer
			err := Run(steps, Options{In: strings.NewReader(input), Out: &out, Err: &errb, Transcript: &transcript})
			if !errors.Is(err, ErrAborted) {
				t.Fatalf("err = %v, want ErrAborted", err)
			}
			if strings.Contains(transcript.String(), "exit: ") || !strings.Contains(transcript.String(), "aborted") {
				t.Errorf("aborted step must not run; transcript:\n%s", transcript.String())
			}
		})
	}
}

func TestRunEditInline(t *testing.T) {
	steps := []Step{{Index: 1, Lang: "sh", Script: "echo original\n"}}
	var out, errb, transcript bytes.Buffer
	in := strings.NewReader("e\necho edited\n.\nr\n")
	if err := Run(steps, Options{In: in, Out: &out, Err: &errb, Transcript: &transcript}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "edited\n") {
		t.Errorf("edited command did not run:\n%s", out.String())
	}
	if !strings.Contains(transcript.String(), "(edited before running)\n$ echo edited") {
		t.Errorf("transcript = %s", transcript.String())
	}
}

func TestRunEditWithEditor(t *testing.T) {
	dir := t.TempDir()
	ed := filepath.Join(dir, "ed.sh")
	if err := os.WriteFile(ed, []byte("#!/bin/sh\necho 'echo from-editor' > \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	steps := []Step{{Index: 1, Lang: "sh", Script: "echo original\n"}}
	var out, errb bytes.Buffer
	in := strings.NewReader("e\nr\n")
	if err := Run(steps, Options{In: in, Out: &out, Err: &errb, Editor: ed}); err != nil {
		t.Fatalf("run: %v\n%s", err, errb.String())
	}
	if !strings.Contains(out.String(), "from-editor\n") {
		t.Errorf("editor's command did not run:\n%s", out.String())
	}
}

func TestDryRunRunsNothing(t *testing.T) {
	steps := fixture(t)
	var out, errb bytes.Buffer
	err := Run(steps, Options{
		Runbook: "failover.md", DryRun: true, Vars: map[string]string{"HOST": "db-02"},
		Out: &out, Err: &errb, Only: []string{"verify"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"2 step(s) selected of 4", "step 1/4 (bash, line 16) [step=verify]", `echo "checking db-02"`, "step 3/4"} {
		if !strings.Contains(got, want) {
			t.Errorf("plan missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\nchecking db-02") {
		t.Errorf("dry run executed a step:\n%s", got)
	}
}
