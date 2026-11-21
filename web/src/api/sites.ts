// Site-to-site VPN between Goblin Gates (task 155): the `vpn.sites` config
// section, pairing with invitation and reply codes, and the live status. The
// types mirror internal/config/sites.go and internal/api/sites_handlers.go by
// JSON name. The section is saved whole with patchSection('vpn.sites', value).
import { apiClient } from './client'

// Site is one remote gate. A site with `via` has no tunnel of its own: its
// networks are routed through the named site, and policies see its traffic on
// that site's zone.
export interface Site {
  name: string // device and zone name: 1-15 of a-z, 0-9, -, starting with a letter
  enabled: boolean
  public_key?: string
  endpoint?: string // host:port of the remote gate; "" = it dials this one
  listen_port?: number
  address?: string // the remote gate's tunnel address
  networks?: string[] // CIDRs
  keepalive?: number // seconds, 0 = 25 when this gate dials, off otherwise
  via?: string
  description?: string
}

export interface SitesSettings {
  enabled: boolean
  address?: string // this gate's tunnel address
  endpoint?: string // public DNS name or IP other gates connect to
  networks?: string[] // address objects (host/subnet) and interface/VLAN names this gate offers
  allow_access?: string[] // https | ssh (ping is always allowed)
  announce?: boolean // put the site networks into BGP/OSPF
  remotes?: Site[]
}

export interface SiteStatus {
  name: string
  enabled: boolean
  via?: string
  address: string
  endpoint?: string
  latest_handshake: number // unix seconds, 0 = never
  handshake_age: number // seconds, -1 = never
  rx_bytes: number
  tx_bytes: number
  reachable?: boolean // absent = not checked
  rtt_ms?: number
}

export interface SitesStatus {
  enabled: boolean
  address?: string
  endpoint?: string
  sites: SiteStatus[]
}

export interface SiteInvitation {
  id: string
  site: string
  port: number
  expires: string
  created: string
}

export interface NewInvitation {
  id: string
  code: string
  site: string
  port: number
  expires: string
  sites_enabled: boolean // sites were turned on in the candidate by this invitation
}

export interface SiteProblem {
  code: 'overlap' | 'no_endpoint' | 'name_taken' | 'address_taken' | string
  network?: string
  message: string
}

export interface SiteOffer {
  site: string // the name suggested for the other gate
  endpoint?: string
  address: string
  networks: string[]
  public_key: string
  expires?: string
  problems: SiteProblem[]
}

export function getSitesStatus(): Promise<SitesStatus> {
  return apiClient<SitesStatus>('/api/v1/vpn/sites/status')
}

export function getInvitations(): Promise<{ invitations: SiteInvitation[] }> {
  return apiClient<{ invitations: SiteInvitation[] }>('/api/v1/vpn/sites/invitations')
}

export function createInvitation(site: string, ttlMinutes?: number): Promise<NewInvitation> {
  return apiClient<NewInvitation>('/api/v1/vpn/sites/invitations', {
    method: 'POST',
    body: JSON.stringify({ site, ttl_minutes: ttlMinutes }),
  })
}

export function cancelInvitation(id: string): Promise<{ status: string }> {
  return apiClient<{ status: string }>(`/api/v1/vpn/sites/invitations/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

export function checkJoin(code: string, site?: string): Promise<SiteOffer> {
  return apiClient<SiteOffer>('/api/v1/vpn/sites/join/check', { method: 'POST', body: JSON.stringify({ code, site: site || undefined }) })
}

export function joinSite(code: string, site: string): Promise<{ site: Site; reply: string }> {
  return apiClient<{ site: Site; reply: string }>('/api/v1/vpn/sites/join', { method: 'POST', body: JSON.stringify({ code, site }) })
}

export function acceptReply(code: string): Promise<{ site: Site }> {
  return apiClient<{ site: Site }>('/api/v1/vpn/sites/accept', { method: 'POST', body: JSON.stringify({ code }) })
}
