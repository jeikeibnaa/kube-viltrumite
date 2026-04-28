# Kube-Viltrumite

A Kubernetes operator for AI-powered upgrade planning.
Named after the Viltrumites from Invincible — superior beings who impose order.

## Project structure
- cmd/operator/     — operator entrypoint
- cmd/cli/          — vilt kubectl plugin  
- internal/ai/      — AIProvider interface + adapters
- internal/controller/ — StackUpgrade reconciler
- internal/scanner/    — cluster + git scanners
- internal/planner/    — compatibility matrix + upgrade ordering
- api/v1alpha1/        — CRD type definitions
- knowledge/           — YAML compatibility database
- ui/                  — React dashboard

## Key rule
The operator NEVER imports a specific AI provider directly.
Always code to the AIProvider interface in internal/ai/provider.go.

## Test commands
make test        — unit tests
make e2e         — e2e against kind cluster
make generate    — regenerate CRD manifests