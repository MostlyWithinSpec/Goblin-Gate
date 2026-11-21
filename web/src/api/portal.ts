// Captive portal: the `captive_portals` config section (saved whole with
// patchSection('captive_portals', list)), the let-in guests and the voucher
// batches. Types mirror internal/config/portal.go and
// internal/api/portal_handlers.go by JSON name.
import { apiClient } from './client'

export type PortalMode = 'click' | 'password' | 'voucher'

export interface CaptivePortal {
  interface: string
  enabled: boolean
  mode: PortalMode
  password?: string // a secret: "" for callers that may not see it (kept when left unchanged)
  session_minutes?: number // 0 = 1440; 5-43200
  idle_minutes?: number // 0 = never; 5-1440
  rate_limit_mbps?: number // 0 = none; per client, each direction
  title?: string // at most 80 characters
  text?: string // terms or welcome, plain text, at most 4000; guests must accept it
  logo?: string // data: URI of a PNG, JPEG, WebP or SVG, at most 128 KB decoded
}

export interface PortalClient {
  interface: string
  mac: string
  ip: string
  method: PortalMode
  voucher?: string
  batch?: string
  started: string
  expires: string
  seconds_left: number
}

export type CodeState = 'unused' | 'in_use' | 'used_up' | 'expired' | 'revoked'

export interface VoucherCode {
  code: string
  used: number
  first_used?: string
  revoked?: boolean
  state: CodeState
}

export interface VoucherBatch {
  id: string
  note?: string
  interface?: string // "" = any voucher portal
  minutes: number
  uses: number
  created: string
  expires?: string
  revoked?: boolean
  counts: Partial<Record<CodeState, number>>
  codes: VoucherCode[]
}

export interface NewBatch {
  count: number // 1-500
  minutes: number // 1-43200 per use
  uses?: number // 1-100, default 1
  valid_days?: number // 0 = no end
  note?: string
  interface?: string
}

export const portalPreviewURL = (iface: string) => `/api/v1/portal/preview/${encodeURIComponent(iface)}`

export function getPortalClients() {
  return apiClient<{ clients: PortalClient[] }>('/api/v1/portal/clients')
}

export function kickPortalClient(mac: string) {
  return apiClient<{ status: string }>(`/api/v1/portal/clients/${encodeURIComponent(mac)}`, { method: 'DELETE' })
}

export function getVoucherBatches() {
  return apiClient<{ batches: VoucherBatch[] }>('/api/v1/portal/vouchers')
}

export function createVoucherBatch(b: NewBatch) {
  return apiClient<VoucherBatch>('/api/v1/portal/vouchers', { method: 'POST', body: JSON.stringify(b) })
}

// revokeVouchers revokes a batch, or one code of it.
export function revokeVouchers(id: string, code?: string) {
  return apiClient<{ status: string }>(`/api/v1/portal/vouchers/${encodeURIComponent(id)}/revoke`, {
    method: 'POST',
    body: JSON.stringify(code ? { code } : {}),
  })
}

export function deleteVoucherBatch(id: string) {
  return apiClient<{ status: string }>(`/api/v1/portal/vouchers/${encodeURIComponent(id)}`, { method: 'DELETE' })
}
