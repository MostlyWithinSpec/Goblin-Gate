// Policy simulator panel (task 073): asks POST /api/v1/policy/simulate what
// would happen to a connection, then shows the verdict, the matched policy as
// a link into the editor, and every step the flow took through the chain.
import React, { useState } from 'react'
import { Link } from 'react-router-dom'
import { Check, X } from 'lucide-react'
import { EditDrawer } from '../../components/EditDrawer'
import { simulatePolicy, type SimProtocol, type SimulateRequest, type SimulateResult } from '../../api/policy'

const input = 'w-full rounded border border-gray-300 px-2 py-1 text-sm dark:border-gray-600 dark:bg-gray-700'

// datetime-local values are naive ("2026-10-17T14:30"); the API wants RFC 3339.
const toRFC3339 = (v: string): string => new Date(v).toISOString()

interface Props {
  interfaces: string[] // candidate interface + vlan names for src_intf
  initial?: SimulateRequest // prefilled from ?simulate=1 or a log event
  onClose: () => void
}

export default function PolicySimulator({ interfaces, initial, onClose }: Props) {
  const [srcIP, setSrcIP] = useState(initial?.src_ip ?? '')
  const [dstIP, setDstIP] = useState(initial?.dst_ip ?? '')
  const [protocol, setProtocol] = useState<SimProtocol>(initial?.protocol ?? 'any')
  const [dstPort, setDstPort] = useState(initial?.dst_port != null ? String(initial.dst_port) : '')
  const [srcIntf, setSrcIntf] = useState(initial?.src_intf ?? '')
  const [at, setAt] = useState('') // datetime-local; "" = now
  const [config, setConfig] = useState<'running' | 'candidate'>(initial?.config ?? 'running')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<SimulateResult | null>(null)

  const showPort = protocol === 'tcp' || protocol === 'udp'

  const run = async () => {
    setBusy(true)
    setError(null)
    try {
      const body: SimulateRequest = { src_ip: srcIP.trim(), dst_ip: dstIP.trim(), protocol }
      if (showPort && dstPort.trim()) body.dst_port = Number(dstPort)
      if (srcIntf) body.src_intf = srcIntf
      if (at) body.at = toRFC3339(at)
      body.config = config
      setResult(await simulatePolicy(body))
    } catch (e) {
      setResult(null)
      setError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <EditDrawer isOpen onClose={onClose} title="Simulate a connection">
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault()
          void run()
        }}
      >
        <label className="block text-sm text-gray-700 dark:text-gray-300">
          Source IP
          <input aria-label="Source IP" className={input + ' mt-1'} value={srcIP} onChange={(e) => setSrcIP(e.target.value)} placeholder="192.168.1.20" />
        </label>
        <label className="block text-sm text-gray-700 dark:text-gray-300">
          Destination IP
          <input aria-label="Destination IP" className={input + ' mt-1'} value={dstIP} onChange={(e) => setDstIP(e.target.value)} placeholder="8.8.8.8" />
        </label>
        <div className="grid grid-cols-2 gap-3">
          <label className="block text-sm text-gray-700 dark:text-gray-300">
            Protocol
            <select aria-label="Protocol" className={input + ' mt-1'} value={protocol} onChange={(e) => setProtocol(e.target.value as SimProtocol)}>
              {(['any', 'tcp', 'udp', 'icmp'] as SimProtocol[]).map((p) => (
                <option key={p} value={p}>{p}</option>
              ))}
            </select>
          </label>
          {showPort && (
            <label className="block text-sm text-gray-700 dark:text-gray-300">
              Destination port
              <input aria-label="Destination port" type="number" min={1} max={65535} className={input + ' mt-1'} value={dstPort} onChange={(e) => setDstPort(e.target.value)} placeholder="443" />
            </label>
          )}
        </div>
        <div className="grid grid-cols-2 gap-3">
          <label className="block text-sm text-gray-700 dark:text-gray-300">
            Source interface
            <select aria-label="Source interface" className={input + ' mt-1'} value={srcIntf} onChange={(e) => setSrcIntf(e.target.value)}>
              <option value="">(from source IP)</option>
              {interfaces.map((i) => (
                <option key={i} value={i}>{i}</option>
              ))}
            </select>
          </label>
          <label className="block text-sm text-gray-700 dark:text-gray-300">
            Time
            <input aria-label="Time" type="datetime-local" className={input + ' mt-1'} value={at} onChange={(e) => setAt(e.target.value)} />
          </label>
        </div>
        <label className="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
          <input type="checkbox" checked={config === 'candidate'} onChange={(e) => setConfig(e.target.checked ? 'candidate' : 'running')} />
          Simulate against the candidate config (unchecked = running)
        </label>

        {error && <div role="alert" className="rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900 dark:text-red-200">{error}</div>}

        <button type="submit" disabled={busy || !srcIP.trim() || !dstIP.trim()} className="rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700 disabled:opacity-50">
          {busy ? 'Simulating…' : 'Simulate connection'}
        </button>
      </form>

      {result && (
        <div className="mt-4 space-y-3 border-t border-gray-200 pt-4 dark:border-gray-600">
          <div
            data-testid="sim-verdict"
            className={`rounded px-3 py-2 text-center text-xl font-bold text-white ${result.verdict === 'accept' ? 'bg-green-600' : 'bg-red-600'}`}
          >
            {result.verdict.toUpperCase()}
          </div>
          <p className="text-sm text-gray-700 dark:text-gray-300">{result.reason}</p>

          <div className="flex flex-wrap gap-1">
            <span className="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-700 dark:bg-gray-700 dark:text-gray-200">chain: {result.chain}</span>
            <span className="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-700 dark:bg-gray-700 dark:text-gray-200">{result.src_intf}{result.dst_intf ? ` → ${result.dst_intf}` : ''}</span>
            {result.nat && <span className="rounded bg-blue-100 px-1.5 py-0.5 text-xs text-blue-800 dark:bg-blue-900 dark:text-blue-200">NAT</span>}
            {result.gateway && <span className="rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-700 dark:bg-gray-700 dark:text-gray-200">gw: {result.gateway}</span>}
            {Object.entries(result.profiles ?? {}).map(([k, v]) => (
              <span key={k} className="rounded bg-purple-100 px-1.5 py-0.5 text-xs text-purple-800 dark:bg-purple-900 dark:text-purple-200">
                {k}: {v}
              </span>
            ))}
          </div>

          {result.blocked_by && (
            <p className="text-sm font-medium text-red-700 dark:text-red-300">
              Blocked by {result.blocked_by.kind === 'temp_block' ? 'temporary block' : 'threat feed'}: {result.blocked_by.name}
            </p>
          )}
          {result.translated && <p className="text-sm text-gray-600 dark:text-gray-300">{result.translated}</p>}

          {result.policy ? (
            <Link to={`/policy?edit=${result.policy.id}`} className="inline-block rounded bg-blue-600 px-3 py-1 text-sm text-white hover:bg-blue-700">
              Matched policy: {result.policy.name}
            </Link>
          ) : (
            <p className="text-sm text-gray-500">No policy matched.</p>
          )}

          {result.notes && result.notes.length > 0 && (
            <ul className="list-disc pl-5 text-xs text-gray-500 dark:text-gray-400">
              {result.notes.map((n) => (
                <li key={n}>{n}</li>
              ))}
            </ul>
          )}

          <div>
            <h3 className="mb-1 text-sm font-semibold text-gray-700 dark:text-gray-200">Steps</h3>
            <ol className="space-y-1">
              {result.steps.map((s) => (
                <li
                  key={s.id}
                  data-testid={`sim-step-${s.id}`}
                  className={`flex items-center gap-2 rounded px-2 py-1 text-sm ${s.matched ? 'bg-yellow-50 dark:bg-yellow-900/40' : ''}`}
                >
                  {s.matched ? <Check size={14} className="text-green-600" /> : <X size={14} className="text-red-500" />}
                  <span className="flex-1">
                    {s.name}
                    {result.policy?.id === s.id && (
                      <Link to={`/policy?edit=${s.id}`} className="ml-1 text-blue-600 hover:underline dark:text-blue-400">open</Link>
                    )}
                  </span>
                  <span className="text-xs text-gray-500">{s.reason}</span>
                </li>
              ))}
            </ol>
          </div>
        </div>
      )}
    </EditDrawer>
  )
}
