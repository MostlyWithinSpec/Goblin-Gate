// Diagnostics API (task 092): ping, traceroute and DNS lookup run on the box.
// Types mirror internal/diag by JSON name. All three are POSTs, so only
// admins can run them; at most two run at once (429 busy otherwise).
import { apiClient } from './client'
import type { Offloads } from './objects'

export interface PingResult {
  target: string
  sent: number
  received: number
  loss_percent: number
  rtt_min_ms: number
  rtt_avg_ms: number
  rtt_max_ms: number
  lines: string[]
}

export interface Hop {
  hop: number
  address: string // "*" when nothing answered
  rtt_ms: number[]
}

export interface TraceResult {
  target: string
  hops: Hop[]
}

export type DNSType = 'A' | 'AAAA' | 'MX' | 'TXT' | 'CNAME' | 'NS' | 'PTR'

export interface DNSResult {
  name: string
  type: string
  server: string
  rcode: string // NOERROR, NXDOMAIN, SERVFAIL, ...
  ms: number
  answers: string[]
}

export function diagPing(body: { host: string; count?: number; interface?: string }): Promise<PingResult> {
  return apiClient<PingResult>('/api/v1/diag/ping', { method: 'POST', body: JSON.stringify(body) })
}

export function diagTraceroute(body: { host: string; max_hops?: number }): Promise<TraceResult> {
  return apiClient<TraceResult>('/api/v1/diag/traceroute', { method: 'POST', body: JSON.stringify(body) })
}

export function diagDNS(body: { name: string; type?: DNSType; server?: string }): Promise<DNSResult> {
  return apiClient<DNSResult>('/api/v1/diag/dns', { method: 'POST', body: JSON.stringify(body) })
}

// --- Throughput test and network cards --------------------------------------
// GET /api/v1/diag/throughput is open to every admin; POST and DELETE need the
// actions scope. The box runs an iperf3 server on one LAN-side interface for a
// few minutes; computers on that network run `iperf3 -c <address>`.

export interface ThroughputResult {
  time: string
  client: string
  protocol: 'tcp' | 'udp'
  direction: 'upload' | 'download' // upload = client → box, download = box → client (iperf3 -R)
  streams: number
  seconds: number
  bits_per_second: number
  retransmits?: number // TCP download only (the box is the sender)
  lost_percent?: number // UDP
  jitter_ms?: number // UDP
}

export interface ThroughputState {
  running: boolean
  interface?: string
  device?: string
  address?: string // what clients connect to
  port: number
  started?: string
  expires?: string
  results: ThroughputResult[] | null
  error?: string // the last failure of the server
}

export function getThroughput(): Promise<ThroughputState> {
  return apiClient<ThroughputState>('/api/v1/diag/throughput')
}

export function startThroughput(body: { interface: string; minutes?: number }): Promise<ThroughputState> {
  return apiClient<ThroughputState>('/api/v1/diag/throughput', { method: 'POST', body: JSON.stringify(body) })
}

export function stopThroughput(): Promise<ThroughputState> {
  return apiClient<ThroughputState>('/api/v1/diag/throughput', { method: 'DELETE' })
}

export interface NICHint {
  code: string // no_link | slow_link | half_duplex | offloads_off | slow_driver | single_queue
  message: string // English
}

export interface NICInfo {
  name: string
  role: string
  enabled: boolean
  offload_overrides?: Offloads
  device: string
  driver?: string
  link: boolean
  speed_mbps: number // 0 = unknown
  duplex?: string
  mtu?: number
  rx_queues: number
  tx_queues: number
  supported_mbps?: number[]
  offloads?: Record<string, { on: boolean; fixed?: boolean }> // ethtool -k names
  hints: NICHint[] | null
}

export function getNICs(): Promise<{ interfaces: NICInfo[] }> {
  return apiClient<{ interfaces: NICInfo[] }>('/api/v1/diag/nics')
}
