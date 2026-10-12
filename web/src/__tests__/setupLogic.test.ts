import { describe, expect, test } from 'vitest'
import { buildRequest, initialAnswers, passwordStrength, stepForPath, suggestDHCPRange, validateStep, withCurrent, type Answers } from '../pages/setup/setupLogic'

const ans = (x: Partial<Answers> = {}): Answers => ({ ...initialAnswers(), wanDevice: 'eth0', lanDevice: 'eth1', password: 'correct horse battery', confirm: 'correct horse battery', ...x })

describe('passwordStrength', () => {
  test('scores length and variety', () => {
    expect(passwordStrength('abc').score).toBeLessThanOrEqual(1)
    expect(passwordStrength('Abcdefgh12!').score).toBeGreaterThanOrEqual(3)
    expect(passwordStrength('Tr0ub4dor&3-horse').label).toBe('Strong')
  })
})

describe('suggestDHCPRange', () => {
  test('/24 gets .100-.199', () => {
    expect(suggestDHCPRange('10.0.5.1/24')).toEqual({ start: '10.0.5.100', end: '10.0.5.199' })
  })
  test('small subnet gets the upper half', () => {
    expect(suggestDHCPRange('192.168.1.1/28')).toEqual({ start: '192.168.1.8', end: '192.168.1.14' })
  })
  test('avoids the LAN address', () => {
    expect(suggestDHCPRange('192.168.1.150/24')).toEqual({ start: '192.168.1.1', end: '192.168.1.149' })
  })
  test('invalid input', () => {
    expect(suggestDHCPRange('nope')).toBeNull()
    expect(suggestDHCPRange('10.0.0.1/31')).toBeNull()
  })
})

describe('validateStep', () => {
  test('valid answers pass every step', () => {
    for (const s of ['Welcome', 'Admin password', 'WAN', 'LAN', 'Security', 'Review'] as const) {
      expect(validateStep(s, ans())).toEqual({})
    }
  })
  test('password', () => {
    expect(Object.keys(validateStep('Admin password', ans({ password: 'short', confirm: 'other' }))).sort()).toEqual(['confirm', 'password'])
  })
  test('static WAN', () => {
    const e = validateStep('WAN', ans({ wanMode: 'static', wanAddress: '203.0.113.2/24', wanGateway: '198.51.100.1' }))
    expect(e.wanGateway).toMatch(/WAN subnet/)
    expect(validateStep('WAN', ans({ wanMode: 'static', wanAddress: 'x', wanGateway: '' }))).toHaveProperty('wanAddress')
    expect(validateStep('WAN', ans({ wanDevice: '' }))).toHaveProperty('wanDevice')
  })
  test('LAN', () => {
    expect(validateStep('LAN', ans({ lanDevice: 'eth0' })).lanDevice).toMatch(/different/)
    expect(validateStep('LAN', ans({ lanAddress: '192.168.1.0/24' })).lanAddress).toMatch(/network address/)
    expect(validateStep('LAN', ans({ dhcpEnd: '10.0.0.1' })).dhcpEnd).toMatch(/LAN subnet/)
    expect(validateStep('LAN', ans({ dhcpStart: '192.168.1.1' })).dhcpStart).toMatch(/LAN address/)
    expect(validateStep('LAN', ans({ dhcpStart: '192.168.1.150', dhcpEnd: '192.168.1.120' })).dhcpEnd).toMatch(/before/)
    expect(validateStep('LAN', ans({ dhcp: false, dhcpEnd: 'junk' }))).toEqual({})
    const overlap = validateStep('LAN', ans({ wanMode: 'static', wanAddress: '192.168.0.2/16', wanGateway: '192.168.0.1' }))
    expect(overlap.lanAddress).toMatch(/overlaps/)
  })
})

test('buildRequest', () => {
  expect(buildRequest(ans({ dhcp: false, level: 'strict' }))).toEqual({
    admin_password: 'correct horse battery',
    wan: { device: 'eth0', mode: 'dhcp' },
    lan: { device: 'eth1', address: '192.168.1.1/24' },
    security_level: 'strict',
  })
})

test('stepForPath', () => {
  expect(stepForPath('lan.dhcp_end')).toBe('LAN')
  expect(stepForPath('wan.gateway')).toBe('WAN')
  expect(stepForPath('admin_password')).toBe('Admin password')
  expect(stepForPath('system.hostname')).toBe('Review')
})

describe('withCurrent', () => {
  const devs = ['enp0s3', 'enp0s8']
  test('takes ports and LAN settings the box already runs', () => {
    const a = withCurrent(initialAnswers(), { wan_device: 'enp0s8', lan_device: 'enp0s3', lan_address: '10.10.10.1/24', dhcp_start: '10.10.10.100', dhcp_end: '10.10.10.199' }, devs)
    expect([a.wanDevice, a.lanDevice, a.lanAddress, a.dhcpStart, a.dhcpEnd]).toEqual(['enp0s8', 'enp0s3', '10.10.10.1/24', '10.10.10.100', '10.10.10.199'])
  })
  test('ignores ports that do not exist (fresh install names eth0/eth1)', () => {
    const a = withCurrent(initialAnswers(), { wan_device: 'eth0', lan_device: 'eth1', lan_address: '192.168.1.1/24' }, devs)
    expect([a.wanDevice, a.lanDevice, a.lanAddress]).toEqual(['enp0s3', 'enp0s8', '192.168.1.1/24'])
  })
  test('no state: the defaults and the first two ports', () => {
    const a = withCurrent(initialAnswers(), undefined, devs)
    expect([a.wanDevice, a.lanDevice, a.lanAddress]).toEqual(['enp0s3', 'enp0s8', '192.168.1.1/24'])
  })
})
