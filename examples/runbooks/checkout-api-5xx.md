---
owner: platform-team
severity: sev2
service: checkout-api
last-reviewed: 2099-01-01
---

# Checkout API is returning 5xx

## Symptoms

- Elevated 5xx rate on `checkout-api`
- Alert `CheckoutErrorRate` firing

## Diagnosis

Check the [dependencies runbook](dependencies.md) and the dashboard.

```bash step=verify
kubectl -n ${NAMESPACE} rollout history deploy/checkout-api
```

## Mitigation

Roll back the latest deploy:

```bash
kubectl -n ${NAMESPACE} rollout undo deploy/checkout-api
```

## Escalation

Page the platform on-call if not mitigated in 15 minutes.
