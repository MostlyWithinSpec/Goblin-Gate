// Live flows and per-device bandwidth (internal/api/flows_handlers.go, task 078).
// Rates are bits per second. "Out" is the side that started the connection;
// for a device rx = download to the device, tx = upload from it.
import { apiClient } from './client'

export interface FlowDeviceRef {
  mac?: string
  name?: string
}

export interface Flow {
  protocol: string
  src: string
  src_port?: number
  dst: string
  dst_port?: number
  state?: string // TCP only
  bytes_out: number
  bytes_in: number
  packets: number
  rate_out_bps: number
  rate_in_bps: number
  age_seconds?: number
  src_device?: FlowDeviceRef
}

export interface FlowDevice {
  ip: string
  mac?: string
  name?: string
  rx_bps: number // to the device (download)
  tx_bps: number // from the device (upload)
  flows: number
}

export async function listFlows(ip?: string, limit?: number): Promise<Flow[]> {
  const p = new URLSearchParams()
  if (ip) p.set('ip', ip)
  if (limit !== undefined) p.set('limit', String(limit))
  const qs = p.toString()
  const res = await apiClient<{ flows: Flow[]; total: number }>(`/api/v1/flows${qs ? `?${qs}` : ''}`)
  return res.flows
}

export async function listFlowDevices(): Promise<FlowDevice[]> {
  const res = await apiClient<{ devices: FlowDevice[] }>('/api/v1/flows/devices')
  return res.devices
}

// formatRate renders a bit/s value as kbit/s or Mbit/s.
export function formatRate(bps: number): string {
  if (bps >= 1_000_000) return `${(bps / 1_000_000).toFixed(2)} Mbit/s`
  if (bps >= 1_000) return `${Math.round(bps / 1_000)} kbit/s`
  return `${Math.round(bps)} bit/s`
}
