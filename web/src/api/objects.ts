// Candidate config API for the object and security profile pages. Types
// mirror internal/config (schema.go, objects.go, profiles.go) by JSON name;
// every page edits one list of the candidate and saves the whole list with
// PATCH {path, value} (see patchSection), then the CommitBar commits it.
import { apiClient } from './client'
import type { Policy, SaveResult } from './policy'

export interface Interface {
  name: string
  device: string
  role: string // wan | lan | dmz | guest | mgmt
  mode: string // static | dhcp | none
  address?: string // CIDR, static only
  gateway?: string
  mtu?: number
  enabled: boolean
  description?: string
  allow_access?: string[] // ping | https | ssh
}

export interface VLAN {
  name: string
  parent: string
  vlan_id: number
  mode: string
  address?: string
  gateway?: string
  mtu?: number
  enabled: boolean
  description?: string
  allow_access?: string[]
}

export interface Address {
  name: string
  type: 'subnet' | 'host' | 'range' | 'fqdn'
  value: string
  comment?: string
}

export interface Group {
  name: string
  members?: string[]
}

export interface Service {
  name: string
  protocol: 'tcp' | 'udp' | 'icmp' | 'any'
  ports?: string
  comment?: string
}

export interface Feed {
  name: string
  type: 'ip' | 'domain' | 'url'
  url: string
  format?: string // plain (default) | csv:<column> | json:<path>
  refresh_minutes?: number // 0 = 60; at least 15
  enabled: boolean
  block_wan?: boolean
  auth_header?: string
}

export type Action3 = 'allow' | 'monitor' | 'block'
export type WebAction = 'allow' | 'monitor' | 'warn' | 'block'

export interface DNSFilterProfile {
  name: string
  comment?: string
  block_feeds?: string[]
  block_domains?: string[]
  allow_domains?: string[]
  safe_search?: boolean
  action?: 'nxdomain' | 'blockpage'
}

export interface WebFilterProfile {
  name: string
  comment?: string
  categories?: Record<string, WebAction>
  block_domains?: string[]
  allow_domains?: string[]
}

export interface AppCtrlProfile {
  name: string
  comment?: string
  groups?: Record<string, Action3>
  apps?: Record<string, Action3>
}

export interface IPSProfile {
  name: string
  comment?: string
  mode?: 'prevent' | 'detect'
  block_severity?: number
  categories?: string[]
  suppress?: { sid: number; track?: string; ip?: string }[]
}

export interface SSHKey {
  name: string
  key: string
}

// Notifications (task 053/054): channels and per-event routing. Types mirror
// internal/config/notify.go by JSON name.
export type NotifyType = 'email' | 'webhook' | 'discord' | 'ntfy' | 'telegram'

export interface NotifyChannel {
  name: string
  type: NotifyType
  enabled: boolean
  url?: string // webhook / discord / ntfy
  token?: string // webhook (Bearer) / ntfy, optional
  bot_token?: string // telegram
  chat_id?: string // telegram
  smtp_host?: string // email
  smtp_port?: number // email; 0 = 587 STARTTLS, 465 = TLS
  username?: string // email
  password?: string // email
  from?: string // email
  to?: string[] // email recipients
}

export interface NotifyRule {
  event: string
  channels: string[]
}

// The events a rule can name (config.NotifyEvents).
export const NOTIFY_EVENTS = ['commit', 'login_failed', 'ips_block', 'feed_error', 'wan_down', 'wan_up', 'update_available', 'test'] as const

export interface SystemSettings {
  hostname: string
  domain?: string
  timezone: string
  admin_port?: number
  ntp_servers?: string[]
  dns_servers?: string[]
  ssh_keys?: SSHKey[]
  update_channel?: 'stable' | 'beta' | 'nightly'
  update_url?: string
}

export interface Candidate {
  system?: SystemSettings
  interfaces?: Interface[]
  vlans?: VLAN[]
  addresses?: Address[]
  address_groups?: Group[]
  services?: Service[]
  service_groups?: Group[]
  policies?: Policy[]
  profiles?: {
    ips?: IPSProfile[]
    webfilter?: WebFilterProfile[]
    dnsfilter?: DNSFilterProfile[]
    appctrl?: AppCtrlProfile[]
  }
  feeds?: Feed[]
  notifications?: { channels?: NotifyChannel[]; rules?: NotifyRule[] }
}

export function getCandidate(): Promise<Candidate> {
  return apiClient<Candidate>('/api/v1/config/candidate')
}

// patchSection replaces one list of the candidate, e.g. "addresses" or
// "profiles.webfilter". The candidate is stored even when invalid; the
// result's validation_errors lists what a commit would reject.
export function patchSection(path: string, value: unknown): Promise<SaveResult> {
  return apiClient<SaveResult>('/api/v1/config/candidate', {
    method: 'PATCH',
    body: JSON.stringify({ path, value }),
  })
}

// testNotification sends a "test" event to one channel (POST /notifications/test).
// It resolves with the status string on success and throws an Error whose
// message is the server's reason (unknown_channel, delivery_failed, ...).
export function testNotification(channel: string): Promise<{ status: string }> {
  return apiClient<{ status: string }>('/api/v1/notifications/test', {
    method: 'POST',
    body: JSON.stringify({ channel }),
  })
}

// Live state next to the config ----------------------------------------------

export interface FeedStatus {
  name: string
  type: string
  url: string
  enabled: boolean
  count: number
  fetched_at?: string
  checked_at?: string
  next_refresh?: string
  error?: string
}

export const getFeedStatus = () => apiClient<{ feeds: FeedStatus[] }>('/api/v1/feeds').then((r) => r.feeds ?? [])
export const refreshFeed = (name: string) =>
  apiClient<FeedStatus>(`/api/v1/feeds/${encodeURIComponent(name)}/refresh`, { method: 'POST' })
export const lookupFeeds = (q: string) =>
  apiClient<{ query: string; feeds: string[] }>(`/api/v1/feeds/lookup?q=${encodeURIComponent(q)}`)

export interface Device {
  name: string
  mac?: string
  up: boolean
}
export const getDevices = () =>
  apiClient<{ devices: Device[] }>('/api/v1/system/devices').then((r) => r.devices ?? [])

export const getWebCategories = () =>
  apiClient<{ categories: string[]; entries: number }>('/api/v1/webfilter/categories')

export interface WebDecision {
  action: WebAction
  category?: string
  match?: string
}
export interface WebLookup {
  domain: string
  categories: string[]
  profiles: Record<string, WebDecision>
}
export const lookupWebCategory = (q: string) =>
  apiClient<WebLookup>(`/api/v1/webfilter/lookup?q=${encodeURIComponent(q)}`)

export interface App {
  id: string
  name: string
  group: string
  domains: string[]
}
export const getApps = () => apiClient<{ apps: App[] }>('/api/v1/appctrl/apps').then((r) => r.apps ?? [])

// profileInterfaces: the interfaces each profile of a kind applies to, as
// the backend computes it (render.assign): every enabled non-WAN interface or
// VLAN with an address gets the profile of the first enabled accept policy
// whose src_intf names it (or all/any) and that sets an existing profile.
export function profileInterfaces(cfg: Candidate, field: 'webfilter_profile' | 'dnsfilter_profile' | 'appctrl_profile', profiles: string[]): Record<string, string[]> {
  const wan = new Set((cfg.interfaces ?? []).filter((i) => i.role === 'wan').map((i) => i.name))
  const links = [
    ...(cfg.interfaces ?? []).filter((i) => i.enabled && i.role !== 'wan' && i.address).map((i) => i.name),
    ...(cfg.vlans ?? []).filter((v) => v.enabled && !wan.has(v.parent) && v.address).map((v) => v.name),
  ]
  const known = new Set(profiles)
  const assigned = new Map<string, string>()
  for (const p of cfg.policies ?? []) {
    const prof = p[field]
    if (!p.enabled || p.action !== 'accept' || !prof || !known.has(prof)) continue
    const src = p.src_intf ?? []
    for (const l of links) {
      if (!assigned.has(l) && src.some((s) => s === l || s === 'all' || s === 'any')) assigned.set(l, prof)
    }
  }
  const out: Record<string, string[]> = {}
  for (const l of [...links].sort()) {
    const prof = assigned.get(l)
    if (prof) (out[prof] ??= []).push(l)
  }
  return out
}
