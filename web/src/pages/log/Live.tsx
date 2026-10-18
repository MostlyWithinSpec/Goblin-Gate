// Log & Report → Live (task 079): top devices by bandwidth, refreshed every
// 3 s with a small sparkline of the last 60 samples per device; clicking a
// device shows its live flows.
import React, { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { listFlows, listFlowDevices, formatRate, type Flow, type FlowDevice } from '../../api/flows'

const REFRESH = 3_000
const SPARK_SAMPLES = 60

// Sparkline is a small inline SVG over the last samples of one device.
function Sparkline({ values }: { values: number[] }) {
  const w = 96
  const h = 24
  if (values.length < 2) return <svg width={w} height={h} data-testid="sparkline" />
  const max = Math.max(...values, 1)
  const step = w / (SPARK_SAMPLES - 1)
  const pts = values.map((v, i) => `${(i + (SPARK_SAMPLES - values.length)) * step},${h - 2 - (v / max) * (h - 4)}`).join(' ')
  return (
    <svg width={w} height={h} data-testid="sparkline" aria-hidden>
      <polyline points={pts} fill="none" stroke="#3b82f6" strokeWidth={1.5} />
    </svg>
  )
}

const deviceLabel = (d: FlowDevice) => d.name || d.ip

// FlowsPanel shows the live flows of one device, filtered by a text box.
function FlowsPanel({ ip, onClose }: { ip: string; onClose: () => void }) {
  const [filter, setFilter] = useState('')
  const q = useQuery({ queryKey: ['flows', ip], queryFn: () => listFlows(ip), refetchInterval: REFRESH, retry: false })
  const flows = (q.data ?? []).filter((f) => {
    if (!filter.trim()) return true
    const needle = filter.trim().toLowerCase()
    return [f.protocol, f.src, f.dst, String(f.src_port ?? ''), String(f.dst_port ?? ''), f.state ?? ''].some((s) => s.toLowerCase().includes(needle))
  })
  return (
    <div className="mt-6">
      <div className="mb-2 flex flex-wrap items-center gap-3">
        <h3 className="text-base font-semibold text-gray-800 dark:text-white">Flows for {ip}</h3>
        <input
          type="search"
          aria-label="Filter flows"
          placeholder="Filter (protocol, address, port, state)"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          className="rounded border border-gray-300 px-2 py-1 text-sm dark:bg-gray-700 dark:border-gray-600"
        />
        <button type="button" onClick={onClose} className="ml-auto rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-100 dark:border-gray-600 dark:hover:bg-gray-700">
          Close
        </button>
      </div>
      {q.isError && (
        <p role="alert" className="mb-2 text-sm text-red-600">{(q.error as Error).message}</p>
      )}
      <div className="overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-700">
        <table className="min-w-full divide-y divide-gray-200 bg-white text-sm dark:divide-gray-700 dark:bg-gray-800 dark:text-gray-200">
          <thead className="bg-gray-50 dark:bg-gray-700">
            <tr>
              {['Protocol', 'Remote address', 'State', 'Download', 'Upload', 'Total bytes', 'Age'].map((h) => (
                <th key={h} className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{h}</th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100 dark:divide-gray-700/50">
            {flows.map((f, i) => (
              <FlowRow key={i} flow={f} ip={ip} />
            ))}
            {!q.isLoading && flows.length === 0 && (
              <tr><td colSpan={7} className="px-3 py-6 text-center text-gray-500">No matching flows.</td></tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

// FlowRow renders one flow; the remote side is whichever end is not the device.
function FlowRow({ flow, ip }: { flow: Flow; ip: string }) {
  const remoteFirst = flow.src === ip
  const remote = remoteFirst ? flow.dst : flow.src
  const remotePort = (remoteFirst ? flow.dst_port : flow.src_port) ?? ''
  const down = remoteFirst ? flow.rate_out_bps : flow.rate_in_bps
  const up = remoteFirst ? flow.rate_in_bps : flow.rate_out_bps
  return (
    <tr>
      <td className="px-3 py-1">{flow.protocol}</td>
      <td className="px-3 py-1 font-mono text-xs">{remote}:{remotePort}</td>
      <td className="px-3 py-1">{flow.state || '—'}</td>
      <td className="px-3 py-1" data-testid={`flow-down-${remote}`}>{formatRate(down)}</td>
      <td className="px-3 py-1">{formatRate(up)}</td>
      <td className="px-3 py-1">{(flow.bytes_out + flow.bytes_in).toLocaleString()}</td>
      <td className="px-3 py-1">{flow.age_seconds !== undefined ? `${flow.age_seconds}s` : '—'}</td>
    </tr>
  )
}

export default function Live() {
  const [selected, setSelected] = useState<string | null>(null)
  // Sparkline history: device ip -> last N total-bit/s samples.
  const [history, setHistory] = useState<Record<string, number[]>>({})

  const q = useQuery({ queryKey: ['flows', 'devices'], queryFn: listFlowDevices, refetchInterval: REFRESH, retry: false })

  useEffect(() => {
    if (!q.data) return
    setHistory((prev) => {
      const next: Record<string, number[]> = {}
      for (const d of q.data!) {
        const total = d.rx_bps + d.tx_bps
        next[d.ip] = [...(prev[d.ip] ?? []), total].slice(-SPARK_SAMPLES)
      }
      return next
    })
  }, [q.data])

  const devices = useMemo(() => q.data ?? [], [q.data])

  // 503 flows_unavailable: dev mode or no sampler.
  if (q.isError && (q.error as { code?: string })?.code === 'flows_unavailable') {
    return (
      <div className="rounded-lg border border-gray-200 bg-white p-6 text-center dark:border-gray-700 dark:bg-gray-800">
        <p className="text-sm text-gray-500">Live flows are read on the firewall itself.</p>
      </div>
    )
  }

  return (
    <div>
      <h2 className="mb-4 text-xl font-semibold text-gray-800 dark:text-white">Live</h2>
      {q.isError && (q.error as { code?: string })?.code !== 'flows_unavailable' && (
        <p role="alert" className="mb-3 text-sm text-red-600">{(q.error as Error).message}</p>
      )}
      <div className="overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-700">
        <table className="min-w-full divide-y divide-gray-200 bg-white text-sm dark:divide-gray-700 dark:bg-gray-800 dark:text-gray-200">
          <thead className="bg-gray-50 dark:bg-gray-700">
            <tr>
              {['Device', 'Download', 'Upload', 'Flows', 'Last 3 min'].map((h) => (
                <th key={h} className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{h}</th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y divide-gray-100 dark:divide-gray-700/50">
            {devices.map((d) => (
              <tr
                key={d.ip}
                data-testid={`device-${d.ip}`}
                className="cursor-pointer hover:bg-gray-50 dark:hover:bg-gray-700"
                onClick={() => setSelected(d.ip)}
              >
                <td className="px-3 py-1">
                  <span className="font-medium">{deviceLabel(d)}</span>
                  <span className="ml-2 font-mono text-xs text-gray-500">{d.ip}</span>
                </td>
                <td className="px-3 py-1" data-testid={`down-${d.ip}`}>{formatRate(d.rx_bps)}</td>
                <td className="px-3 py-1">{formatRate(d.tx_bps)}</td>
                <td className="px-3 py-1">{d.flows}</td>
                <td className="px-3 py-1"><Sparkline values={history[d.ip] ?? []} /></td>
              </tr>
            ))}
            {!q.isLoading && devices.length === 0 && (
              <tr><td colSpan={5} className="px-3 py-6 text-center text-gray-500">No active flows yet.</td></tr>
            )}
          </tbody>
        </table>
      </div>

      {selected && <FlowsPanel ip={selected} onClose={() => setSelected(null)} />}
    </div>
  )
}
