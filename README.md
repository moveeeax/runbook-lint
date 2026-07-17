# runbook-lint

> Make every runbook complete and consistent before an incident needs it.

**Status:** 🚧 In development

## Overview

Lint operational runbooks for required structure and frontmatter.

## Features

- Enforces required sections (Symptoms, Diagnosis, Mitigation, Escalation)
- Validates YAML frontmatter (owner, severity, service)
- Checks for stale runbooks (last-reviewed date)
- Broken internal link detection
- Pretty + JSON output

## Stack

Go + goldmark markdown AST.

## Usage

```bash
runbook-lint runbooks/
```

## License

MIT
