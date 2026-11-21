// Dynamic routing (BGP and OSPF through FRR, task 153): the `routing` config
// section and its live status. The types mirror internal/config/dynrouting.go
// and internal/frr/status.go by JSON name. The section is one nested object,
// so it is saved whole with patchSection('routing', value) like any other path.
import { apiClient } from './client'

// PrefixRule permits or denies one prefix on a neighbour's import or export
// filter. The rules are ordered and the first match wins; a non-empty list ends
// with an implicit deny. max_length 0 (left out) means exactly `prefix`.
export interface PrefixRule {
  action: 'permit' | 'deny'
  prefix: string // CIDR without host bits, e.g. 10.0.0.0/8
  max_length?: number
}

export interface BGPNeighbor {
  address: string // the neighbour's IPv4 or IPv6 address
  remote_as: number
  enabled: boolean
  description?: string
  // password is the TCP MD5 password both sides share. It is a secret: the API
  // returns "" for a caller that may not see secrets, and a neighbour sent back
  // with a blank password keeps its stored one while nothing else changed.
  password?: string
  multihop?: number // 0 = directly connected, up to 255
  import?: PrefixRule[]
  export?: PrefixRule[]
}

export interface BGPSection {
  enabled: boolean
  as: number // 1-4294967294
  // announce names address objects of type host or subnet and interfaces or
  // VLANs (their networks). A prefix is announced only while the box has a route to it.
  announce?: string[]
  redistribute_connected?: boolean
  redistribute_static?: boolean
  neighbors?: BGPNeighbor[]
}

export interface OSPFInterface {
  name: string // an interface or VLAN name
  cost?: number // 0 = FRR's default, else 1-65535
  passive?: boolean
}

export interface OSPFArea {
  id: string // "0" or "0.0.0.1"
  interfaces: OSPFInterface[]
}

export interface OSPFSection {
  enabled: boolean
  areas?: OSPFArea[]
  redistribute_connected?: boolean
  redistribute_static?: boolean
}

// RoutingConfig is the whole `routing` section. router_id is an IPv4 address
// the box need not own; "" takes it from the first static LAN-side address.
export interface RoutingConfig {
  router_id?: string
  bgp: BGPSection
  ospf: OSPFSection
}

export interface BGPNeighborStatus {
  address: string
  family: string // ipv4 | ipv6
  remote_as: number
  description?: string
  state: string // Established, Active, Connect, Idle, ...
  up: boolean
  uptime_seconds: number // 0 when not up
  prefixes_received: number
  prefixes_sent: number
}

export interface OSPFNeighborStatus {
  router_id: string
  address: string
  interface: string // the kernel device, e.g. eth1
  state: string // Full/DR, 2-Way/DROther, Init, ...
  full: boolean
}

export interface LearnedRoute {
  prefix: string
  protocol: string // bgp | ospf
  selected: boolean // the best route to the prefix
  installed: boolean
  distance: number
  metric: number
  next_hops: string[]
  interface?: string
}

// RoutingStatus is GET /api/v1/routing/status: whether BGP and OSPF are on in
// the *running* config, and what FRR reports now. It answers 503 when the box
// cannot reach FRR at all (dev mode), which apiClient throws on.
export interface RoutingStatus {
  bgp_enabled: boolean
  ospf_enabled: boolean
  router_id?: string
  running: boolean
  bgp: BGPNeighborStatus[]
  ospf: OSPFNeighborStatus[]
  routes: LearnedRoute[]
}

export function getRoutingStatus(): Promise<RoutingStatus> {
  return apiClient<RoutingStatus>('/api/v1/routing/status')
}
