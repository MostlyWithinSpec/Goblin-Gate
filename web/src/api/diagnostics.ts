// Diagnostics API (task 092): ping, traceroute and DNS lookup run on the box.
// Types mirror internal/diag by JSON name. All three are POSTs, so only
// admins can run them; at most two run at once (429 busy otherwise).
import { apiClient } from './client'

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
