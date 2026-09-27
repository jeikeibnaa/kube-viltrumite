# Project Todos

Roadmap to v1.0.0. One item = one Claude Code session. See `docs/HANDOFF.md` for the full scope of each session.

## Active
- [ ] [v0.1.0] S21 — Embed KB via go:embed, multi-stage Dockerfile, fill config/ (manager, kustomize), add missing RBAC markers (Flux helmreleases, Argo applications, secrets, leases, events), UI binds localhost + drop CORS *
- [ ] [v0.1.0] S22 — GitHub Actions CI (vet, lint, test, image) + kind smoke test + `make e2e`; tag v0.1.0
- [ ] [v0.2.0] S23 — KB schema v2: chart names/aliases, app_version vs chart_version, source links, real semver, KB validation test
- [ ] [v0.2.0] S24 — Scanners: Flux helm.toolkit.fluxcd.io/v2, name by chart, report app version, Argo multi-source, detection for all 6 tools
- [ ] [v0.2.0] S25 — CompatibilityPolicy reconciler tests + envtest
- [ ] [v0.2.0] S26 — Verify all 6 KB tools against upstream release notes and extend to current releases; tag v0.2.0
- [ ] [v0.3.0] S27 — StackUpgrade API: releaseName/namespace/source/chart repo per tool; approval moves from status to spec; drop Requeue:true
- [ ] [v0.3.0] S28 — Pre-flight gate: min_kubernetes vs cluster version, incompatible_with vs installed stack
- [ ] [v0.3.0] S29 — Execution router by source; raw -> NotHelmManaged terminal state; honest Failed/RolledBack
- [ ] [v0.3.0] S30 — e2e on kind: helm-install old cert-manager -> plan -> approve -> upgraded; tag v0.3.0
- [ ] [v0.4.0] S31 — Multi-hop upgrade paths (no minor skipping where KB requires it)
- [ ] [v0.4.0] S32 — Cross-tool dependency ordering (topological sort from incompatible_with/requires)
- [ ] [v0.4.0] S33 — Planner wired into policy reconciler; autoplan creates ordered multi-step plans; tag v0.4.0
- [ ] [v0.5.0] S34 — UI: tracked tools dashboard + /api/policies/{ns}/{name}/tools
- [ ] [v0.5.0] S35 — UI: "Plan upgrade" -> POST /api/upgrades (allowlist + name sanitization)
- [ ] [v0.5.0] S36 — UI: plan detail view (steps, breaking changes, preflight, approve/reject, progress)
- [ ] [v0.5.0] S37 — UI auth (TokenReview/SubjectAccessReview or OIDC); tag v0.5.0
- [ ] [v0.6.0] S38 — Prompts -> internal/ai/prompts/; provider from policy spec.ai + secretRef
- [ ] [v0.6.0] S39 — Real Anthropic + OpenAI-compatible adapters
- [ ] [v0.6.0] S40 — Changelog source (optional, air-gap safe); AI summary on StackUpgrade status; AI can raise, never lower, KB risk
- [ ] [v0.6.0] S41 — AI eval harness (golden changelogs -> expected risk); tag v0.6.0
- [ ] [v0.7.0] S42 — Git scanner (go-git): locate HelmRelease/Application manifests for installed tools
- [ ] [v0.7.0] S43 — GitHub PR generation for GitOps-managed upgrades
- [ ] [v0.7.0] S44 — StackUpgrade tracks PR state until GitOps reports the new version; tag v0.7.0
- [ ] [v0.8.0] S45–47 — Hardening: least-privilege RBAC, CEL validation, Events/audit trail, Prometheus metrics, NetworkPolicy, security review; tag v0.8.0
- [ ] [v0.9.0] S48–51 — Distribution: operator Helm chart, `vilt` kubectl plugin, goreleaser + multi-arch + cosign + SBOM, docs; tag v0.9.0
- [ ] [v1.0.0] S52–54 — API v1beta1 freeze, e2e across 3 k8s versions, 0.9 -> 1.0 upgrade test, CHANGELOG; tag v1.0.0
- [ ] [owner] Decide whether to purge docs/devlog/test + the 97MB operator binary from git history (history rewrite + force push)
- [ ] [owner] Install GitHub CLI (`winget install GitHub.cli`) and run `gh auth login` so session PRs open automatically

## Completed
- [x] [v0.1.0] S20 — Repo hygiene: remove stray devlog/test + tracked binary, fix Makefile, LICENSE, README, bilingual devlog + PR workflow | Done: 09-27-2026
- [x] S1–S19 — Scaffold, AI interface + adapters, CRDs, reconcilers, scanners (Flux/Helm/Argo/raw), matrix, Helm executor, UI scaffold, pull+push model | Done: 06-01-2026
