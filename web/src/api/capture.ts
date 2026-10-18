// Packet capture API — live state, not candidate config (task 076).
import { apiClient } from './client'

export interface CaptureStatus {
  id: string
  state: 'running' | 'done' | 'failed'
  interface: string
  filter: string
  packets: number
  bytes: number
  started: string
  finished?: string
  error?: string
}

export interface CapturePacket {
  n: number
  time: string
  src: string
  dst: string
  protocol: string
  length: number
  info: string
}

export interface StartCaptureRequest {
  interface: string
  host?: string
  port?: number
  protocol?: 'tcp' | 'udp' | 'icmp' | 'icmp6' | 'arp'
  max_packets?: number
  max_seconds?: number
  snaplen?: number
}

export async function startCapture(req: StartCaptureRequest): Promise<CaptureStatus> {
  return apiClient<CaptureStatus>('/api/v1/capture', {
    method: 'POST',
    body: JSON.stringify(req),
  })
}

export async function getCapture(id: string): Promise<CaptureStatus> {
  return apiClient<CaptureStatus>(`/api/v1/capture/${id}`)
}

export async function stopCapture(id: string): Promise<void> {
  await apiClient(`/api/v1/capture/${id}`, { method: 'DELETE' })
}

export function capturePcapUrl(id: string): string {
  return `/api/v1/capture/${id}/pcap`
}
