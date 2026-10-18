// Diagnostics (task 092): ping, traceroute and DNS lookup run on the box.
// Admins only (POST endpoints); at most two run at once — a 429 "busy" means
// another diagnostic is in flight. Traceroute can take ~60 s, so the button
// stays disabled with a spinner the whole time.
import { useEffect, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { inputClass } from '../../components/ConfigList'
import { diagPing, diagTraceroute, diagDNS } from '../../api/diagnostics'
import type { PingResult, TraceResult, DNSResult, DNSType } from '../../api/diagnostics'
import { getCandidate } from '../../api/objects'
import { APIRequestError } from '../../api/client'

type Tab = 'ping' | 'traceroute' | 'dns'

const fmtMs = (n: number) => `${Math.round(n * 10) / 10} ms`

export default function Diagnostics() {
  const [tab, setTab] = useState<Tab>('ping')

  return (
    <div className="space-y-4">
      <div className="flex gap-1 border-b border-gray-200 dark:border-gray-700">
        {(['ping', 'traceroute', 'dns'] as Tab[]).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`px-3 py-1.5 text-sm capitalize ${tab === t ? 'bg-blue-600 text-white' : 'text-gray-600 dark:text-gray-300 hover:bg-gray-100 dark:hover:bg-gray-800'}`}
          >
            {t}
          </button>
        ))}
      </div>
      {tab === 'ping' && <PingTab />}
      {tab === 'traceroute' && <TracerouteTab />}
      {tab === 'dns' && <DnsTab />}
    </div>
  )
}

// errorText renders the server's message; a 429 "busy" gets its own wording.
function errorText(e: unknown): string {
  if (e instanceof APIRequestError && e.status === 429) {
    return 'Another diagnostic is running; try again in a moment.'
  }
  return e instanceof Error ? e.message : String(e)
}

const busyBtn = (running: boolean, label: string) => (
  <button
    type="submit"
    disabled={running}
    className="inline-flex items-center gap-2 rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700 disabled:opacity-50"
  >
    {running && <Loader2 size={14} className="animate-spin" />}
    {label}
  </button>
)

// --- Ping -------------------------------------------------------------------

function PingTab() {
  const [host, setHost] = useState('')
  const [count, setCount] = useState(4)
  const [iface, setIface] = useState('')
  const [ifaces, setIfaces] = useState<string[]>([])
  const [running, setRunning] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<PingResult | null>(null)

  useEffect(() => {
    getCandidate()
      .then((c) => {
        const names = [
          ...(c.interfaces ?? []).filter((i) => i.enabled).map((i) => i.name),
          ...(c.vlans ?? []).filter((v) => v.enabled).map((v) => v.name),
        ]
        setIfaces([...new Set(names)].sort())
      })
      .catch(() => {}) // the select is optional; a missing candidate just leaves it empty
  }, [])

  const run = async (ev: React.FormEvent) => {
    ev.preventDefault()
    setRunning(true)
    setError(null)
    try {
      setResult(await diagPing({ host: host.trim(), count, interface: iface || undefined }))
    } catch (e) {
      setError(errorText(e))
    } finally {
      setRunning(false)
    }
  }

  return (
    <div className="space-y-4">
      <form onSubmit={run} className="flex flex-wrap items-end gap-3">
        <label className="block text-sm">
          Host
          <input
            className={inputClass}
            value={host}
            onChange={(e) => setHost(e.target.value)}
            placeholder="example.com or 1.2.3.4"
            required
          />
        </label>
        <label className="block text-sm">
          Count
          <input
            type="number"
            min={1}
            max={10}
            className={`${inputClass} w-20`}
            value={count}
            onChange={(e) => setCount(Number(e.target.value))}
          />
        </label>
        <label className="block text-sm">
          Source interface
          <select className={inputClass} value={iface} onChange={(e) => setIface(e.target.value)}>
            <option value="">(default)</option>
            {ifaces.map((n) => (
              <option key={n} value={n}>{n}</option>
            ))}
          </select>
        </label>
        {busyBtn(running, 'Ping')}
      </form>
      {error && <div className="rounded bg-red-50 p-2 text-sm text-red-700" role="alert">{error}</div>}
      {result && (
        <div className="space-y-2">
          <p className="text-sm font-medium">
            {result.sent} sent, {result.received} received, {Math.round(result.loss_percent)}% loss,{' '}
            {fmtMs(result.rtt_avg_ms)} average
          </p>
          <pre className="overflow-x-auto rounded bg-gray-50 p-2 font-mono text-xs leading-5 dark:bg-gray-800">
            {result.lines.join('\n')}
          </pre>
        </div>
      )}
    </div>
  )
}

// --- Traceroute ---------------------------------------------------------------

function TracerouteTab() {
  const [host, setHost] = useState('')
  const [maxHops, setMaxHops] = useState(20)
  const [running, setRunning] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<TraceResult | null>(null)

  const run = async (ev: React.FormEvent) => {
    ev.preventDefault()
    setRunning(true)
    setError(null)
    try {
      setResult(await diagTraceroute({ host: host.trim(), max_hops: maxHops }))
    } catch (e) {
      setError(errorText(e))
    } finally {
      setRunning(false)
    }
  }

  return (
    <div className="space-y-4">
      <form onSubmit={run} className="flex flex-wrap items-end gap-3">
        <label className="block text-sm">
          Host
          <input
            className={inputClass}
            value={host}
            onChange={(e) => setHost(e.target.value)}
            placeholder="example.com or 1.2.3.4"
            required
          />
        </label>
        <label className="block text-sm">
          Max hops
          <input
            type="number"
            min={1}
            max={30}
            className={`${inputClass} w-20`}
            value={maxHops}
            onChange={(e) => setMaxHops(Number(e.target.value))}
          />
        </label>
        {busyBtn(running, 'Trace')}
      </form>
      {error && <div className="rounded bg-red-50 p-2 text-sm text-red-700" role="alert">{error}</div>}
      {result && (
        <table className="w-full border-collapse text-sm">
          <thead>
            <tr className="border-b border-gray-200 text-left dark:border-gray-700">
              <th className="py-1 pr-4">Hop</th>
              <th className="py-1 pr-4">Address</th>
              <th className="py-1">Times</th>
            </tr>
          </thead>
          <tbody>
            {result.hops.map((h) => (
              <tr key={h.hop} className={h.address === '*' ? 'text-gray-400 dark:text-gray-500' : ''}>
                <td className="py-1 pr-4">{h.hop}</td>
                <td className="py-1 pr-4 font-mono text-xs">{h.address}</td>
                <td className="py-1 font-mono text-xs">{h.rtt_ms.map(fmtMs).join('  ')}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}

// --- DNS lookup ---------------------------------------------------------------

const DNS_TYPES: DNSType[] = ['A', 'AAAA', 'MX', 'TXT', 'CNAME', 'NS', 'PTR']

function DnsTab() {
  const [name, setName] = useState('')
  const [type, setType] = useState<DNSType>('A')
  const [server, setServer] = useState('box')
  const [running, setRunning] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<DNSResult | null>(null)

  const run = async (ev: React.FormEvent) => {
    ev.preventDefault()
    setRunning(true)
    setError(null)
    try {
      setResult(await diagDNS({ name: name.trim(), type, server }))
    } catch (e) {
      setError(errorText(e))
    } finally {
      setRunning(false)
    }
  }

  return (
    <div className="space-y-4">
      <form onSubmit={run} className="flex flex-wrap items-end gap-3">
        <label className="block text-sm">
          Name
          <input
            className={inputClass}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={type === 'PTR' ? '1.2.3.4 (an IP address)' : 'example.com'}
            required
          />
        </label>
        <label className="block text-sm">
          Type
          <select className={inputClass} value={type} onChange={(e) => setType(e.target.value as DNSType)}>
            {DNS_TYPES.map((t) => (
              <option key={t} value={t}>{t}</option>
            ))}
          </select>
        </label>
        <label className="block text-sm">
          Server
          <input
            className={`${inputClass} w-40`}
            value={server}
            onChange={(e) => setServer(e.target.value)}
            placeholder="box or an IP address"
          />
        </label>
        {busyBtn(running, 'Lookup')}
      </form>
      {error && <div className="rounded bg-red-50 p-2 text-sm text-red-700" role="alert">{error}</div>}
      {result && (
        <div className="space-y-2">
          <p className="text-sm font-medium">
            {result.rcode} · {fmtMs(result.ms)} · via {result.server}
          </p>
          {result.answers.length === 0 ? (
            <p className="text-sm text-gray-500">No answers.</p>
          ) : (
            <ul className="space-y-1 font-mono text-xs">
              {result.answers.map((a, i) => (
                <li key={i} className="rounded bg-gray-50 px-2 py-1 dark:bg-gray-800">{a}</li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  )
}
