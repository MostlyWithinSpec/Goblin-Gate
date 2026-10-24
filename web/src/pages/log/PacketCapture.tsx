// Log & Report → Packet capture (task 077): start a capture on an interface,
// watch packets live over SSE, stop it, download the pcap.
import React, { useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Download, Square, Play, AlertTriangle } from 'lucide-react'
import { getCandidate } from '../../api/objects'
import {
  startCapture,
  getCapture,
  stopCapture,
  capturePcapUrl,
  type CaptureStatus,
  type CapturePacket,
  type StartCaptureRequest,
} from '../../api/capture'

const MAX_ROWS = 500

const inputClass = 'mt-1 block w-full rounded border border-gray-300 px-2 py-1 text-sm dark:bg-gray-700 dark:border-gray-600'
const labelClass = 'text-sm font-medium text-gray-700 dark:text-gray-300'

export default function PacketCapture() {
  const [iface, setIface] = useState('')
  const [host, setHost] = useState('')
  const [port, setPort] = useState('')
  const [protocol, setProtocol] = useState<'' | 'tcp' | 'udp' | 'icmp' | 'icmp6' | 'arp'>('')
  const [maxPackets, setMaxPackets] = useState('1000')
  const [maxSeconds, setMaxSeconds] = useState('60')

  const [captureId, setCaptureId] = useState<string | null>(null)
  const [rows, setRows] = useState<CapturePacket[]>([])
  const [dropped, setDropped] = useState(0)
  const [startError, setStartError] = useState<string | null>(null)
  const [busyCaptureId, setBusyCaptureId] = useState<string | null>(null)

  // Live status while a capture is running (packets/bytes counters).
  const statusQ = useQuery({
    queryKey: ['capture', captureId],
    queryFn: () => getCapture(captureId!),
    enabled: captureId !== null,
    retry: false,
    refetchInterval: 2000,
  })

  // SSE stream of packets while running.
  useEffect(() => {
    if (!captureId) return
    const es = new EventSource(`/api/v1/capture/${captureId}/live`)
    es.addEventListener('packet', (e) => {
      const p = JSON.parse((e as MessageEvent).data) as CapturePacket
      setRows((prev) => [p, ...prev].slice(0, MAX_ROWS))
    })
    es.addEventListener('dropped', (e) => {
      const d = JSON.parse((e as MessageEvent).data) as { count: number }
      setDropped((prev) => prev + d.count)
    })
    es.addEventListener('done', () => {
      statusQ.refetch()
    })
    return () => es.close()
  }, [captureId])

  const candidate = useQuery({ queryKey: ['candidate'], queryFn: getCandidate, staleTime: 60_000 })
  const interfaces = [
    ...(candidate.data?.interfaces ?? []).filter((i) => i.enabled).map((i) => i.name),
    ...(candidate.data?.vlans ?? []).filter((v) => v.enabled).map((v) => v.name),
  ]

  const state: CaptureStatus['state'] | null = statusQ.data?.state ?? (captureId ? 'running' : null)

  const start = async () => {
    setStartError(null)
    if (!iface) {
      setStartError('Pick an interface.')
      return
    }
    const req: StartCaptureRequest = { interface: iface }
    if (host.trim()) req.host = host.trim()
    if (port.trim()) req.port = Number(port)
    if (protocol) req.protocol = protocol
    if (maxPackets) req.max_packets = Number(maxPackets)
    if (maxSeconds) req.max_seconds = Number(maxSeconds)
    try {
      const status = await startCapture(req)
      setRows([])
      setDropped(0)
      setCaptureId(status.id)
    } catch (e) {
      const err = e as { status?: number; code?: string; message?: string; details?: unknown }
      if (err.status === 409 || err.code === 'busy') {
        // The API has no list endpoint, so we only know the busy capture's id
        // if the server tells us in the error body.
        const d = err.details as { id?: string } | undefined
        setBusyCaptureId(d?.id ?? null)
        return
      }
      setStartError(err.message ?? String(e))
    }
  }

  const stop = async () => {
    if (!captureId) return
    try {
      await stopCapture(captureId)
      statusQ.refetch()
    } catch (e) {
      setStartError((e as Error).message)
    }
  }

  // 503 capture_unavailable: dev mode or no manager.
  if (statusQ.isError && (statusQ.error as { code?: string })?.code === 'capture_unavailable') {
    return (
      <div className="rounded-lg border border-gray-200 bg-white p-6 text-center dark:border-gray-700 dark:bg-gray-800">
        <p className="text-sm text-gray-500">Packet capture runs on the firewall itself.</p>
      </div>
    )
  }

  return (
    <div>
      <h2 className="mb-4 text-xl font-semibold text-gray-800 dark:text-white">Packet capture</h2>

      {/* Start form */}
      {!captureId && (
        <form
          className="mb-6 grid max-w-3xl grid-cols-1 gap-4 sm:grid-cols-2 md:grid-cols-3"
          onSubmit={(e) => {
            e.preventDefault()
            void start()
          }}
        >
          <label className="block">
            <span className={labelClass}>Interface</span>
            <select value={iface} onChange={(e) => setIface(e.target.value)} className={inputClass}>
              <option value="">Select…</option>
              {interfaces.map((n) => (
                <option key={n} value={n}>{n}</option>
              ))}
            </select>
          </label>
          <label className="block">
            <span className={labelClass}>Host (IP)</span>
            <input type="text" placeholder="e.g. 192.168.1.50" value={host} onChange={(e) => setHost(e.target.value)} className={inputClass} />
          </label>
          <label className="block">
            <span className={labelClass}>Port</span>
            <input type="number" min={1} max={65535} placeholder="any" value={port} onChange={(e) => setPort(e.target.value)} className={inputClass} />
          </label>
          <label className="block">
            <span className={labelClass}>Protocol</span>
            <select value={protocol} onChange={(e) => setProtocol(e.target.value as typeof protocol)} className={inputClass}>
              <option value="">any</option>
              <option value="tcp">tcp</option>
              <option value="udp">udp</option>
              <option value="icmp">icmp</option>
              <option value="icmp6">icmp6</option>
              <option value="arp">arp</option>
            </select>
          </label>
          <label className="block">
            <span className={labelClass}>Max packets</span>
            <input type="number" min={1} max={10000} value={maxPackets} onChange={(e) => setMaxPackets(e.target.value)} className={inputClass} />
          </label>
          <label className="block">
            <span className={labelClass}>Max seconds</span>
            <input type="number" min={1} max={300} value={maxSeconds} onChange={(e) => setMaxSeconds(e.target.value)} className={inputClass} />
          </label>

          {startError && (
            <p role="alert" className="col-span-full text-sm text-red-600">{startError}</p>
          )}
          {busyCaptureId !== null && (
            <div role="alert" className="col-span-full flex items-center gap-2 rounded bg-yellow-50 p-2 text-sm text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-200">
              <AlertTriangle size={16} />
              A capture is already running.{' '}
              {busyCaptureId && (
                <button type="button" className="underline" onClick={() => setCaptureId(busyCaptureId)}>View it</button>
              )}
            </div>
          )}

          <div className="col-span-full flex items-end">
            <button type="submit" className="flex items-center gap-1 rounded bg-blue-600 px-4 py-1.5 text-sm text-white hover:bg-blue-700">
              <Play size={16} /> Start capture
            </button>
          </div>
        </form>
      )}

      {/* Running / done */}
      {captureId && (
        <div>
          <div className="mb-3 flex flex-wrap items-center gap-4 text-sm">
            <span data-testid="capture-state" className="font-medium">
              {state === 'running' ? 'Capturing…' : state === 'done' ? 'Done' : state === 'failed' ? 'Failed' : '…'}
            </span>
            {statusQ.data && (
              <>
                <span>{statusQ.data.packets} packets</span>
                <span>{statusQ.data.bytes} bytes</span>
                <span className="text-gray-500">filter: <code>{statusQ.data.filter}</code></span>
              </>
            )}
            {dropped > 0 && (
              <span className="text-red-600">{dropped} dropped</span>
            )}
            {state === 'running' && (
              <button type="button" onClick={() => void stop()} className="flex items-center gap-1 rounded bg-red-600 px-3 py-1 text-sm text-white hover:bg-red-700">
                <Square size={14} /> Stop
              </button>
            )}
            {state !== 'running' && (
              <button
                type="button"
                onClick={() => {
                  setCaptureId(null)
                  setRows([])
                  setDropped(0)
                  setBusyCaptureId(null)
                  setStartError(null)
                }}
                className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-100 dark:border-gray-600 dark:hover:bg-gray-700"
              >
                New capture
              </button>
            )}
            {state !== 'running' && (
              <button
                type="button"
                onClick={() => {
                  setCaptureId(null)
                  setRows([])
                  setDropped(0)
                  setBusyCaptureId(null)
                  setStartError(null)
                }}
                className="rounded border border-gray-300 px-3 py-1 text-sm hover:bg-gray-100 dark:border-gray-600 dark:hover:bg-gray-700"
              >
                New capture
              </button>
            )}
            {state === 'done' && (
              <a href={capturePcapUrl(captureId)} className="flex items-center gap-1 rounded bg-blue-600 px-3 py-1 text-sm text-white hover:bg-blue-700">
                <Download size={14} /> Download pcap
              </a>
            )}
          </div>

          {statusQ.data?.state === 'failed' && statusQ.data.error && (
            <p role="alert" className="mb-3 text-sm text-red-600">{statusQ.data.error}</p>
          )}
          {state === 'done' && (
            <p className="mb-3 text-xs text-gray-500">Open the pcap in Wireshark to inspect it.</p>
          )}

          {/* Live table */}
          <div className="overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-700">
            <table className="min-w-full divide-y divide-gray-200 bg-white text-sm dark:divide-gray-700 dark:bg-gray-800 dark:text-gray-200">
              <thead className="bg-gray-50 dark:bg-gray-700">
                <tr>
                  {['Time', 'Source', 'Destination', 'Protocol', 'Length', 'Info'].map((h) => (
                    <th key={h} className="px-3 py-2 text-left text-xs font-medium uppercase text-gray-500">{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-100 dark:divide-gray-700/50">
                {rows.map((p) => (
                  <tr key={p.n}>
                    <td className="px-3 py-1 font-mono text-xs">{new Date(p.time).toLocaleTimeString()}</td>
                    <td className="px-3 py-1 font-mono text-xs">{p.src}</td>
                    <td className="px-3 py-1 font-mono text-xs">{p.dst}</td>
                    <td className="px-3 py-1">{p.protocol}</td>
                    <td className="px-3 py-1">{p.length}</td>
                    <td className="px-3 py-1 text-gray-500">{p.info}</td>
                  </tr>
                ))}
                {rows.length === 0 && (
                  <tr><td colSpan={6} className="px-3 py-6 text-center text-gray-500">Waiting for packets…</td></tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}
