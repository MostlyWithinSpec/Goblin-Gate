// Firewall policy API: types mirror internal/config/policy.go (JSON names),
// and the object lists the editor offers come from the candidate config.
import { apiClient } from './client'

export type PolicyAction = 'accept' | 'deny'
export type PolicyLog = 'off' | 'all' | 'deny'

export interface Policy {
  id: number
  name: string
  enabled: boolean
  src_intf?: string[]
  dst_intf?: string[]
  src_addr?: string[] // address/group names or "all"
  dst_addr?: string[]
  service?: string[] // service/group names or "any"
  action: PolicyAction
  nat?: boolean
  log?: PolicyLog
  schedule?: string
  ips_profile?: string
  webfilter_profile?: string
  dnsfilter_profile?: string
  appctrl_profile?: string
  threat_feeds?: string[]
  gateway?: string // a gateway or gateway group name; "" = default route
}

interface Named {
  name: string
}

// The parts of the candidate config the policy page reads.
export interface PolicyConfig {
  interfaces?: Named[]
  vlans?: Named[]
  addresses?: Named[]
  address_groups?: Named[]
  services?: Named[]
  service_groups?: Named[]
  policies?: Policy[]
  profiles?: {
    ips?: Named[]
    webfilter?: Named[]
    dnsfilter?: Named[]
    appctrl?: Named[]
  }
  feeds?: Named[]
  schedules?: Named[]
  gateways?: Named[]
  gateway_groups?: Named[]
}

export interface ValidationError {
  path: string
  message: string
}

export interface SaveResult {
  status: string
  validation_errors: ValidationError[] | null
}

// Choices for the editor's selects.
export interface PolicyObjects {
  interfaces: string[]
  addresses: string[] // includes "all"
  services: string[] // includes "any"
  profiles: { ips: string[]; webfilter: string[]; dnsfilter: string[]; appctrl: string[] }
  feeds: string[]
  schedules: string[]
  gateways: string[]
  gatewayGroups: string[]
}

const names = (xs?: Named[]) => (xs ?? []).map((x) => x.name)

export function objectsOf(cfg: PolicyConfig): PolicyObjects {
  return {
    interfaces: [...names(cfg.interfaces), ...names(cfg.vlans)],
    addresses: ['all', ...names(cfg.addresses), ...names(cfg.address_groups)],
    services: ['any', ...names(cfg.services), ...names(cfg.service_groups)],
    profiles: {
      ips: names(cfg.profiles?.ips),
      webfilter: names(cfg.profiles?.webfilter),
      dnsfilter: names(cfg.profiles?.dnsfilter),
      appctrl: names(cfg.profiles?.appctrl),
    },
    feeds: names(cfg.feeds),
    schedules: names(cfg.schedules),
    gateways: names(cfg.gateways),
    gatewayGroups: names(cfg.gateway_groups),
  }
}

export function getCandidate(): Promise<PolicyConfig> {
  return apiClient<PolicyConfig>('/api/v1/config/candidate')
}

// savePolicies replaces the whole policies list in the candidate (order matters:
// the first matching policy wins). The candidate is stored even when invalid;
// validation_errors lists what a commit would reject.
export function savePolicies(policies: Policy[]): Promise<SaveResult> {
  return apiClient<SaveResult>('/api/v1/config/candidate', {
    method: 'PATCH',
    body: JSON.stringify({ path: 'policies', value: policies }),
  })
}

// Hit counters arrive with task 024; until then the table shows "—".
export async function getPolicyHits(): Promise<Record<number, number> | null> {
  try {
    const res = await apiClient<{ policies?: { id: number; packets?: number; hits?: number }[] }>(
      '/api/v1/stats/policies'
    )
    const out: Record<number, number> = {}
    for (const p of res.policies ?? []) out[p.id] = p.hits ?? p.packets ?? 0
    return out
  } catch {
    return null
  }
}
