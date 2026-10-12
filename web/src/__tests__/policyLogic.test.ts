import { describe, expect, test } from 'vitest'
import type { Policy, PolicyObjects } from '../api/policy'
import {
  applySecurityLevel,
  clean,
  clonePolicy,
  groupByPair,
  move,
  newPolicy,
  profileBadges,
  validatePolicy,
} from '../pages/policy/policyLogic'

const objects: PolicyObjects = {
  interfaces: ['lan', 'wan1', 'guest'],
  addresses: ['all', 'nas'],
  services: ['any', 'web'],
  profiles: { ips: ['basic', 'strict'], webfilter: ['basic'], dnsfilter: [], appctrl: ['strict'] },
  feeds: ['drop'],
}

const pol = (id: number, extra: Partial<Policy> = {}): Policy => ({
  id,
  name: `p${id}`,
  enabled: true,
  src_intf: ['lan'],
  dst_intf: ['wan1'],
  src_addr: ['all'],
  dst_addr: ['all'],
  service: ['any'],
  action: 'accept',
  ...extra,
})

describe('validatePolicy', () => {
  test('accepts a complete policy', () => {
    expect(validatePolicy(pol(1), objects, [pol(2)])).toEqual({})
  })
  test('reports missing and unknown fields', () => {
    const e = validatePolicy(pol(1, { name: ' ', src_intf: [], service: ['ftp'] }), objects, [])
    expect(e.name).toMatch(/required/)
    expect(e.src_intf).toMatch(/at least one/)
    expect(e.service).toMatch(/Unknown service: ftp/)
  })
  test('reports duplicate id and name among the other policies', () => {
    const e = validatePolicy(pol(2, { name: 'p3' }), objects, [pol(2, { name: 'x' }), pol(3)])
    expect(e.id).toMatch(/already used/)
    expect(e.name).toMatch(/already used/)
  })
})

describe('list helpers', () => {
  test('newPolicy takes the next free id', () => {
    expect(newPolicy([pol(4), pol(9)]).id).toBe(10)
  })
  test('clonePolicy gets a fresh id and a unique name and deep-copies lists', () => {
    const ps = [pol(1), pol(2, { name: 'p1-copy' })]
    const c = clonePolicy(ps[0], ps)
    expect(c.id).toBe(3)
    expect(c.name).toBe('p1-copy2')
    c.src_intf!.push('guest')
    expect(ps[0].src_intf).toEqual(['lan'])
  })
  test('move reorders', () => {
    expect(move([pol(1), pol(2), pol(3)], 2, 0).map((p) => p.id)).toEqual([3, 1, 2])
  })
  test('groupByPair keeps policy order and groups by first appearance', () => {
    const g = groupByPair([pol(1), pol(2, { src_intf: ['guest'] }), pol(3)])
    expect(g.map((x) => x.key)).toEqual(['lan → wan1', 'guest → wan1'])
    expect(g[0].policies.map((p) => p.id)).toEqual([1, 3])
  })
})

describe('security profiles', () => {
  test('a level picks same-named profiles where they exist', () => {
    const p = applySecurityLevel(pol(1), 'strict', objects)
    expect(p.ips_profile).toBe('strict')
    expect(p.webfilter_profile).toBeUndefined()
    expect(p.appctrl_profile).toBe('strict')
    expect(applySecurityLevel(p, 'off', objects).ips_profile).toBeUndefined()
  })
  test('badges', () => {
    expect(profileBadges(pol(1, { ips_profile: 'basic', threat_feeds: ['drop'] }))).toEqual(['IPS', 'FEED'])
  })
  test('clean drops empty optional fields', () => {
    expect(clean(pol(1, { nat: false, threat_feeds: [], ips_profile: undefined }))).toEqual(pol(1))
  })
})
