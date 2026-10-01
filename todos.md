# Project Todos

Roadmap to v1.0.0. One item = one Claude Code session. See `docs/HANDOFF.md` for the full scope of each session.

## Active
- [ ] [v0.3.0] S27 — StackUpgrade API: releaseName/namespace/source/chart repo per tool plus the managing object (HelmRelease/Application kind, namespace, name); approval moves from status to spec; drop Requeue:true
- [ ] [v0.3.0] S28 — Pre-flight gate: min_kubernetes vs cluster version, incompatible_with vs installed stack (keys through `CanonicalName`; define what a partial version like "1.14" matches); decide whether a cluster below `min_kubernetes` blocks or warns: since S26 it is upstream's supported or tested floor, stricter than the chart `kubeVersion` (external-secrets 2.9+ says 1.36, the kind e2e node is 1.32.2)
- [ ] [v0.3.0] S29 — Execution router by source; raw -> NotHelmManaged terminal state; honest Failed/RolledBack
- [ ] [v0.3.0] S30 — e2e on kind: helm-install old cert-manager -> plan -> approve -> upgraded; tag v0.3.0
- [ ] [v0.4.0] S31 — Multi-hop upgrade paths (no minor skipping where KB requires it); an upgrade's risk is the highest risk of every KB entry it crosses (today only the target counts: cert-manager 1.17 → 1.21 reads MEDIUM though 1.18 is HIGH, S26 review), and a patch upgrade inside a minor does not carry the minor's risk
- [ ] [v0.4.0] S32 — Cross-tool dependency ordering (topological sort from incompatible_with/requires)
- [ ] [v0.4.0] S33 — Planner wired into policy reconciler; autoplan creates ordered multi-step plans (and falls back to the newest version within `maxRisk` when the recommended one is above it); the report says when newer versions exist above `riskTolerance` instead of "up to date" (S25 review); tag v0.4.0
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
- [ ] [v0.8.0] S45–47 — Hardening: least-privilege RBAC (incl. narrowing cluster-wide Helm `secrets` get/list, HANDOFF #15), CEL validation, Events/audit trail, a CompatibilityPolicy status condition when a scan fails (today the tools read "not currently installed", S25 review), Prometheus metrics, NetworkPolicy, security review; tag v0.8.0
- [ ] [v0.9.0] S48–51 — Distribution: operator Helm chart, `vilt` kubectl plugin, goreleaser + multi-arch + cosign + SBOM (the version from ldflags instead of the literal in `internal/server/server.go`), docs; tag v0.9.0
- [ ] [v1.0.0] S52–54 — API v1beta1 freeze, e2e across 3 k8s versions, 0.9 -> 1.0 upgrade test, CHANGELOG; tag v1.0.0
- [ ] [backlog] KB freshness check: a script (CI job, e.g. weekly) that compares the six chart indexes with `knowledge/tools/` and flags minors without an entry; external-secrets ships a minor every month, and Istio 1.31 moved its chart repository to blob.istio.io (S26 review)
- [ ] [owner] Tag v0.2.0 at the S26 merge commit on `main` (exit criteria met in S24–S26)
- [ ] [owner] Decide whether to purge docs/devlog/test + the 97MB operator binary from git history (history rewrite + force push)
- [ ] [owner] Decide on Dependabot for `github-actions`: ci.yml pins actions by commit SHA, and Dependabot would open PRs to bump them

## Completed
- [x] [v0.2.0] S26 — Knowledge base verified against upstream and extended to current releases (audit #12): one entry per minor up to cert-manager 1.21, Argo CD 3.5, Istio 1.31, External Secrets 2.11, Prometheus Operator 0.94, Vault 2.0.4; charts and sources checked by script; upstream `min_kubernetes`; unsourced `incompatible_with` removed; no ingress-nginx entry (retired upstream); Istio 1.30 image rule; reconcile tests updated; checked on kind with Argo CD, Helm and raw; version 0.2.0 | Done: 10-01-2026
- [x] [v0.2.0] S25 — CompatibilityPolicy reconciler tests: fake client + status subresource over the real scanners (modes, aliases, every message, riskTolerance, autoplan gate/idempotence/autoApprove, requeue), envtest for CRD validation/defaults/status subresource (`make envtest`, CI job); fixed `riskTolerance` without enum, `spec.ai` rejecting Go clients, duplicate untrackable names | Done: 09-30-2026
- [x] [v0.2.0] S24 — Scanners report canonical names and app versions: Flux HelmRelease v2 (beta fallback, history, cluster-wide by watched namespace, merged with its Helm release), Helm AppVersion, Argo CD multi-source + images, raw StatefulSets + detection for all 6 tools, `IdentifyChart`/`alias_origins`, GitOps > Helm > raw; checked on kind | Done: 09-30-2026
- [x] [owner] Tag v0.1.0 at the S22 merge commit `7362ba4` | Done: 09-30-2026
- [x] [v0.2.0] S23 — KB schema v2: canonical names + aliases (`Matrix.CanonicalName`), `app_version`/`chart_version` from upstream charts, release-notes `source` per version, full semver (`CompareVersions`), strict `Load` validating the whole KB; kind e2e green | Done: 09-28-2026
- [x] [v0.1.0] S22 — GitHub Actions CI (verify + generate-diff, golangci-lint + shellcheck, UI, image build, kind e2e) + `make e2e` kind smoke test, green in CI and locally; first real build of the S21 Dockerfile; README "Install in a cluster" (tagging v0.1.0 left to the owner) | Done: 09-27-2026
- [x] [owner] Install Docker Desktop so sessions can build and kind-test the operator image locally (disk image on V:) | Done: 09-27-2026
- [x] [owner] Install GitHub CLI (`winget install GitHub.cli`) and run `gh auth login` so session PRs open automatically | Done: 09-27-2026
- [x] [v0.1.0] S21 — Embed KB via go:embed, multi-stage Dockerfile, fill config/ (manager, kustomize), add missing RBAC markers (Flux helmreleases, Argo applications, secrets, leases, events), UI binds localhost + drop CORS * (+ CSRF and DNS-rebinding guards) | Done: 09-27-2026
- [x] [v0.1.0] S20 — Repo hygiene: remove stray devlog/test + tracked binary, fix Makefile, LICENSE, README, bilingual devlog + PR workflow | Done: 09-27-2026
- [x] S1–S19 — Scaffold, AI interface + adapters, CRDs, reconcilers, scanners (Flux/Helm/Argo/raw), matrix, Helm executor, UI scaffold, pull+push model | Done: 06-01-2026
