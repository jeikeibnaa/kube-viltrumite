import { useEffect, useState } from 'react'

interface ToolUpgradeSpec {
  name: string
  currentVersion: string
  targetVersion: string
  risk: string
}

interface StackUpgradeSpec {
  tools?: ToolUpgradeSpec[]
  approvalRequired?: boolean
}

interface StackUpgradeStatus {
  phase?: string
}

interface StackUpgrade {
  name: string
  namespace: string
  spec: StackUpgradeSpec
  status: StackUpgradeStatus
}

const css = `
  *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
  body { background: #0d1117; color: #e6edf3; font-family: system-ui, -apple-system, sans-serif; }
  .app { padding: 0 24px 48px; max-width: 1200px; margin: 0 auto; }
  .header { border-bottom: 1px solid #21262d; padding: 20px 0 16px; margin-bottom: 28px; }
  .header h1 { font-size: 1.4rem; font-weight: 600; letter-spacing: -0.01em; }
  .subtitle { margin-top: 4px; color: #8b949e; font-size: 0.8125rem; }
  .state { color: #8b949e; padding: 12px 0; }
  .error { color: #f85149; }
  .table-wrap { overflow-x: auto; }
  table { width: 100%; border-collapse: collapse; font-size: 0.8125rem; }
  thead { background: #161b22; }
  th { padding: 10px 14px; text-align: left; color: #8b949e; font-weight: 500; border-bottom: 1px solid #21262d; white-space: nowrap; }
  td { padding: 10px 14px; border-bottom: 1px solid #161b22; vertical-align: middle; }
  tr:last-child td { border-bottom: none; }
  tr:hover td { background: #161b22; }
  .empty { text-align: center; color: #8b949e; padding: 32px 0; }
  .phase { display: inline-block; padding: 2px 8px; border-radius: 12px; font-size: 0.75rem; font-weight: 500; }
  .phase-pending  { background: #3d2b00; color: #e3b341; }
  .phase-approved { background: #0a3069; color: #79c0ff; }
  .phase-upgrading{ background: #012e26; color: #56d364; }
  .phase-succeeded{ background: #012e26; color: #56d364; }
  .phase-failed   { background: #3d0614; color: #ff7b72; }
  .phase-rolledback { background: #3d0614; color: #ff7b72; }
  .btn-approve { background: #238636; color: #fff; border: none; padding: 4px 12px; border-radius: 6px; font-size: 0.8125rem; cursor: pointer; }
  .btn-approve:hover { background: #2ea043; }
` as const

async function approveUpgrade(namespace: string, name: string): Promise<void> {
  const res = await fetch(`/api/stackupgrades/${namespace}/${name}/approve`, { method: 'POST' })
  if (!res.ok) throw new Error(`Approve failed: ${res.statusText}`)
}

export default function App() {
  const [upgrades, setUpgrades] = useState<StackUpgrade[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  async function fetchUpgrades() {
    try {
      const res = await fetch('/api/stackupgrades')
      if (!res.ok) throw new Error(`HTTP ${res.status}: ${res.statusText}`)
      const data: StackUpgrade[] = await res.json()
      setUpgrades(data ?? [])
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void fetchUpgrades()
    const id = setInterval(() => { void fetchUpgrades() }, 10_000)
    return () => clearInterval(id)
  }, [])

  async function handleApprove(ns: string, name: string) {
    try {
      await approveUpgrade(ns, name)
      await fetchUpgrades()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  const tools = (u: StackUpgrade) =>
    u.spec.tools?.map((t) => `${t.name} ${t.currentVersion}→${t.targetVersion}`).join(', ') ?? '—'

  const topRisk = (u: StackUpgrade) => u.spec.tools?.[0]?.risk ?? '—'

  const phase = (u: StackUpgrade) => (u.status.phase ?? 'unknown').toLowerCase()

  return (
    <>
      <style>{css}</style>
      <div className="app">
        <header className="header">
          <h1>Kube-Viltrumite</h1>
          <p className="subtitle">Viltrumite-grade Kubernetes upgrade intelligence</p>
        </header>

        <main>
          {loading && <p className="state">Loading upgrades…</p>}
          {error && <p className="state error">Error: {error}</p>}
          {!loading && !error && (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Namespace</th>
                    <th>Phase</th>
                    <th>Tools</th>
                    <th>Risk</th>
                    <th>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {upgrades.length === 0 ? (
                    <tr>
                      <td colSpan={6} className="empty">No StackUpgrades found.</td>
                    </tr>
                  ) : (
                    upgrades.map((u) => (
                      <tr key={`${u.namespace}/${u.name}`}>
                        <td>{u.name}</td>
                        <td>{u.namespace}</td>
                        <td>
                          <span className={`phase phase-${phase(u)}`}>
                            {u.status.phase ?? '—'}
                          </span>
                        </td>
                        <td>{tools(u)}</td>
                        <td>{topRisk(u)}</td>
                        <td>
                          {u.status.phase === 'Pending' && (
                            <button
                              className="btn-approve"
                              onClick={() => { void handleApprove(u.namespace, u.name) }}
                            >
                              Approve
                            </button>
                          )}
                        </td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            </div>
          )}
        </main>
      </div>
    </>
  )
}
