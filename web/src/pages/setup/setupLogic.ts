// Step validation for the first-boot wizard. Mirrors BuildSetupConfig in
// internal/api/setup.go; the server stays authoritative.
import type { SecurityLevel, SetupRequest } from '../../api/setup'
import { inSubnet, ipv4, type Errors } from '../network/networkLogic'

export const MIN_PASSWORD = 10 // internal/auth MinPasswordLen
export const MAX_PASSWORD = 256

export const STEPS = ['Welcome', 'Admin password', 'WAN', 'LAN', 'Security', 'Review'] as const
export type Step = (typeof STEPS)[number]

export interface Answers {
  password: string
  confirm: string
  wanDevice: string
  wanMode: 'dhcp' | 'static'
  wanAddress: string
  wanGateway: string
  lanDevice: string
  lanAddress: string
  dhcp: boolean
  dhcpStart: string
  dhcpEnd: string
  level: SecurityLevel
}

export const initialAnswers = (): Answers => ({
  password: '',
  confirm: '',
  wanDevice: '',
  wanMode: 'dhcp',
  wanAddress: '',
  wanGateway: '',
  lanDevice: '',
  lanAddress: '192.168.1.1/24',
  dhcp: true,
  dhcpStart: '192.168.1.100',
  dhcpEnd: '192.168.1.199',
  level: 'basic',
})

// passwordStrength scores 0-4 from length and character variety.
export function passwordStrength(pw: string): { score: number; label: string } {
  let score = 0
  if (pw.length >= MIN_PASSWORD) score++
  if (pw.length >= 14) score++
  const kinds = [/[a-z]/, /[A-Z]/, /\d/, /[^A-Za-z0-9]/].filter((r) => r.test(pw)).length
  if (kinds >= 2) score++
  if (kinds >= 3 && pw.length >= MIN_PASSWORD) score++
  if (pw.length < MIN_PASSWORD) score = Math.min(score, 1)
  return { score, label: ['Too weak', 'Weak', 'Fair', 'Good', 'Strong'][score] }
}

interface Prefix {
  addr: number
  bits: number
}

// cidr parses an IPv4 CIDR such as 192.168.1.1/24.
export function cidr(s: string): Prefix | null {
  const m = /^([^/]+)\/(\d{1,2})$/.exec(s.trim())
  if (!m) return null
  const addr = ipv4(m[1])
  const bits = Number(m[2])
  if (addr === null || bits > 32) return null
  return { addr, bits }
}

const mask = (bits: number) => (bits === 0 ? 0 : (0xffffffff << (32 - bits)) >>> 0)
const fmt = (n: number) => [24, 16, 8, 0].map((s) => (n >>> s) & 255).join('.')

// suggestDHCPRange proposes a range in the LAN subnet that skips the LAN
// address: .100-.199 style for /24 and larger, otherwise the upper half.
export function suggestDHCPRange(lan: string): { start: string; end: string } | null {
  const p = cidr(lan)
  if (!p || p.bits < 8 || p.bits > 30) return null
  const net = (p.addr & mask(p.bits)) >>> 0
  const size = 2 ** (32 - p.bits)
  let lo: number
  let hi: number
  if (size >= 256) {
    const base = (p.addr & mask(24)) >>> 0
    ;[lo, hi] = [base + 100, base + 199]
  } else {
    ;[lo, hi] = [net + size / 2, net + size - 2]
  }
  if (p.addr >= lo && p.addr <= hi) {
    // the LAN address sits in the range: use the part below it instead
    ;[lo, hi] = [net + 1 === p.addr ? net + 2 : net + 1, p.addr - 1]
    if (lo > hi) return null
  }
  return { start: fmt(lo), end: fmt(hi) }
}

export function validateStep(step: Step, a: Answers): Errors {
  const e: Errors = {}
  switch (step) {
    case 'Admin password':
      if (a.password.length < MIN_PASSWORD) e.password = `At least ${MIN_PASSWORD} characters`
      else if (a.password.length > MAX_PASSWORD) e.password = `At most ${MAX_PASSWORD} characters`
      if (a.confirm !== a.password) e.confirm = 'Passwords do not match'
      break
    case 'WAN': {
      if (!a.wanDevice) e.wanDevice = 'Choose the device plugged into your modem'
      if (a.wanMode === 'static') {
        const p = cidr(a.wanAddress)
        if (!p) e.wanAddress = 'IPv4 address with prefix, e.g. 203.0.113.2/24'
        if (ipv4(a.wanGateway) === null) e.wanGateway = 'IPv4 address of your ISP gateway'
        else if (p && (!inSubnet(a.wanGateway, a.wanAddress) || ipv4(a.wanGateway) === p.addr))
          e.wanGateway = 'Gateway must be another address in the WAN subnet'
      }
      break
    }
    case 'LAN': {
      if (!a.lanDevice) e.lanDevice = 'Choose the device for your home network'
      else if (a.lanDevice === a.wanDevice) e.lanDevice = 'LAN and WAN must use different devices'
      const p = cidr(a.lanAddress)
      if (!p) e.lanAddress = 'IPv4 address with prefix, e.g. 192.168.1.1/24'
      else if (p.bits < 8 || p.bits > 30) e.lanAddress = 'Prefix must be between /8 and /30'
      else if (((p.addr & mask(p.bits)) >>> 0) === p.addr) e.lanAddress = 'Use a host address, not the network address'
      else if (a.wanMode === 'static' && cidr(a.wanAddress)) {
        const w = cidr(a.wanAddress)!
        const bits = Math.min(w.bits, p.bits)
        if (((w.addr & mask(bits)) >>> 0) === ((p.addr & mask(bits)) >>> 0)) e.lanAddress = 'LAN subnet overlaps the WAN subnet'
      }
      if (a.dhcp && !e.lanAddress) {
        const s = ipv4(a.dhcpStart)
        const t = ipv4(a.dhcpEnd)
        if (s === null || !inSubnet(a.dhcpStart, a.lanAddress)) e.dhcpStart = 'Must be an address in the LAN subnet'
        if (t === null || !inSubnet(a.dhcpEnd, a.lanAddress)) e.dhcpEnd = 'Must be an address in the LAN subnet'
        if (!e.dhcpStart && !e.dhcpEnd) {
          if (t! < s!) e.dhcpEnd = 'End is before start'
          else if (p!.addr >= s! && p!.addr <= t!) e.dhcpStart = 'Range must not include the LAN address'
        }
      }
      break
    }
  }
  return e
}

export function buildRequest(a: Answers): SetupRequest {
  return {
    admin_password: a.password,
    wan:
      a.wanMode === 'static'
        ? { device: a.wanDevice, mode: 'static', address: a.wanAddress.trim(), gateway: a.wanGateway.trim() }
        : { device: a.wanDevice, mode: 'dhcp' },
    lan: a.dhcp
      ? { device: a.lanDevice, address: a.lanAddress.trim(), dhcp_start: a.dhcpStart.trim(), dhcp_end: a.dhcpEnd.trim() }
      : { device: a.lanDevice, address: a.lanAddress.trim() },
    security_level: a.level,
  }
}

// stepForPath maps a server error path back to the wizard step that edits it.
export function stepForPath(path: string): Step {
  if (path.startsWith('admin')) return 'Admin password'
  if (path.startsWith('wan') || path.startsWith('interfaces[0]')) return 'WAN'
  if (path.startsWith('lan') || path.startsWith('interfaces') || path.startsWith('dhcp')) return 'LAN'
  if (path.startsWith('security') || path.startsWith('profiles') || path.startsWith('policies')) return 'Security'
  return 'Review'
}

// What the box runs now (GET /api/v1/setup/state "current"): ports and LAN
// settings someone may already have chosen on the console.
export interface SetupCurrent {
  wan_device?: string
  lan_device?: string
  lan_address?: string
  dhcp_start?: string
  dhcp_end?: string
}

// withCurrent starts the wizard from the running config. Ports are taken
// only if they exist (a fresh install may name eth0/eth1 that are not
// there); otherwise the first two devices are suggested.
export function withCurrent(a: Answers, cur: SetupCurrent | undefined, devices: string[]): Answers {
  const has = (d?: string) => !!d && devices.includes(d)
  const wanDevice = has(cur?.wan_device) ? cur!.wan_device! : devices[0] ?? ''
  const lanDevice = has(cur?.lan_device) && cur!.lan_device !== wanDevice ? cur!.lan_device! : devices.find((d) => d !== wanDevice) ?? ''
  const out = { ...a, wanDevice: a.wanDevice || wanDevice, lanDevice: a.lanDevice || lanDevice }
  if (cur?.lan_address) out.lanAddress = cur.lan_address
  if (cur?.dhcp_start && cur?.dhcp_end) {
    out.dhcpStart = cur.dhcp_start
    out.dhcpEnd = cur.dhcp_end
  }
  return out
}
