// Port forward, DHCP and DNS API: types mirror internal/config (policy.go,
// services.go, schema.go) by JSON name; edits replace one config section in
// the candidate with PATCH.
import { apiClient } from './client'
import type { SaveResult } from './policy'

export interface PortForward {
  name: string
  in_intf: string // a wan interface or a VLAN on one
  ext_port: string // "443" or "8000-8100"
  protocol: 'tcp' | 'udp'
  to_addr: string // IPv4
  to_port: string // "" = same as ext_port
}

export interface DHCPStaticLease {
  mac: string
  ip: string
  hostname?: string
}

export interface DHCPServer {
  interface: string
  range_start: string
  range_end: string
  lease_seconds?: number // 0/absent = server default
  dns?: string[] // handed to clients; absent = the interface address
  gateway?: string // absent = the interface address
  static_leases?: DHCPStaticLease[]
}

export interface DNSSettings {
  forwarders?: string[]
  dnssec?: boolean
  local_records?: Record<string, string> // name -> IPv4
}

export interface UPnpAllow {
  address: string
  ports: string
}

export interface UPnPConfig {
  enabled: boolean
  interfaces?: string[]
  nat_pmp?: boolean
  secure_mode?: boolean
  allow?: UPnpAllow[]
  max_lease?: number
  stun?: boolean
}

export interface UPnpMapping {
  protocol: string
  external_port: number
  internal_ip: string
  internal_port: number
  description?: string
  expires?: string
}

export interface NetIf {
  name: string
  role?: string // interfaces only; a VLAN inherits its parent's
  parent?: string // VLANs only
  mode: string // static | dhcp | none
  address?: string // CIDR, e.g. 192.168.1.1/24
  enabled: boolean
}

export interface NetworkConfig {
  interfaces?: NetIf[]
  vlans?: NetIf[]
  nat?: { port_forwards?: PortForward[] }
  dhcp?: DHCPServer[]
  dns?: DNSSettings
  upnp?: UPnPConfig
}

export interface DHCPLease {
  ip: string
  mac: string
  hostname?: string
  expires?: string
}

export function getNetworkConfig(): Promise<NetworkConfig> {
  return apiClient<NetworkConfig>('/api/v1/config/candidate')
}

// patchSection replaces one section of the candidate, e.g. "dhcp" or
// "nat.port_forwards". The candidate is stored even when invalid; the result's
// validation_errors lists what a commit would reject.
export function patchSection(path: string, value: unknown): Promise<SaveResult> {
  return apiClient<SaveResult>('/api/v1/config/candidate', {
    method: 'PATCH',
    body: JSON.stringify({ path, value }),
  })
}

// getLeases returns the active DHCP leases, or null while the endpoint does
// not exist yet (404).
export async function getLeases(): Promise<DHCPLease[] | null> {
  const res = await fetch('/api/v1/dhcp/leases', { credentials: 'same-origin' })
  if (res.status === 404) return null
  if (!res.ok) throw new Error(`leases: ${res.status} ${res.statusText}`)
  const body = (await res.json()) as { leases?: DHCPLease[] } | DHCPLease[]
  return Array.isArray(body) ? body : body.leases ?? []
}

// wanInterfaces lists the interfaces a port forward may arrive on: enabled
// wan interfaces and VLANs whose parent is one (the server's rule).
export function wanInterfaces(cfg: NetworkConfig): string[] {
  const wan = new Set((cfg.interfaces ?? []).filter((i) => i.role === 'wan').map((i) => i.name))
  return [...wan, ...(cfg.vlans ?? []).filter((v) => v.parent && wan.has(v.parent)).map((v) => v.name)]
}

// staticInterfaces lists interfaces and VLANs that can run a DHCP server.
export function staticInterfaces(cfg: NetworkConfig): NetIf[] {
  return [...(cfg.interfaces ?? []), ...(cfg.vlans ?? [])].filter((i) => i.mode === 'static' && i.address)
}

// getUpnpMappings returns the active UPnP port mappings from miniupnpd.
export function getUpnpMappings(): Promise<{ enabled: boolean; mappings: UPnpMapping[] }> {
  return apiClient('/api/v1/upnp/mappings')
}
