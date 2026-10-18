// Candidate config API for the object and security profile pages. Types
// mirror internal/config (schema.go, objects.go, profiles.go) by JSON name;
// every page edits one list of the candidate and saves the whole list with
// PATCH {path, value} (see patchSection), then the CommitBar commits it.
import { apiClient } from './client'
import type { Policy, SaveResult } from './policy'

export interface IPv6 {
  mode: 'none' | 'dhcp6' | 'static' | 'delegated'
  address?: string // CIDR, static only
  gateway?: string
  pd_size?: number // 48-64, dhcp6 only
  uplink?: string // delegated: name of a dhcp6 interface ("" = automatic)
  prefix_id?: number // delegated
  ra?: { enabled: boolean; dns: boolean; managed?: boolean; other?: boolean }
}

// Smart queue (CAKE) on a WAN. The speeds stay in the config even when it is
// disabled, so re-enabling does not require typing them again.
export interface SQM {
  enabled: boolean
  download_mbit?: number // 1-100000, about 90% of the measured speed
  upload_mbit?: number // 1-100000
}

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
  ipv6?: IPv6
  sqm?: SQM
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
  ipv6?: IPv6
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
export const NOTIFY_EVENTS = ['commit', 'login_failed', 'ips_block', 'feed_error', 'wan_down', 'wan_up', 'update_available', 'plugin', 'block_added', 'new_device', 'test'] as const

// Schedules (task 057): weekly time windows a policy can be limited to.
// Times are in system.timezone. End before Start runs overnight; "24:00" is
// midnight; Start == End is invalid.
export interface ScheduleRange {
  days: string[] // mon tue wed thu fri sat sun
  start: string // HH:MM
  end: string // HH:MM, may be 24:00
}

export interface Schedule {
  name: string
  comment?: string
  ranges: ScheduleRange[]
}

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
  metrics?: MetricsSettings
}

// system.metrics: Prometheus /metrics on the admin port; allow = CIDRs that
// may scrape without an API token.
export interface MetricsSettings {
  enabled: boolean
  allow?: string[]
}

// Multi-WAN gateways, groups and routes (task 049/050). Types mirror
// internal/config/routing.go by JSON name.

export interface GatewayMonitor {
  ip?: string // IPv4; "" = pick a public resolver per gateway
  disabled?: boolean // keep the gateway always up (no pings)
  interval_seconds?: number // 0 = 2
  loss_percent?: number // 0 = 40
  latency_ms?: number // 0 = 1000
}

export interface Gateway {
  name: string
  interface: string // WAN interface/VLAN or a WireGuard client name
  address?: string // "dhcp" | router IPv4 | "" (tunnel)
  monitor?: GatewayMonitor
}

export interface GatewayGroupMember {
  gateway: string
  tier?: number // 1 = first choice
  weight?: number // share within the tier (1-100)
}

export interface GatewayGroup {
  name: string
  members: GatewayGroupMember[]
}

export interface Route {
  destination: string // IPv4 CIDR
  gateway: string // a gateway, not a group
  comment?: string
}

export interface WireGuardClient {
  name: string // device name: 1-15 of [a-z0-9_-]
  enabled: boolean
  address: string // tunnel IPv4/32 from the provider, e.g. 10.64.12.34/32
  peer_public_key: string
  endpoint: string // "host:port"
  allowed_ips?: string[] // default 0.0.0.0/0
  dns?: string[]
  keepalive?: number // seconds, 0 = 25
  mtu?: number
  kill_switch?: boolean
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
  schedules?: Schedule[]
  notifications?: { channels?: NotifyChannel[]; rules?: NotifyRule[] }
  gateways?: Gateway[]
  gateway_groups?: GatewayGroup[]
  routes?: Route[]
  default_gateway?: string
  vpn?: { wireguard_clients?: WireGuardClient[] }
  ddns?: DDNS[]
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

// DDNS (task 087/088): one dynamic DNS entry. The box pushes its public IP to
// a provider so the hostname always resolves.
export interface DDNS {
  name: string
  enabled: boolean
  provider: 'cloudflare' | 'duckdns' | 'custom'
  hostname: string
  interface?: string
  token?: string
  zone_id?: string
  url?: string
}

// DDNSStatus is the per-entry view from GET /api/v1/ddns.
export interface DDNSStatus {
  name: string
  hostname: string
  ip?: string
  last_update?: string
  last_error?: string
  last_attempt?: string
}

export const getDDNSStatus = () => apiClient<{ entries: DDNSStatus[] }>('/api/v1/ddns').then((r) => r.entries ?? [])
export const updateDDNSNow = (name: string) =>
  apiClient<DDNSStatus>(`/api/v1/ddns/${encodeURIComponent(name)}/update`, { method: 'POST' })

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
