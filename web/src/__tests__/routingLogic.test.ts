import { describe, expect, it } from 'vitest'
import {
  announceOptions,
  areaID,
  bgpBadge,
  buildRouting,
  emptyNeighbor,
  formOf,
  formatUptime,
  frrRunning,
  interfaceOptions,
  moveRule,
  ospfBadge,
  sortRoutes,
  validateRouting,
  type NeighborForm,
  type RoutingForm,
} from '../pages/network/routingLogic'
import type { Candidate } from '../api/objects'
import type { RoutingConfig, RoutingStatus } from '../api/routing'

const base = formOf(undefined)
const f = (patch: Partial<RoutingForm>): RoutingForm => ({ ...base, ...patch })
// nb builds one neighbour form on top of an empty one, so a test only spells out
// the fields it is about.
const nb = (patch: Partial<NeighborForm>): NeighborForm => ({ ...emptyNeighbor(), ...patch })

describe('formOf and buildRouting', () => {
  it('starts every protocol off with no neighbours', () => {
    const form = formOf(undefined)
    expect(form.bgp_enabled).toBe(false)
    expect(form.bgp_neighbors).toEqual([])
    expect(form.ospf_areas).toEqual([])
    expect(buildRouting(form)).toEqual({ bgp: { enabled: false, as: 0 }, ospf: { enabled: false } })
  })

  it('reads a stored section back into the form', () => {
    const stored: RoutingConfig = {
      router_id: '192.0.2.9',
      bgp: {
        enabled: true,
        as: 64512,
        announce: ['lan', 'srv01'],
        redistribute_connected: true,
        neighbors: [{ address: '198.51.100.1', remote_as: 64513, enabled: true, description: 'uplink', multihop: 2, password: 'secret' }],
      },
      ospf: { enabled: true, areas: [{ id: '0', interfaces: [{ name: 'lan', cost: 20, passive: true }] }] },
    }
    const form = formOf(stored)
    expect(form.router_id).toBe('192.0.2.9')
    expect(form.bgp_as).toBe('64512')
    expect(form.bgp_announce).toEqual(['lan', 'srv01'])
    expect(form.bgp_neighbors[0]).toMatchObject({ address: '198.51.100.1', remote_as: '64513', multihop: '2', password: 'secret' })
    expect(form.ospf_areas[0].interfaces[0]).toEqual({ name: 'lan', cost: '20', passive: true })
  })

  it('round-trips a full section without losing a field', () => {
    const stored: RoutingConfig = {
      router_id: '192.0.2.9',
      bgp: {
        enabled: true,
        as: 64512,
        announce: ['lan'],
        redistribute_connected: true,
        redistribute_static: true,
        neighbors: [
          {
            address: '198.51.100.1',
            remote_as: 64513,
            enabled: true,
            password: 'secret',
            multihop: 2,
            import: [{ action: 'permit', prefix: '10.0.0.0/8', max_length: 24 }],
            export: [{ action: 'deny', prefix: '0.0.0.0/0' }],
          },
        ],
      },
      ospf: { enabled: true, areas: [{ id: '0', interfaces: [{ name: 'lan', cost: 20, passive: true }] }], redistribute_static: true },
    }
    expect(buildRouting(formOf(stored))).toEqual(stored)
  })

  it('leaves a blank password out so the stored one is kept', () => {
    const built = buildRouting(f({ bgp_enabled: true, bgp_as: '64512', bgp_neighbors: [nb({ address: '198.51.100.1', remote_as: '64513', password: '  ' })] }))
    expect(built.bgp.neighbors?.[0].password).toBeUndefined()
  })

  it('keeps an AS that was typed while BGP is off, so it survives an OSPF save', () => {
    const built = buildRouting(f({ bgp_enabled: false, bgp_as: '64512', ospf_enabled: false }))
    expect(built).toEqual({ bgp: { enabled: false, as: 64512 }, ospf: { enabled: false } })
  })

  it('leaves out what a disabled protocol does not use', () => {
    const built = buildRouting(
      f({
        bgp_enabled: false,
        bgp_announce: ['lan'],
        bgp_neighbors: [nb({ address: '198.51.100.1', remote_as: '64513' })],
        ospf_enabled: false,
        ospf_areas: [{ id: '0', interfaces: [{ name: 'lan', cost: '', passive: false }] }],
      }),
    )
    expect(built.bgp.announce).toBeUndefined()
    expect(built.bgp.neighbors).toBeUndefined()
    expect(built.ospf.areas).toBeUndefined()
  })

  it('leaves out defaults such as a zero cost and multihop', () => {
    const built = buildRouting(
      f({
        bgp_enabled: true,
        bgp_as: '64512',
        bgp_neighbors: [nb({ address: '198.51.100.1', remote_as: '64513', multihop: '0' })],
        ospf_enabled: true,
        ospf_areas: [{ id: '0', interfaces: [{ name: 'lan', cost: '0', passive: false }] }],
      }),
    )
    expect(built.bgp.neighbors?.[0].multihop).toBeUndefined()
    expect(built.ospf.areas?.[0].interfaces[0]).toEqual({ name: 'lan' })
  })
})

describe('validateRouting', () => {
  it('says nothing while both protocols are off', () => {
    expect(validateRouting(f({}))).toEqual({})
  })

  it('checks our own AS the way the server does', () => {
    expect(validateRouting(f({ bgp_enabled: true, bgp_as: '' }))).toHaveProperty('bgp.as')
    expect(validateRouting(f({ bgp_enabled: true, bgp_as: '0' }))).toHaveProperty('bgp.as')
    expect(validateRouting(f({ bgp_enabled: true, bgp_as: '23456' }))).toHaveProperty('bgp.as')
    expect(validateRouting(f({ bgp_enabled: true, bgp_as: '4294967295' }))).toHaveProperty('bgp.as')
    expect(validateRouting(f({ bgp_enabled: true, bgp_as: '4294967296' }))).toHaveProperty('bgp.as')
    expect(validateRouting(f({ bgp_enabled: true, bgp_as: 'not-a-number' }))).toHaveProperty('bgp.as')
    expect(validateRouting(f({ bgp_enabled: true, bgp_as: '64512' }))).not.toHaveProperty('bgp.as')
  })

  it('wants a v4 router ID when one is typed', () => {
    expect(validateRouting(f({ bgp_enabled: true, router_id: 'not-an-address' }))).toHaveProperty('router_id')
    expect(validateRouting(f({ bgp_enabled: true, router_id: '2001:db8::1' }))).toHaveProperty('router_id')
    expect(validateRouting(f({ bgp_enabled: true, router_id: '192.0.2.9' }))).not.toHaveProperty('router_id')
  })

  it('rejects a neighbour that cannot talk', () => {
    const errs = validateRouting(
      f({
        bgp_enabled: true,
        bgp_as: '64512',
        bgp_neighbors: [nb({ address: 'nope', remote_as: '0', multihop: '256', description: 'bad * char', password: 'has space' })],
      }),
    )
    expect(errs['bgp.neighbors[0].address']).toBeTruthy()
    expect(errs['bgp.neighbors[0].remote_as']).toBeTruthy()
    expect(errs['bgp.neighbors[0].multihop']).toBeTruthy()
    expect(errs['bgp.neighbors[0].description']).toBeTruthy()
    expect(errs['bgp.neighbors[0].password']).toBeTruthy()
  })

  it('refuses an address that is no use as a neighbour, and a duplicate', () => {
    const mk = (a: string) => f({ bgp_enabled: true, bgp_as: '64512', bgp_neighbors: [nb({ address: a, remote_as: '64513' })] })
    // Go rejects the whole 127/8 and any ::1-style loopback, not just the two
    // addresses people usually type.
    for (const a of ['0.0.0.0', '127.0.0.1', '127.0.0.5', '224.0.0.5', 'ff02::5', '::1'])
      expect(validateRouting(mk(a))).toHaveProperty('bgp.neighbors[0].address')
    const twice = f({
      bgp_enabled: true,
      bgp_as: '64512',
      bgp_neighbors: [nb({ address: '198.51.100.1', remote_as: '64513' }), nb({ address: '198.51.100.1', remote_as: '64514' })],
    })
    expect(validateRouting(twice)).toHaveProperty('bgp.neighbors[1].address')
  })

  it('accepts a v6 neighbour', () => {
    const ok = f({ bgp_enabled: true, bgp_as: '64512', bgp_neighbors: [nb({ address: '2001:db8::1', remote_as: '64513' })] })
    expect(validateRouting(ok)).toEqual({})
  })

  it('checks a prefix rule: no host bits, and max_length inside the prefix', () => {
    const rule = (prefix: string, max_length: string) =>
      f({ bgp_enabled: true, bgp_as: '64512', bgp_neighbors: [nb({ address: '198.51.100.1', remote_as: '64513', import: [{ action: 'permit', prefix, max_length }] })] })
    expect(validateRouting(rule('10.0.0.0/8', ''))).toEqual({})
    expect(validateRouting(rule('10.0.0.1/8', ''))).toHaveProperty('bgp.neighbors[0].import[0].prefix')
    expect(validateRouting(rule('nope', ''))).toHaveProperty('bgp.neighbors[0].import[0].prefix')
    expect(validateRouting(rule('10.0.0.0/8', '8'))).toEqual({})
    expect(validateRouting(rule('10.0.0.0/8', '7'))).toHaveProperty('bgp.neighbors[0].import[0].max_length')
    expect(validateRouting(rule('10.0.0.0/8', '33'))).toHaveProperty('bgp.neighbors[0].import[0].max_length')
    expect(validateRouting(rule('ffff::/16', '16'))).toEqual({})
    expect(validateRouting(rule('ffff::/16', '15'))).toHaveProperty('bgp.neighbors[0].import[0].max_length')
    expect(validateRouting(rule('2001:db8::/32', ''))).toEqual({})
    expect(validateRouting(rule('2001:db8::1/32', ''))).toHaveProperty('bgp.neighbors[0].import[0].prefix')
  })

  it('wants at least one area and one interface, and no interface in two areas', () => {
    expect(validateRouting(f({ ospf_enabled: true, ospf_areas: [] }))).toHaveProperty('ospf.areas')
    expect(validateRouting(f({ ospf_enabled: true, ospf_areas: [{ id: '0', interfaces: [] }] }))).toHaveProperty('ospf.areas[0].interfaces')
    const twoAreas = f({
      ospf_enabled: true,
      ospf_areas: [
        { id: '0', interfaces: [{ name: 'lan', cost: '', passive: false }] },
        { id: '0', interfaces: [{ name: 'lan', cost: '', passive: false }] },
      ],
    })
    const errs = validateRouting(twoAreas)
    expect(errs['ospf.areas[1].id']).toBeTruthy()
    expect(errs['ospf.areas[1].interfaces[0].name']).toBeTruthy()
  })

  it('takes an area ID as a number or a dotted ID, and rejects anything else', () => {
    expect(areaID('0')).toBe('0.0.0.0')
    expect(areaID('1')).toBe('0.0.0.1')
    expect(areaID('0.0.0.1')).toBe('0.0.0.1')
    expect(areaID('x')).toBeNull()
    expect(areaID('300.1.1.1')).toBeNull()
    const errs = validateRouting(f({ ospf_enabled: true, ospf_areas: [{ id: '42', interfaces: [{ name: 'lan', cost: '70000', passive: false }] }] }))
    expect(errs['ospf.areas[0].interfaces[0].cost']).toBeTruthy()
    expect(errs).not.toHaveProperty('ospf.areas[0].id')
  })

  it('agrees with the server on a save that is fine', () => {
    const good = f({
      bgp_enabled: true,
      bgp_as: '64512',
      bgp_neighbors: [nb({ address: '198.51.100.1', remote_as: '64513', import: [{ action: 'deny', prefix: '0.0.0.0/0', max_length: '' }] })],
      ospf_enabled: true,
      ospf_areas: [{ id: '0', interfaces: [{ name: 'lan', cost: '10', passive: false }] }],
    })
    expect(validateRouting(good)).toEqual({})
  })
})

describe('the pickers', () => {
  const cfg: Candidate = {
    addresses: [
      { name: 'srv01', type: 'host', value: '192.168.1.20' },
      { name: 'office', type: 'subnet', value: '10.0.0.0/24' },
      { name: 'badguys', type: 'fqdn', value: 'evil.example' },
    ],
    interfaces: [
      { name: 'wan', device: 'eth0', role: 'wan', mode: 'dhcp', enabled: true },
      { name: 'lan', device: 'eth1', role: 'lan', mode: 'static', address: '10.0.0.1/24', enabled: true },
    ],
    vlans: [{ name: 'guest', parent: 'lan', vlan_id: 20, mode: 'static', address: '10.0.20.1/24', enabled: true }],
  }

  it('offers host and subnet objects plus interfaces and VLANs, and nothing else', () => {
    expect(announceOptions(cfg).map((o) => o.value)).toEqual(['srv01', 'office', 'wan', 'lan', 'guest'])
  })

  it('offers only interfaces and VLANs to OSPF', () => {
    expect(interfaceOptions(cfg)).toEqual(['wan', 'lan', 'guest'])
  })

  it('copes with a candidate that has nothing yet', () => {
    expect(announceOptions({})).toEqual([])
    expect(interfaceOptions({})).toEqual([])
  })
})

describe('rule order', () => {
  const rules = [
    { action: 'permit', prefix: '10.0.0.0/8', max_length: '' },
    { action: 'deny', prefix: '0.0.0.0/0', max_length: '' },
    { action: 'permit', prefix: '192.168.0.0/16', max_length: '' },
  ]

  it('moves a rule up or down and keeps the rest', () => {
    expect(moveRule(rules, 2, -1).map((r) => r.prefix)).toEqual(['10.0.0.0/8', '192.168.0.0/16', '0.0.0.0/0'])
    expect(moveRule(rules, 0, 1).map((r) => r.prefix)).toEqual(['0.0.0.0/0', '10.0.0.0/8', '192.168.0.0/16'])
  })

  it('does nothing at the ends and keeps the original list', () => {
    expect(moveRule(rules, 0, -1)).toEqual(rules)
    expect(moveRule(rules, 2, 1)).toEqual(rules)
    expect(rules.map((r) => r.prefix)).toEqual(['10.0.0.0/8', '0.0.0.0/0', '192.168.0.0/16'])
  })
})

describe('the live status', () => {
  const st = (patch: Partial<RoutingStatus>): RoutingStatus => ({
    bgp_enabled: true,
    ospf_enabled: false,
    running: true,
    bgp: [],
    ospf: [],
    routes: [],
    ...patch,
  })

  it('calls an up session good and a half-open one a warning', () => {
    expect(bgpBadge({ up: true, state: 'Established' })).toBe('ok')
    expect(bgpBadge({ up: false, state: 'Active' })).toBe('warn')
    expect(bgpBadge({ up: false, state: 'Connect' })).toBe('warn')
    expect(bgpBadge({ up: false, state: 'Idle' })).toBe('bad')
    expect(ospfBadge({ full: true })).toBe('ok')
    expect(ospfBadge({ full: false })).toBe('warn')
  })

  it('only calls FRR "starting" while a protocol is on', () => {
    expect(frrRunning(st({ running: true }))).toBe(true)
    expect(frrRunning(st({ running: false }))).toBe(false)
    expect(frrRunning(st({ running: false, bgp_enabled: false }))).toBe(true)
  })

  it('shows an uptime coarsely and nothing when the session is not up', () => {
    expect(formatUptime(0)).toBe('')
    expect(formatUptime(45)).toBe('45 s')
    expect(formatUptime(60)).toBe('1 min')
    expect(formatUptime(7200)).toBe('2 h')
    expect(formatUptime(172800)).toBe('2 d')
  })

  it('puts the selected route first, then protocol and prefix, without touching the input', () => {
    const routes = [
      { prefix: '203.0.113.0/24', protocol: 'ospf', selected: true, installed: true, distance: 110, metric: 20, next_hops: ['192.0.2.2'] },
      { prefix: '10.0.0.0/8', protocol: 'bgp', selected: false, installed: false, distance: 20, metric: 0, next_hops: ['198.51.100.1'] },
      { prefix: '0.0.0.0/0', protocol: 'bgp', selected: true, installed: true, distance: 20, metric: 0, next_hops: ['198.51.100.1'] },
    ]
    expect(sortRoutes(routes).map((r) => r.prefix)).toEqual(['0.0.0.0/0', '203.0.113.0/24', '10.0.0.0/8'])
    expect(routes.map((r) => r.prefix)).toEqual(['203.0.113.0/24', '10.0.0.0/8', '0.0.0.0/0'])
  })
})
