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
errors exit `2`.

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
| `--max-age-days N` | `180` | Warn on runbooks not reviewed within N days (`0` disables). |
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
