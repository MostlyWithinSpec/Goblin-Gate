// RADIUS: the `radius` config section (saved whole with
// patchSection('radius', value)), the authentication log, the sessions,
// device VLANs, per-device Wi-Fi passwords and EAP-TLS certificates. Types
// mirror internal/config/radius.go, internal/radius and
// internal/api/radius_handlers.go by JSON name.
import { apiClient } from './client'
import type { DeviceInventory } from './devices'

export type UnknownChoice = '' | 'reject' | 'quarantine' | 'accept'

export interface RadiusClient {
  name: string
  address: string // IP or CIDR
  secret?: string // a secret: "" = unchanged (kept by name)
  coa_port?: number // 0 = 3799
  description?: string
}

export interface RadiusGroup {
  name: string
  vlan?: string // VLAN name, "" = the port's own
}

export interface RadiusUser {
  name: string
  enabled: boolean
  password?: string // a secret: "" = unchanged (kept by name)
  group?: string
}

export interface RadiusConfig {
  enabled: boolean
  clients?: RadiusClient[]
  mab?: boolean
  dot1x?: boolean
  unknown?: UnknownChoice // "" = reject
  quarantine_vlan?: string
  groups?: RadiusGroup[]
  users?: RadiusUser[]
}

export type AuthMethod = 'mab' | 'peap' | 'eap-tls' | 'disconnect'
export type AuthResult = 'accept' | 'quarantine' | 'reject' | 'acked' | 'refused' | 'no_answer'

export interface AuthEvent {
  time: string
  client?: string
  nas?: string
  port?: string // NAS-Port-Id or NAS-Port
  mac?: string
  user?: string
  method: AuthMethod
  result: AuthResult
  vlan?: string
  vlan_id?: number
  reason?: string
}

export interface RadiusSession {
  mac: string
  user?: string
  method: AuthMethod
  client?: string
  nas: string
  nas_identifier?: string
  port?: string
  ssid?: string
  vlan?: string
  ip?: string
  session_id?: string
  started: string
  updated: string
}

export interface DisconnectOutcome {
  mac: string
  client: string
  nas: string
  result: 'acked' | 'refused' | 'no_answer'
  error_cause?: number
}

export interface DeviceVLANResult {
  device: DeviceInventory & { vlan?: string }
  disconnect?: DisconnectOutcome
  disconnect_error?: string
}

export interface PPSKEntry {
  mac: string
  psk: string
  set: string
}

export interface IssuedCert {
  user: string
  serial: string
  issued: string
  not_after: string
  revoked?: string
}

export interface ClientCert {
  user: string
  serial: string
  not_after: string
  certificate: string
  key: string
  ca: string
  ca_sha256: string
}

const enc = encodeURIComponent

export function getRadiusLog(limit = 200) {
  return apiClient<{ events: AuthEvent[] }>(`/api/v1/radius/log?limit=${limit}`)
}

export function getRadiusSessions() {
  return apiClient<{ sessions: RadiusSession[] }>('/api/v1/radius/sessions')
}

export function disconnectSession(mac: string) {
  return apiClient<DisconnectOutcome>(`/api/v1/radius/sessions/${enc(mac)}/disconnect`, { method: 'POST' })
}

// setDeviceVLAN moves a device ("" = none of its own); a connected device is
// disconnected so it rejoins in the new VLAN.
export function setDeviceVLAN(mac: string, vlan: string) {
  return apiClient<DeviceVLANResult>(`/api/v1/devices/${enc(mac)}/vlan`, { method: 'PUT', body: JSON.stringify({ vlan }) })
}

export function getPPSKs() {
  return apiClient<{ devices: PPSKEntry[] }>('/api/v1/radius/ppsk')
}

export function setPPSK(mac: string, psk: string) {
  return apiClient<{ status: string }>(`/api/v1/radius/ppsk/${enc(mac)}`, { method: 'PUT', body: JSON.stringify({ psk }) })
}

export function deletePPSK(mac: string) {
  return apiClient<{ status: string }>(`/api/v1/radius/ppsk/${enc(mac)}`, { method: 'DELETE' })
}

export function getCertificates(user?: string) {
  return apiClient<{ certificates: IssuedCert[] }>(`/api/v1/radius/certificates${user ? `?user=${enc(user)}` : ''}`)
}

export function issueCertificate(user: string, days: number) {
  return apiClient<ClientCert>(`/api/v1/radius/users/${enc(user)}/certificate`, { method: 'POST', body: JSON.stringify({ days }) })
}

// revokeCertificates revokes one certificate, or every one of the user's.
export function revokeCertificates(user: string, serial?: string) {
  return apiClient<{ revoked: number }>(`/api/v1/radius/users/${enc(user)}/certificates/revoke`, {
    method: 'POST',
    body: JSON.stringify(serial ? { serial } : {}),
  })
}
