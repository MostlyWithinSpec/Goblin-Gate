// Throughput test and network cards (System → Diagnostics). The box runs an
// iperf3 server on one LAN-side interface for a few minutes; a computer on
// that network runs the iperf3 client and the result shows up here. The
// network card tab lists driver, link, queues and offloads per interface with
// hints for slow throughput. Offload overrides are edited on the Interfaces
// page (interfaces[].offloads).
import { useCallback, useEffect, useRef, useState } from 'react'
import { Copy, Loader2 } from 'lucide-react'
import { inputClass } from '../../components/ConfigList'
import { getNICs, getThroughput, startThroughput, stopThroughput } from '../../api/diagnostics'
import type { NICHint, NICInfo, ThroughputState } from '../../api/diagnostics'
import { getCandidate } from '../../api/objects'
import { APIRequestError } from '../../api/client'
import { language, t } from '../../i18n'

const pollMs = 2000

// lanTargets lists the enabled interfaces and VLANs on the LAN side with a
// static IPv4 address: the only ones the server accepts.
async function lanTargets(): Promise<string[]> {
  const c = await getCandidate()
  const wan = new Set((c.interfaces ?? []).filter((i) => i.role === 'wan').map((i) => i.name))
  const v4 = (a?: string) => !!a && !a.includes(':')
  return [
    ...(c.interfaces ?? []).filter((i) => i.enabled && !wan.has(i.name) && i.mode === 'static' && v4(i.address)).map((i) => i.name),
    ...(c.vlans ?? []).filter((v) => v.enabled && !wan.has(v.parent) && v.mode === 'static' && v4(v.address)).map((v) => v.name),
  ].sort()
}

function throughputError(e: unknown): string {
  if (e instanceof APIRequestError) {
    switch (e.code) {
      case 'throughput_running': return t('throughput.err.running')
      case 'wan_interface': return t('throughput.err.wan')
      case 'interface_disabled': return t('throughput.err.disabled')
      case 'no_address': return t('throughput.err.noAddress')
      case 'unknown_interface': return t('throughput.err.unknown')
      case 'invalid_minutes': return t('throughput.err.minutes')
    }
  }
  return e instanceof Error ? e.message : String(e)
}

export const mbit = (bps: number) => (Math.round(bps / 1e5) / 10).toFixed(1)

function countdown(expires: string | undefined, now: number): string {
  if (!expires) return ''
  const left = Math.max(0, Math.round((Date.parse(expires) - now) / 1000))
  return `${Math.floor(left / 60)}:${String(left % 60).padStart(2, '0')}`
}

function CopyLine({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false)
  const copy = () => {
    navigator.clipboard?.writeText(text).then(() => setCopied(true), () => {})
  }
  return (
    <div className="flex items-center gap-2">
      <span className="w-24 shrink-0 text-xs opacity-70">{label}</span>
      <code className="min-w-0 flex-1 overflow-x-auto rounded bg-gray-100 px-2 py-1 font-mono text-xs dark:bg-gray-800">{text}</code>
      <button type="button" onClick={copy} aria-label={t('throughput.copy', { what: label })} className="rounded p-1 text-gray-500 hover:bg-gray-100 dark:hover:bg-gray-800">
        <Copy size={14} />
      </button>
      {copied && <span className="text-xs text-green-600">{t('throughput.copied')}</span>}
    </div>
  )
}

export function ThroughputTab() {
  const [targets, setTargets] = useState<string[]>([])
  const [iface, setIface] = useState('')
  const [minutes, setMinutes] = useState(5)
  const [state, setState] = useState<ThroughputState | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [now, setNow] = useState(() => Date.now())
  const timer = useRef<ReturnType<typeof setInterval> | null>(null)

  const refresh = useCallback(() => {
    getThroughput().then(setState).catch((e) => setError(throughputError(e)))
  }, [])

  useEffect(() => {
    refresh()
    lanTargets()
      .then((ts) => {
        setTargets(ts)
        setIface((cur) => cur || ts[0] || '')
      })
      .catch(() => {})
  }, [refresh])

  // poll and tick the countdown while the server runs
  const running = !!state?.running
  useEffect(() => {
    if (!running) return
    const poll = setInterval(refresh, pollMs)
    timer.current = setInterval(() => setNow(Date.now()), 1000)
    return () => {
      clearInterval(poll)
      if (timer.current) clearInterval(timer.current)
    }
  }, [running, refresh])

  const start = async (ev: React.FormEvent) => {
    ev.preventDefault()
    setBusy(true)
    setError(null)
    try {
      setState(await startThroughput({ interface: iface, minutes }))
      setNow(Date.now())
    } catch (e) {
      setError(throughputError(e))
    } finally {
      setBusy(false)
    }
  }

  const stop = async () => {
    setBusy(true)
    setError(null)
    try {
      setState(await stopThroughput())
    } catch (e) {
      setError(throughputError(e))
    } finally {
      setBusy(false)
    }
  }

  const results = [...(state?.results ?? [])].sort((a, b) => Date.parse(b.time) - Date.parse(a.time))

  return (
    <div className="space-y-4">
      <p className="text-sm opacity-80">{t('throughput.intro')}</p>
      {running && state ? (
        <div className="space-y-3 rounded border border-blue-200 bg-blue-50 p-3 dark:border-blue-800 dark:bg-blue-950" role="status">
          <div className="flex flex-wrap items-center gap-3">
            <Loader2 size={16} className="animate-spin text-blue-600" />
            <span className="text-sm font-medium">
              {t('throughput.running', { interface: state.interface ?? '', address: state.address ?? '', time: countdown(state.expires, now) })}
            </span>
            <button type="button" onClick={stop} disabled={busy} className="ml-auto rounded bg-red-600 px-3 py-1.5 text-sm text-white hover:bg-red-700 disabled:opacity-50">
              {t('throughput.stop')}
            </button>
          </div>
          <p className="text-xs opacity-80">{t('throughput.runOn')}</p>
          <CopyLine label={t('throughput.upload')} text={`iperf3 -c ${state.address}${state.port && state.port !== 5201 ? ` -p ${state.port}` : ''}`} />
          <CopyLine label={t('throughput.download')} text={`iperf3 -c ${state.address}${state.port && state.port !== 5201 ? ` -p ${state.port}` : ''} -R`} />
        </div>
      ) : (
        <form onSubmit={start} className="flex flex-wrap items-end gap-3">
          <label className="block text-sm">
            {t('throughput.interface')}
            <select aria-label={t('throughput.interface')} className={inputClass} value={iface} onChange={(e) => setIface(e.target.value)} required>
              {targets.length === 0 && <option value="">{t('throughput.noTargets')}</option>}
              {targets.map((n) => <option key={n} value={n}>{n}</option>)}
            </select>
          </label>
          <label className="block text-sm">
            {t('throughput.minutes')}
            <input aria-label={t('throughput.minutes')} type="number" min={1} max={15} className={`${inputClass} w-20`} value={minutes} onChange={(e) => setMinutes(Number(e.target.value))} />
          </label>
          <button type="submit" disabled={busy || !iface} className="inline-flex items-center gap-2 rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700 disabled:opacity-50">
            {busy && <Loader2 size={14} className="animate-spin" />}
            {t('throughput.start')}
          </button>
        </form>
      )}
      {error && <div className="rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-950 dark:text-red-300" role="alert">{error}</div>}
      {state?.error && !error && <div className="rounded bg-amber-50 p-2 text-sm text-amber-800 dark:bg-amber-950 dark:text-amber-200" role="alert">{t('throughput.serverError', { error: state.error })}</div>}
      <div>
        <h3 className="mb-2 text-sm font-semibold">{t('throughput.results')}</h3>
        {results.length === 0 ? (
          <p className="text-sm opacity-70">{t('throughput.noResults')}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-sm">
              <thead className="text-xs uppercase opacity-70">
                <tr>
                  <th className="py-1 pr-3">{t('throughput.col.time')}</th>
                  <th className="py-1 pr-3">{t('throughput.col.client')}</th>
                  <th className="py-1 pr-3">{t('throughput.col.direction')}</th>
                  <th className="py-1 pr-3 text-right">{t('throughput.col.speed')}</th>
                  <th className="py-1 pr-3 text-right">{t('throughput.col.extra')}</th>
                </tr>
              </thead>
              <tbody>
                {results.map((r, i) => (
                  <tr key={`${r.time}-${i}`} className="border-t border-gray-100 dark:border-gray-800">
                    <td className="py-1 pr-3 whitespace-nowrap">{new Date(r.time).toLocaleTimeString()}</td>
                    <td className="py-1 pr-3 font-mono text-xs">{r.client}</td>
                    <td className="py-1 pr-3">
                      {r.direction === 'download' ? t('throughput.download') : t('throughput.upload')}
                      <span className="ml-1 text-xs opacity-60">{r.protocol.toUpperCase()}{r.streams > 1 ? ` ×${r.streams}` : ''}</span>
                    </td>
                    <td className="py-1 pr-3 text-right font-medium">{t('throughput.mbit', { value: mbit(r.bits_per_second) })}</td>
                    <td className="py-1 pr-3 text-right text-xs opacity-80">
                      {r.protocol === 'udp'
                        ? t('throughput.udpExtra', { lost: (r.lost_percent ?? 0).toFixed(1), jitter: (r.jitter_ms ?? 0).toFixed(2) })
                        : r.retransmits !== undefined ? t('throughput.retransmits', { count: r.retransmits }) : ''}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}

// hintText: the server's English carries the details (speeds, which
// offloads, which driver); other languages get the translated gist by code.
function hintText(h: NICHint): string {
  if (language() === 'en' && h.message) return h.message
  switch (h.code) {
    case 'no_link': return t('nics.hint.no_link')
    case 'slow_link': return t('nics.hint.slow_link')
    case 'half_duplex': return t('nics.hint.half_duplex')
    case 'offloads_off': return t('nics.hint.offloads_off')
    case 'slow_driver': return t('nics.hint.slow_driver')
    case 'single_queue': return t('nics.hint.single_queue')
  }
  return h.message
}

// the offloads worth showing, by their ethtool -k names
const shownOffloads: [string, string][] = [
  ['generic-receive-offload', 'GRO'],
  ['generic-segmentation-offload', 'GSO'],
  ['tcp-segmentation-offload', 'TSO'],
  ['large-receive-offload', 'LRO'],
  ['rx-checksumming', 'RX csum'],
  ['tx-checksumming', 'TX csum'],
]

function speed(n: NICInfo): string {
  if (!n.link) return t('nics.noLink')
  if (!n.speed_mbps) return t('nics.speedUnknown')
  const s = n.speed_mbps >= 1000 ? `${n.speed_mbps / 1000} Gbit/s` : `${n.speed_mbps} Mbit/s`
  return n.duplex ? `${s} ${n.duplex}` : s
}

export function NICsTab() {
  const [nics, setNics] = useState<NICInfo[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    getNICs()
      .then((r) => setNics(r.interfaces ?? []))
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
  }, [])

  if (error) return <div className="rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-950 dark:text-red-300" role="alert">{error}</div>
  if (!nics) return <Loader2 size={16} className="animate-spin" aria-label={t('nics.loading')} />
  if (nics.length === 0) return <p className="text-sm opacity-70">{t('nics.none')}</p>

  return (
    <div className="space-y-3">
      <p className="text-sm opacity-80">{t('nics.intro')}</p>
      {nics.map((n) => (
        <div key={n.name} className="space-y-2 rounded border border-gray-200 p-3 dark:border-gray-700">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <span className="font-semibold">{n.name}</span>
            <code className="text-xs opacity-70">{n.device}</code>
            <span className="text-xs uppercase opacity-60">{n.role}</span>
            {!n.enabled && <span className="text-xs opacity-60">{t('nics.disabled')}</span>}
          </div>
          <dl className="grid grid-cols-2 gap-x-4 gap-y-1 text-sm sm:grid-cols-4">
            <div><dt className="text-xs opacity-60">{t('nics.driver')}</dt><dd>{n.driver || '—'}</dd></div>
            <div><dt className="text-xs opacity-60">{t('nics.link')}</dt><dd>{speed(n)}</dd></div>
            <div><dt className="text-xs opacity-60">{t('nics.queues')}</dt><dd>{t('nics.queueCounts', { rx: n.rx_queues, tx: n.tx_queues })}</dd></div>
            <div><dt className="text-xs opacity-60">MTU</dt><dd>{n.mtu || '—'}</dd></div>
          </dl>
          {n.offloads && (
            <div className="flex flex-wrap gap-1 text-xs">
              {shownOffloads.filter(([k]) => k in n.offloads!).map(([k, label]) => {
                const f = n.offloads![k]
                return (
                  <span key={k} title={f.fixed ? t('nics.fixed') : undefined} className={`rounded px-1.5 py-0.5 ${f.on ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200' : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400'}`}>
                    {label} {f.on ? t('nics.on') : t('nics.off')}
                  </span>
                )
              })}
              {n.offload_overrides && Object.keys(n.offload_overrides).length > 0 && <span className="px-1.5 py-0.5 opacity-70">{t('nics.overridden')}</span>}
            </div>
          )}
          {(n.hints ?? []).length > 0 && (
            <ul className="list-disc space-y-0.5 pl-5 text-sm text-amber-800 dark:text-amber-300">
              {(n.hints ?? []).map((h) => <li key={h.code}>{hintText(h)}</li>)}
            </ul>
          )}
        </div>
      ))}
    </div>
  )
}
