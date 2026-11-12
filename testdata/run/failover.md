---
owner: platform-team
severity: sev1
service: orders-db
last-reviewed: 2099-01-01
---

# Fail over orders-db

## Symptoms

Primary `${HOST}` is not accepting writes.

## Diagnosis

```bash step=verify
echo "checking ${HOST}"
```

```console
$ this is a transcript example, not a step
```

## Mitigation

```bash
echo "promoting ${HOST} in ${REGION}"
```

```sh step=verify
echo "replica lag ok"
```

## Escalation

```bash
for NODE in a b; do echo "node ${NODE}"; done
```
