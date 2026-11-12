# runbook-lint

[![ci](https://github.com/moveeeax/runbook-lint/actions/workflows/ci.yml/badge.svg)](https://github.com/moveeeax/runbook-lint/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/go-1.26%2B-00ADD8?logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)

Lint operational runbooks for the things you always wish you'd checked *before*
an incident needs them: required sections, ownership metadata, staleness, and
broken internal links.

A runbook that's missing a **Mitigation** section — or that links to a page
someone deleted six months ago — is worse than no runbook, because you only find
out at 3am. `runbook-lint` turns those gaps into a failing check in CI.

## What it checks

- **Required sections** — every runbook must have `Symptoms`, `Diagnosis`,
  `Mitigation`, and `Escalation` headings (case-insensitive).
- **Frontmatter** — a YAML block with `owner`, `severity`, and `service`.
- **Staleness** — warns when `last-reviewed` is older than `--max-age-days`
  (default 180).
- **Broken internal links** — relative Markdown links that don't resolve to a
  file on disk. External (`http`), `mailto:`, and in-page `#anchor` links are
  ignored.

Errors fail the run (exit code `1`); staleness is a warning (exit `0`). Usage
errors exit `2`. Sections, frontmatter fields and the age limit can be changed
per repository, directory or severity with a
[configuration file](#configuration-file).

## Install

Requires Go 1.26 or newer.

```shell
go install github.com/moveeeax/runbook-lint@latest
```

## Usage

Point it at files or directories (directories are walked recursively):

```shell
runbook-lint runbooks/
```

```
runbooks/db-slow.md
  error   [frontmatter] missing required frontmatter field "service"
  warning [stale] last reviewed 2020-01-01 (2419 days ago, max 180)
  error   [section] missing required section "Mitigation"
  error   [link] broken internal link "does-not-exist.md"

3 error(s), 1 warning(s) across 1 file(s)
```

Machine-readable output for dashboards or a CI annotation step:

```shell
runbook-lint --json runbooks/
```

Flags:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--json` | off | Emit findings as a JSON array. |
| `--max-age-days N` | `180` | Warn on runbooks not reviewed within N days (`0` disables). Overrides the config file, severity rules included. |
| `--config PATH` | nearest `.runbook-lint.yaml` | Use this config file for every runbook. |
| `--version` | | Print the version and exit. |

A runbook that passes looks like this — see [`examples/runbooks`](examples/runbooks):

```markdown
---
owner: platform-team
severity: sev2
service: checkout-api
last-reviewed: 2026-08-01
---

# Checkout API is returning 5xx

## Symptoms
...
## Diagnosis
...
## Mitigation
...
## Escalation
...
```

## Configuration file

The built-in rules are a default, not a requirement. To use other headings or
extra frontmatter, add a `.runbook-lint.yaml`. For each runbook the nearest one
found walking up from the runbook's directory is used; `--config PATH` uses one
file for everything instead.

```yaml
# Replace the defaults. Leave a key out to keep the built-in value;
# an empty list ([]) requires nothing.
required_sections: [Symptoms, Impact, Mitigation, Rollback]
required_frontmatter: [owner, severity, service, runbook-version]
max_age_days: 365

# Applied in order on top of the rules above. Globs are relative to the
# directory of this file; ** matches any number of directories.
overrides:
  - glob: "sev1/**"
    required_sections: [Symptoms, Impact, Mitigation, Rollback, Escalation]
  - glob: "**/legacy-*.md"
    max_age_days: 0

# Applied last, by the runbook's own `severity` frontmatter (case-insensitive).
severity_rules:
  sev1:
    max_age_days: 90
```

Each override and severity rule takes the same three keys as the top level.
Unknown keys and negative ages are an error (exit `2`), so a typo does not
quietly fall back to the defaults. `--max-age-days` wins over every
`max_age_days` in the file.

## Running a runbook

`runbook-lint run` executes the fenced `bash` and `sh` blocks of a runbook in
document order, so the documented procedure and what was actually run cannot
drift apart. Other fences (`console`, `yaml`, ...) are treated as
documentation.

```shell
runbook-lint run runbooks/failover.md --var HOST=db-02 --transcript ./inc-1043.log
```

Every step is shown with its number and source line and waits for an answer:
`r` runs it, `s` skips it, `e` edits it before running (in `$VISUAL` or
`$EDITOR`; without either, type the replacement and end it with a line holding
only `.`), `a` aborts. End of input counts as abort.

- `${NAME}` placeholders with upper-case names are filled from `--var NAME=VALUE`
  or prompted for once, the first time a step needs them, then reused. Names
  set in the environment are left for the shell to expand, and so are names
  the step assigns itself (`NAME=...`, `read NAME`, `for NAME in`). `${lower}`
  and `$NAME` are never touched.
- `--transcript FILE` appends a timestamped record of each step: the command
  as run, whether it was edited or skipped, exit code, duration, stdout and
  stderr. The file is created with mode `0600`, since output can contain
  secrets.
- A step that exits non-zero stops the run with exit code `1` and prints the
  `--from N` to resume with. `--keep-going` continues instead.
- `--dry-run` prints the selected steps, with `--var` values filled in, and
  runs nothing.
- `--from N` starts at step N. `--only verify` runs only the steps whose fence
  carries `step=verify`, e.g. a read-only triage pass:

  ````markdown
  ```bash step=verify
  kubectl -n ${NAMESPACE} rollout history deploy/checkout-api
  ```
  ````

Each step runs as `bash -c` or `sh -c` in its own process from the current
directory, so `cd` and variables do not carry over between steps. Commands
get the terminal's stdin; their stdout and stderr go through a pipe to be
captured, so programs that check for a TTY may switch to non-interactive
output. Ctrl-C stops the running command, the result is recorded, and the run
stops.

Exit codes: `0` every selected step ran or was skipped, `1` a step failed or
the run was aborted, `2` usage or I/O error. To lint a directory literally
named `run`, pass it as `./run`.

## How it works

Frontmatter is split off the top `---` fence and parsed with `go.yaml.in/yaml/v3`.
The Markdown body is parsed into an AST with
[`goldmark`](https://github.com/yuin/goldmark), and a single tree walk collects
every heading and link. Sections are matched against the required set,
relative links are resolved against the runbook's directory with `os.Stat`, and
`last-reviewed` is compared to a reference clock that's injectable so tests stay
deterministic.

## CI integration

### GitHub Action

```yaml
- uses: moveeeax/runbook-lint@v0
  with:
    path: runbooks/
    args: --max-age-days 90
```

The action builds the `runbook-lint` version matching the ref you pin
(`@v0` resolves to the newest `v0.x.y` release, `@v0.2.0` to exactly that one).
Override the toolchain with the `go-version` input (default `1.26`).

### pre-commit

```yaml
repos:
  - repo: https://github.com/moveeeax/runbook-lint
    rev: v0.1.0
    hooks:
      - id: runbook-lint
```

## Development

```shell
go test ./...
go build -o runbook-lint .
```

## License

MIT — see [LICENSE](LICENSE).
