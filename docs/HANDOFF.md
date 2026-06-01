# Kube-Viltrumite — project handoff context

Paste this into a new chat to give full context on the project's current state and the planned work. This captures decisions and prompts as of 2026-06-01.

---

## What the project is

Kube-Viltrumite is an open source, self-hosted Kubernetes operator for AI-powered, safety-first upgrade planning of a CNCF platform/tool stack (observability, GitOps, secrets, service mesh, etc.). It is named after the Viltrumites from *Invincible* — superior beings who impose order on chaotic systems.

It solves a real problem: safely managing version compatibility across a complex tool stack. It is smarter than Renovate — it understands cross-tool compatibility, upgrade ordering, and risk, not just "a new version exists."

Core constraint: no data should leave the cluster. On-prem AI (Ollama) is a strategic differentiator for banking / regulated industries.

GitHub module path username: `jeikeibnaa`. API version observed in cluster: `kubeviltrumite.io/v1alpha1`.

---

## Architecture

Self-hosted Kubernetes operator + web UI, installed via Helm into the user's own cluster.

- Operator (kubebuilder v4 + controller-runtime) with two CRDs:
  - `CompatibilityPolicy` — engineer declares which tools to track, namespaces, risk tolerance, AI config, and (optionally) auto-plan settings. The operator scans and writes a status report.
  - `StackUpgrade` — the executable upgrade plan. Phase state machine: Pending -> Approved -> Upgrading -> Succeeded / Failed / RolledBack.
- AIProvider interface (`internal/ai/provider.go`) with four adapters: Anthropic, OpenAI-compatible, Ollama (on-prem), and Noop (AI-free). The operator only ever calls the interface, never a specific provider.
- Scanners: cluster scanner (FluxCD HelmRelease, plain Helm, ArgoCD Applications) and workload scanner (raw `kubectl apply` installs, detected by Deployment/DaemonSet container image tags).
- Planner: YAML compatibility knowledge base (`knowledge/tools/`) + matrix resolver + upgrade-path ordering + risk scoring.
- Executor: real Helm upgrades via `helm.sh/helm/v3/pkg/action` with atomic rollback.
- Web UI: React + Vite served as static files by a small Go server in the operator; reads CRD status, triggers actions.

### The pull + push model (key design decision)

- Pull (always on): the operator scans and writes a status report per tracked tool. Nothing executes automatically. The engineer reviews the report in the UI and clicks "Plan upgrade" to create a `StackUpgrade`, then "Approve" to execute. Two human gates. This is the safe default for production / banking.
- Push (opt-in, per policy, risk-capped): if `spec.autoplan.enabled` is true, the operator also auto-creates `StackUpgrade` objects for upgrades at or below `autoplan.maxRisk`. If `autoplan.autoApprove` is true it creates them already Approved (for non-prod). Default ships disabled.

The engineer only ever writes `CompatibilityPolicy`. `StackUpgrade` is created either by clicking in the UI (pull) or by the gated push step — never hand-written.

### Tracked-tools modes

- `spec.trackedTools` empty -> discovery mode: track every detectable tool that has a knowledge base entry.
- `spec.trackedTools` populated -> focused mode: track only those named tools. Names must match knowledge base entries; unmatched names are reported as `untrackableTools`.

---

## Tech stack and conventions

- Go 1.23 (required by kubebuilder v4). Chosen over TypeScript for K8s-native ecosystem fit, Helm SDK access, single-binary distribution.
- kubebuilder v4, controller-runtime, helm.sh/helm/v3, go-git, go-github, Anthropic Go SDK, React + Vite.
- Knowledge base: YAML, embedded via go:embed.
- Workflow with Claude Code: one session = one package; always name exact files to touch; end every prompt with "run tests, fix errors before stopping"; commit after every session; use `--continue` for small follow-ups.
- Devlog: `docs/devlog/DEVLOG-{date}.md`, one file per session, with `docs/devlog/README.md` as the index.
- Skills installed in `.claude/skills/`: `code-reviewer` (applied at end of each session) and `senior-prompt-engineer`.
- Custom commands in `.claude/commands/`: `commit`, `todo`, `update-docs`.
- README must disclaim no affiliation with Skybound / Image Comics; avoid official *Invincible* artwork/logos.

---

## Current state (as of 2026-06-01)

Done and passing tests:

- Sessions 1–5: scaffold, AIProvider interface + types, Noop adapter, Ollama adapter, first knowledge base entry (cert-manager) + matrix resolver.
- Session 6: kubebuilder scaffold (hit go-version and existing-files issues; resolved via manual scaffold).
- Sessions 7–8: CRD types (`StackUpgrade`, `CompatibilityPolicy`); StackUpgrade reconciler phase state machine (logging stub, no real Helm yet).
- Session 9: CompatibilityPolicy reconciler + cluster scanner (FluxCD).
- Session 10: main.go wiring (operator builds and runs).
- Session 11–12 (multi-source scanner): plain Helm + ArgoCD + `ScanAll` unification.
- Session 17: version normalization in the planner (fixed the `v1.14.0` vs `1.14` mismatch). normalizeVersion, CompareMinor added.

Known issues found in testing:

- Raw `kubectl apply` installs (e.g. cert-manager installed directly) are NOT detected by the Helm/Flux/ArgoCD scanner — needs the workload scanner (Session 18).
- The Helm executor cannot upgrade raw-installed tools (no Helm release) — returns "release not found". Needs an explicit guard (Session 22).

Not yet built:

- Workload scanner for raw installs (Session 18)
- Tracked-tools list + status report + pull/push modes (Session 19)
- UI tracked-tools dashboard (Session 20)
- "Plan upgrade" UI action (Session 21)
- Raw-install execution guard (Session 22)
- Later: git repo scanner, GitHub PR generation, Helm chart for install, Docker image, first release.

Note: the actual API group/import path is `kubeviltrumite.io/v1alpha1` (no `stack.` prefix). Any new code must read the existing types file and match it rather than assume a group.

---

## Next sessions (18–22) — exact Claude Code prompts

### Session 18 — Workload scanner for raw installs + detection metadata

```
Read CLAUDE.md first.
Read .claude/skills/code-reviewer/SKILL.md.
Only touch:
  internal/scanner/workload.go
  internal/scanner/workload_test.go
  knowledge/tools/cert-manager.yaml

Read internal/scanner/cluster.go first to reuse the InstalledTool
struct (Name, ChartName, CurrentVersion, Namespace, ReleaseName,
Source, RepoURL). Do NOT redefine it.
Read knowledge/tools/cert-manager.yaml to understand the schema.

--- Part 1: add detection metadata to cert-manager.yaml ---

Add a top-level "detection" section:

detection:
  deployments:
    - name: cert-manager
      namespace_hint: cert-manager
      container: cert-manager-controller
      image_contains: "cert-manager-controller"
      version_from: image_tag
  labels:
    app.kubernetes.io/name: cert-manager

This describes how to find cert-manager when installed via raw
kubectl apply (no Helm release exists).
version_from options: image_tag | label | annotation

--- Part 2: WorkloadScanner ---

Create internal/scanner/workload.go.

ToolDetection struct (parsed from the detection section):
  ToolName string
  Deployments []DeploymentMatch
    DeploymentMatch { Name, NamespaceHint, Container,
                      ImageContains, VersionFrom string }
  Labels map[string]string

WorkloadScanner struct:
  Client client.Client

Method:
  ScanWorkloads(ctx, namespaces []string,
                detections []ToolDetection) ([]InstalledTool, error)

Logic:
  - List appsv1.Deployment AND appsv1.DaemonSet across namespaces
  - For each, check against every ToolDetection:
      match if workload name == DeploymentMatch.Name
      OR any container image contains ImageContains
  - Extract version:
      version_from == "image_tag": take substring after last ":" in
        the image, e.g.
        "quay.io/jetstack/cert-manager-controller:v1.14.0" -> "v1.14.0"
      version_from == "label": read that label value
  - Build InstalledTool:
      Name = detection.ToolName
      CurrentVersion = extracted version
      Namespace = workload namespace
      Source = "raw"
      ReleaseName = ""
  - DEDUPLICATE: cert-manager has 3 workloads (controller, webhook,
    cainjector) but is ONE tool. Return one InstalledTool per tool,
    keyed by ToolName, not per workload.

--- Part 3: tests ---

workload_test.go using fake client (sigs.k8s.io/.../client/fake):
  Test 1: one cert-manager Deployment, image
    "quay.io/jetstack/cert-manager-controller:v1.14.0" in ns cert-manager
    -> 1 InstalledTool, Name=cert-manager, CurrentVersion=v1.14.0,
       Source=raw
  Test 2: three cert-manager workloads -> still exactly 1 InstalledTool
  Test 3: unrelated nginx deployment -> empty result
  Test 4: empty namespaces -> empty result, no panic

Run: make test ./internal/scanner/...
Fix all errors before stopping.

Apply code-reviewer skill to workload.go. Report findings.
```

### Session 19 — Matrix helpers + tracked tools + pull AND push modes

```
Read CLAUDE.md first.
Read .claude/skills/code-reviewer/SKILL.md.
Only touch:
  internal/planner/matrix.go
  internal/planner/matrix_test.go
  api/v1alpha1/compatibilitypolicy_types.go
  internal/controller/compatibilitypolicy_controller.go

Read full current content of all four first. Also read:
  internal/scanner/cluster.go
  internal/scanner/workload.go
  api/v1alpha1/stackupgrade_types.go
  internal/ai/provider.go   (for the RiskLevel type)

CRITICAL: match the existing API group / import path exactly as it
appears in compatibilitypolicy_types.go. Do not invent a group.

--- Part 1: add helpers to matrix.go ---

ListTools() []string
  returns all tool names that have a knowledge base entry.

LatestSafeVersion(tool, currentVersion string, tolerance RiskLevel)
  (recommended string, risk RiskLevel, found bool)
  - find all KB versions of tool NEWER than currentVersion
    (use existing CompareMinor from session 17)
  - keep only those whose risk_level is at or below tolerance
  - return the HIGHEST such version, its risk, found=true
  - if none qualify: found=false

RiskAtOrBelow(risk, ceiling RiskLevel) bool
  ordering: LOW < MEDIUM < HIGH < BLOCKING
  returns true if risk <= ceiling

Add table-driven tests for all three in matrix_test.go using the
real cert-manager data:
  ListTools includes "cert-manager"
  LatestSafeVersion("cert-manager","1.13",MEDIUM) returns the
    highest <=MEDIUM version above 1.13
  LatestSafeVersion("cert-manager","1.15",LOW) -> found=false
  RiskAtOrBelow(LOW, MEDIUM)=true, RiskAtOrBelow(HIGH, LOW)=false

--- Part 2: extend CompatibilityPolicy types ---

Add to the Spec:
  TrackedTools []string  // +optional
    // empty   -> discovery mode (track all detectable known tools)
    // set     -> focused mode (track only these)
  Autoplan *AutoplanConfig  // +optional pointer
    AutoplanConfig {
      Enabled bool
      MaxRisk RiskLevel      // +kubebuilder:default=LOW
      AutoApprove bool       // create plan already Approved (non-prod)
    }

Add/extend the Status:
  LastScanTime *metav1.Time
  Mode string                  // "discovery" | "focused"
  Tools []TrackedToolStatus
  UntrackableTools []string    // listed in TrackedTools but no KB entry
  UnknownInstalled []string    // OPTIONAL: installed in cluster, no KB
                               // entry (blind-spot signal). If you do not
                               // want this, omit this field and its logic.

TrackedToolStatus {
  Name, InstalledVersion, Source, Namespace, RecommendedVersion string
  Installed, UpgradeAvailable bool
  Risk RiskLevel
  Message string
}

printcolumns: Mode -> .status.mode, Last Scan -> .status.lastScanTime

--- Part 3: reconciler (pull always, push optional) ---

Rewrite Reconcile:

1. Determine tool list:
   known := matrix.ListTools()
   if len(spec.TrackedTools)==0:
     mode="discovery"; toEval = known
   else:
     mode="focused"
     for n in spec.TrackedTools:
       if n in known: toEval += n
       else: status.UntrackableTools += n
   status.Mode = mode

2. Scan once, reuse:
   clusterTools,_ := clusterScanner.ScanAll(ctx, spec.WatchNamespaces)
   rawTools,_     := workloadScanner.ScanWorkloads(ctx,
                       spec.WatchNamespaces, detections)
   build installed map keyed by tool name; if a tool is found by BOTH
   a helm/flux/argocd source AND raw, PREFER the non-raw source
   (it is upgradeable).
   OPTIONAL: any installed tool whose name is not in `known`
   -> append to status.UnknownInstalled.

3. For each tool in toEval build a TrackedToolStatus:
   default Installed=false, Message="not currently installed in cluster"
   if in installed map:
     Installed=true; set InstalledVersion, Source, Namespace
     rec, risk, found := matrix.LatestSafeVersion(name,
                           installedVersion, spec.RiskTolerance)
     if found:
       UpgradeAvailable=true; RecommendedVersion=rec; Risk=risk
       Message="upgrade available"
     else:
       Message="up to date"
   append to status.Tools

4. PUSH step (only if enabled):
   if spec.Autoplan != nil && spec.Autoplan.Enabled:
     for t in status.Tools where UpgradeAvailable:
       if matrix.RiskAtOrBelow(t.Risk, spec.Autoplan.MaxRisk):
         name := sanitize("auto-"+t.Name+"-"+t.RecommendedVersion)
         if StackUpgrade name does not already exist in policy namespace:
           create StackUpgrade:
             spec.tools=[{Name,CurrentVersion=t.InstalledVersion,
                          TargetVersion=t.RecommendedVersion,Risk=t.Risk}]
             spec.approvalRequired = !spec.Autoplan.AutoApprove
             if spec.Autoplan.AutoApprove: set status.Phase=Approved
   if Autoplan nil or disabled: create NOTHING (pure pull / report-only)

5. status.LastScanTime=now; r.Status().Update(...)
   requeue after spec.ScanInterval (default 5m if zero)

Do NOT keep any unconditional auto-create logic from before.
Creation happens ONLY inside the gated push step above.

Update cmd/operator/main.go:
  inject WorkloadScanner into CompatibilityPolicyReconciler
  load detection metadata from the knowledge base at startup, pass it in

Run: make manifests && make generate && make build && make test
Fix all errors before stopping.

Apply code-reviewer skill to matrix.go and the reconciler. Report findings.
```

### Session 20 — UI: tracked tools dashboard with mode badge + risk badges

```
Read CLAUDE.md first.
Read .claude/skills/code-reviewer/SKILL.md.
Only touch:
  internal/server/server.go
  internal/server/server_test.go
  ui/src/App.tsx

Read full current content of all three first.
Read api/v1alpha1/compatibilitypolicy_types.go for the exact
TrackedToolStatus + status JSON shape (Mode, LastScanTime,
UntrackableTools, UnknownInstalled if present).

--- Part 1: server endpoint ---

GET /api/policies/{namespace}/{name}/tools
  fetch the CompatibilityPolicy, return:
  {
    "mode": "...",
    "lastScanTime": "...",
    "tools": [TrackedToolStatus...],
    "untrackableTools": [...],
    "unknownInstalled": [...]   // include only if the field exists
  }

server_test.go:
  returns 200 with empty tools array when policy has no status yet

--- Part 2: React UI ---

Add a "Tracked tools" section ABOVE the existing StackUpgrades table.
Fetch /api/policies/{ns}/{name}/tools (read first policy for now).

Header row shows:
  "Tracked tools"
  a mode badge on the right:
    discovery mode -> gray badge "discovery"
    focused mode   -> gray badge "focused"
  if policy has autoplan enabled, append a second badge:
    "auto-plan <= {maxRisk}"  (amber)
  "Last scanned: {relative time}"

One card per tool:
  - tool name (bold)
  - status badge:
      green "up to date"        Installed && !UpgradeAvailable
      amber "upgrade available" Installed && UpgradeAvailable
      gray  "not installed"     !Installed
  - if installed: "v{InstalledVersion} via {Source}"
  - if upgradeAvailable: "{InstalledVersion} -> {RecommendedVersion}"
    + risk badge: LOW green, MEDIUM amber, HIGH orange, BLOCKING red
  - if upgradeAvailable AND source != "raw": "Plan upgrade" button
    (stub console.log for now; wired in session 21)
  - if upgradeAvailable AND source == "raw": disabled button with
    tooltip "manual upgrade required (not Helm-managed)"
  - if not installed: muted "not currently installed in cluster"

If untrackableTools non-empty: small warning line listing them
  "No knowledge base entry: {names}"
If unknownInstalled present and non-empty: muted info line
  "Detected but not in knowledge base: {names}"

Keep dark theme (#0d1117 bg, #e6edf3 text). Auto-refresh every 15s.

Run: go build ./... && make test ./internal/server/...
Run: cd ui && npm run build
Fix all errors before stopping.

Apply code-reviewer skill to server.go and App.tsx. Report findings.
```

### Session 21 — "Plan upgrade" creates a StackUpgrade from the UI

```
Read CLAUDE.md first.
Read .claude/skills/code-reviewer/SKILL.md.
Only touch:
  internal/server/server.go
  internal/server/server_test.go
  ui/src/App.tsx

Read full current content of all three first.
Read api/v1alpha1/stackupgrade_types.go and
api/v1alpha1/compatibilitypolicy_types.go for exact shapes and the
real API group / import path.

--- Part 1: POST /api/upgrades ---

body: { namespace, toolName, currentVersion, targetVersion, risk }

Logic:
  - validate toolName against matrix.ListTools() (allowlist).
    unknown -> 400 "unknown tool"
  - sanitize name to RFC 1123: "plan-{tool}-{target}", lowercase,
    dots -> dashes, strip invalid chars
  - build StackUpgrade:
      namespace = request namespace
      spec.approvalRequired = true
      spec.tools = [{Name,CurrentVersion,TargetVersion,Risk}]
  - create via mgr.GetClient().Create
  - already exists -> 409 "an upgrade plan for this tool+version
    already exists"
  - success -> 201 with created object name

  SECURITY (for the reviewer): this creates a cluster resource from
  an HTTP request. Enforce the allowlist + name sanitization. Reject
  anything not in the knowledge base.

server_test.go:
  valid POST -> 201 and object created
  duplicate -> 409
  unknown tool -> 400
  invalid chars in name -> sanitized correctly

--- Part 2: wire the UI button ---

Replace the stub "Plan upgrade" handler with a real POST to
/api/upgrades using the tool's InstalledVersion, RecommendedVersion,
and Risk.
  success -> toast "Upgrade plan created", refresh StackUpgrades table
  409 -> toast "Plan already exists"
  error -> toast with message

End-to-end flow now:
  tracked tool shows upgrade available
  -> Plan upgrade -> StackUpgrade Pending -> appears in table
  -> Approve (existing) -> reconciler executes

Run: go build ./... && make test ./internal/server/...
Run: cd ui && npm run build
Fix all errors before stopping.

Apply code-reviewer skill to server.go and App.tsx.
Report CRITICAL and MAJOR findings.
```

### Session 22 — Guard raw installs in the execution path

```
Read CLAUDE.md first.
Read .claude/skills/code-reviewer/SKILL.md.
Only touch:
  internal/controller/stackupgrade_controller.go
  internal/controller/stackupgrade_controller_test.go

Read full current content of both first, plus
internal/executor/helm.go and api/v1alpha1/stackupgrade_types.go.

Problem: a StackUpgrade may target a tool installed via raw kubectl
apply. HelmExecutor cannot upgrade it (no Helm release exists) and
returns "release not found" — the exact error seen in production.

Add a guard in the Upgrading phase, BEFORE calling Executor.Upgrade:
  - attempt to confirm a Helm release exists for the tool
    (add executor.ReleaseExists(ctx, releaseName, namespace) (bool,error)
     using action.NewGet / action.NewList)
  - if no release exists:
      set Phase=Failed
      set FailureReason="tool is not Helm-managed; manual upgrade
        required. Kube-Viltrumite can only execute Helm-managed upgrades."
      set condition Ready=False reason "NotHelmManaged"
      do NOT requeue
  - if release exists: proceed with the existing Executor.Upgrade flow

This turns the confusing "release not found" into a clear, honest
terminal state.

tests:
  Upgrading phase with a tool that has no Helm release -> Phase=Failed,
    reason NotHelmManaged, FailureReason mentions manual upgrade
  Upgrading phase with an existing release (dry-run executor) ->
    proceeds and succeeds as before

Run: make test ./internal/controller/...
Fix all errors before stopping.

Apply code-reviewer skill to both files. Report findings.
```

---

## Example CompatibilityPolicy (target shape after session 19)

```yaml
apiVersion: kubeviltrumite.io/v1alpha1
kind: CompatibilityPolicy
metadata:
  name: platform-stack
  namespace: viltrumite-system
spec:
  watchNamespaces: [cert-manager, vault, argocd]
  trackedTools: []          # empty = discovery mode
  riskTolerance: MEDIUM
  scanInterval: 5m
  autoplan:
    enabled: false          # push mode; ship disabled (prod-safe default)
    maxRisk: LOW
    autoApprove: false
  ai:
    provider: ollama
    endpoint: "http://ollama.infra.svc.cluster.local:11434"
    model: "llama3.1:70b"
```

---

## After session 22

Full intended behavior: pull report always on; push opt-in and risk-capped; raw installs detected and clearly marked manual-only; the confusing "release not found" replaced with a clear NotHelmManaged terminal state. Later work: git repo scanner, GitHub PR generation, Helm chart for install, Docker image, knowledge base expansion (external-secrets, argo-cd, prometheus-stack, istio, vault), first v0.1.0 release.

Verification after each session: `make build && make test && cd ui && npm run build`.