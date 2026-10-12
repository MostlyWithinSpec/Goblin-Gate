import { apiClient } from './client'

export interface WireGuardPeer {
  name: string
  public_key: string
  address: string
  allowed_ips?: string[]
  keepalive?: number
  endpoint?: string
  latest_handshake?: string
  rx_bytes?: number
  tx_bytes?: number
}

export interface WireGuardServer {
  name: string
  enabled: boolean
  public_key: string
  listen_port: number
  address: string
  endpoint?: string
  peers: WireGuardPeer[]
}

export interface WireGuardConfig {
  interfaces: WireGuardServer[]
}

export interface WireGuardPeerRequest {
  name: string
  full_tunnel?: boolean
  keepalive?: number
}

export interface WireGuardPeerResponse {
  peer: WireGuardPeer
  config: string
  qr_svg?: string
}

export const getWireGuardConfig = async (): Promise<WireGuardConfig> => {
  return apiClient<WireGuardConfig>('/api/v1/vpn/wireguard')
}

export const addWireGuardPeer = async (interfaceName: string, request: WireGuardPeerRequest): Promise<WireGuardPeerResponse> => {
  return apiClient<WireGuardPeerResponse>(`/api/v1/vpn/wireguard/${interfaceName}/peers`, {
    method: 'POST',
    body: JSON.stringify(request)
  })
}