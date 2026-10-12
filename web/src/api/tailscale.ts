import { apiClient } from './client'

export interface TailscaleStatus {
  available: boolean
  backend_state?: string
  auth_url?: string
  hostname?: string
  ips?: string[]
  advertised_routes?: string[]
  approved_routes?: string[]
  peers?: TailscalePeer[]
}

export interface TailscalePeer {
  name: string
  dns_name?: string
  os?: string
  ips: string[]
  online: boolean
  last_seen?: string
  exit_node: boolean
  exit_node_option: boolean
  routes?: string[]
}

export interface TailscaleLoginRequest {
  auth_key?: string
}

export interface TailscaleSettings {
  enabled: boolean
  hostname?: string
  advertise_routes?: string[]
  exit_node?: boolean
  accept_routes?: boolean
  accept_dns?: boolean
  login_server?: string
  allow_access?: string[]
}

// Tailscale API functions
export async function getTailscaleStatus(): Promise<TailscaleStatus> {
  const data = await apiClient<TailscaleStatus>('/api/v1/vpn/tailscale/status')
  return data
}

export async function loginTailscale(authKey?: string): Promise<{ auth_url?: string; status?: string }> {
  const body = authKey ? { auth_key: authKey } : {}
  const data = await apiClient<{ auth_url?: string; status?: string }>('/api/v1/vpn/tailscale/login', {
    method: 'POST',
    body: JSON.stringify(body)
  })
  return data
}

export async function logoutTailscale(): Promise<{ status: string }> {
  const data = await apiClient<{ status: string }>('/api/v1/vpn/tailscale/logout', {
    method: 'POST'
  })
  return data
}

export async function patchTailscaleSettings(settings: TailscaleSettings): Promise<void> {
  await apiClient('/api/v1/config/candidate', {
    method: 'PATCH',
    body: JSON.stringify({
      path: 'vpn.tailscale',
      value: settings
    })
  })
}