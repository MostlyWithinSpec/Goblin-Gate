// Multi-WAN gateway status and WireGuard client key endpoints. Types mirror
// internal/gateways/gateways.go and internal/api/gateways_handlers.go by
// JSON name; the config side lives in api/objects.ts (Gateway, GatewayGroup).
import { apiClient } from './client'

export type GatewayState = 'up' | 'down' | 'unknown'
export type TargetMode = 'gateways' | 'default' | 'blackhole' | 'none'

export interface GatewayStatus {
  name: string
  interface: string
  device?: string
  monitor_ip?: string
  state: GatewayState
  loss_percent: number
  latency_ms: number
  since?: string
  reason?: string
}

export interface TargetStatus {
  name: string
  group?: boolean
  active: string[]
  mode: TargetMode
}

export interface GatewayReport {
  enabled: boolean
  gateways: GatewayStatus[]
  targets: TargetStatus[]
  default: TargetStatus
}

export function getGatewayStatus(): Promise<GatewayReport> {
  return apiClient<GatewayReport>('/api/v1/gateways/status')
}

// WireGuard client keys live outside the config; register the generated
// public key with the provider, or paste the provider's private key here.
export function getWireGuardClientPublicKey(name: string): Promise<{ public_key: string }> {
  return apiClient<{ public_key: string }>(`/api/v1/vpn/wireguard-clients/${encodeURIComponent(name)}/public-key`)
}

export function setWireGuardClientPrivateKey(name: string, privateKey: string): Promise<{ public_key: string }> {
  return apiClient<{ public_key: string }>(`/api/v1/vpn/wireguard-clients/${encodeURIComponent(name)}/private-key`, {
    method: 'PUT',
    body: JSON.stringify({ private_key: privateKey }),
  })
}
