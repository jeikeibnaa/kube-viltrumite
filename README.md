# Kube-Viltrumite

**Safety-first, AI-assisted upgrade planning for your Kubernetes platform stack.**

Kube-Viltrumite is a self-hosted Kubernetes operator that watches the platform tools running in your
cluster (cert-manager, Argo CD, External Secrets, Istio, kube-prometheus-stack, Vault, …), tells you
which upgrades are available, how risky they are, and in what order they should happen — and, when
you approve, executes them.

Where Renovate says *"a new version exists"*, Kube-Viltrumite asks *"is it safe for this stack, and
what has to move first?"*

- **No data leaves the cluster.** Runs entirely in-cluster; AI analysis works with on-prem models
  (Ollama), so it fits regulated environments such as banking.
- **Humans stay in control.** Report-only by default. Upgrades need a plan *and* an approval.
- **AI is optional.** A curated compatibility knowledge base drives every decision; AI adds
  changelog analysis on top and can never lower a risk rating.

> **Status: pre-release (alpha).** APIs are `v1alpha1` and will change. Not for production use yet.
> See the [roadmap](#roadmap-to-v100).

---

## How it works

```
  Helm releases ──┐
  Flux / Argo CD ─┼─▶ Scanners ─▶ Planner ─▶ CompatibilityPolicy status ─▶ Dashboard
  Raw manifests ──┘                  │       (per-tool upgrade report)
                                     ▼
                               StackUpgrade ──(approve)──▶ Executor (Helm)
                                     ▲
                                AIProvider  (Ollama · OpenAI-compatible · Anthropic · none)
```

The planner is driven by a curated compatibility knowledge base (`knowledge/tools/*.yaml`) that
records, per tool version, the minimum Kubernetes version, known-incompatible versions of other
tools, breaking changes, and a risk level.

Two custom resources:

| Resource | Written by | Purpose |
|---|---|---|
| `CompatibilityPolicy` | You | Which tools to track, namespaces to watch, risk tolerance, optional auto-planning. The operator writes a per-tool status report back. |
| `StackUpgrade` | The operator or the dashboard — never by hand | An executable upgrade plan. Phases: `Pending → Approved → Upgrading → Succeeded / Failed / RolledBack`. |

**Pull mode (default):** the operator scans and reports. You click *Plan upgrade*, then *Approve*.
**Push mode (opt-in):** `spec.autoplan` creates plans automatically for upgrades at or below a risk
ceiling; `autoApprove` is intended for non-production clusters only.

```yaml
apiVersion: kubeviltrumite.io/v1alpha1
kind: CompatibilityPolicy
metadata:
  name: platform-stack
  namespace: viltrumite-system
spec:
  watchNamespaces: [cert-manager, vault, argocd]
  trackedTools: []        # empty = discovery mode (track every known tool)
  riskTolerance: MEDIUM   # hide upgrades riskier than this
  scanInterval: 5m
  autoplan:
    enabled: false        # push mode ships disabled
    maxRisk: LOW
    autoApprove: false
```

## What works today

| Area | State |
|---|---|
| CRDs, reconcilers, pull + push model | ✅ Working |
| Detection: Helm, Flux `HelmRelease`, Argo CD `Application`, raw `kubectl apply` installs | 🟡 Working, accuracy fixes in v0.2.0 |
| Compatibility knowledge base (6 tools) | 🟡 Present, being verified and refreshed in v0.2.0 |
| Upgrade execution (Helm SDK, atomic rollback) | 🔴 Experimental, reworked in v0.3.0 |
| Cross-tool ordering and multi-step plans | 🔴 Planned for v0.4.0 |
| Dashboard | 🟡 Upgrade list + approve; full dashboard in v0.5.0 |
| AI providers | 🟡 Ollama adapter works; wiring + OpenAI-compatible/Anthropic in v0.6.0 |

## Development

Prerequisites: Go 1.26+, Node.js 20+, `make`, `kubectl`, and a cluster to point at
([kind](https://kind.sigs.k8s.io/) works well).

```bash
make help        # list targets
make verify      # go vet + unit tests + build (run before every commit)
make generate    # regenerate deepcopy, CRDs and RBAC after changing api/ or RBAC markers
make install     # apply CRDs to the current kube-context
make ui          # build the dashboard into ui/dist
make run         # run the operator locally; dashboard on http://localhost:8082
```

Then apply a policy and watch the report:

```bash
kubectl apply -f config/samples/compatibilitypolicy_sample.yaml
kubectl get compatibilitypolicies -A
```

### Repository layout

```
api/v1alpha1/          CRD types
cmd/operator/          operator entrypoint
internal/ai/           AIProvider interface + adapters
internal/controller/   reconcilers
internal/executor/     Helm upgrade execution
internal/planner/      compatibility matrix + risk
internal/scanner/      cluster + workload scanners
internal/server/       dashboard HTTP API
knowledge/tools/       compatibility knowledge base (YAML)
ui/                    React dashboard
docs/devlog/           per-session development log (English + Mongolian)
```

## Roadmap to v1.0.0

| Release | Theme |
|---|---|
| v0.1.0 | **It installs** — repo hygiene, embedded knowledge base, container image, manifests, CI |
| v0.2.0 | **The report is true** — knowledge base schema v2, accurate version/name matching, refreshed data |
| v0.3.0 | **Approve actually works** — source-aware execution, pre-flight checks, approval in spec |
| v0.4.0 | **The brain** — multi-hop upgrade paths and cross-tool ordering |
| v0.5.0 | **Dashboard** — tracked tools, plan/approve flow, authentication |
| v0.6.0 | **AI layer** — provider wiring, changelog analysis, evaluation harness |
| v0.7.0 | **GitOps PR mode** — pull requests for Flux/Argo-managed tools |
| v0.8.0 | **Hardening** — least-privilege RBAC, validation, audit trail, metrics |
| v0.9.0 | **Distribution** — Helm chart, `vilt` kubectl plugin, signed images |
| v1.0.0 | **Stable** — API freeze (`v1beta1`), multi-version e2e |

Session-by-session progress is logged in [`docs/devlog/`](docs/devlog/README.md).

## License

[Apache License 2.0](LICENSE)

## Disclaimer

The name is a nod to the Viltrumites from the comic series *Invincible*. Kube-Viltrumite is an
independent open-source project and is not affiliated with, endorsed by, or sponsored by Skybound
Entertainment, Image Comics, or the producers of *Invincible*. All related names and trademarks
belong to their respective owners.
