import { describe, expect, test } from 'vitest'
import type { DHCPServer, PortForward } from '../api/network'
import { cleanDHCP, inSubnet, ipv4, portOrRange, validateDHCP, validateDNS, validateLogExport, validatePortForward } from '../pages/network/networkLogic'

describe('address helpers', () => {
  test('ipv4', () => {
    expect(ipv4('192.168.1.1')).toBe(0xc0a80101)
    expect(ipv4('256.1.1.1')).toBeNull()
    expect(ipv4('01.2.3.4')).toBeNull()
    expect(ipv4('1.2.3')).toBeNull()
  })
  test('inSubnet', () => {
    expect(inSubnet('192.168.1.200', '192.168.1.1/24')).toBe(true)
    expect(inSubnet('192.168.2.1', '192.168.1.1/24')).toBe(false)
    expect(inSubnet('10.0.0.1', '0.0.0.0/0')).toBe(true)
  })
  test('portOrRange', () => {
    expect(portOrRange('443')).toEqual([443, 443])
    expect(portOrRange('8000-8100')).toEqual([8000, 8100])
    expect(portOrRange('0')).toBeNull()
    expect(portOrRange('9-8')).toBeNull()
    expect(portOrRange('70000')).toBeNull()
  })
})

const pf = (x: Partial<PortForward> = {}): PortForward => ({
  name: 'web', in_intf: 'wan1', ext_port: '443', protocol: 'tcp', to_addr: '192.168.1.10', to_port: '', ...x,
})

describe('validatePortForward', () => {
  test('accepts a valid forward', () => {
    expect(validatePortForward(pf(), ['wan1'], [])).toEqual({})
    expect(validatePortForward(pf({ ext_port: '8000-8100', to_port: '8000-8100' }), ['wan1'], [])).toEqual({})
    expect(validatePortForward(pf({ ext_port: '8000-8100', to_port: '80' }), ['wan1'], [])).toEqual({})
  })
  test('rejects bad fields like the server', () => {
    const e = validatePortForward(pf({ name: 'a b', in_intf: 'lan', ext_port: 'x', to_addr: '::1', to_port: 'abc' }), ['wan1'], [])
    expect(Object.keys(e).sort()).toEqual(['ext_port', 'in_intf', 'name', 'to_addr', 'to_port'])
    expect(validatePortForward(pf({ ext_port: '8000-8100', to_port: '9000-9100' }), ['wan1'], []).to_port).toMatch(/must equal/)
    expect(validatePortForward(pf(), ['wan1'], [pf()]).name).toMatch(/already used/)
  })
})

const dhcp = (x: Partial<DHCPServer> = {}): DHCPServer => ({
  interface: 'lan', range_start: '192.168.1.100', range_end: '192.168.1.199', ...x,
})

describe('validateDHCP', () => {
  test('accepts a valid server', () => {
    expect(validateDHCP(dhcp({ static_leases: [{ mac: 'AA:bb:cc:dd:ee:01', ip: '192.168.1.10', hostname: 'nas' }] }), '192.168.1.1/24', [])).toEqual({})
  })
  test('range checks', () => {
    expect(validateDHCP(dhcp({ range_end: '192.168.1.50' }), '192.168.1.1/24', []).range_end).toMatch(/before start/)
    expect(validateDHCP(dhcp({ range_start: '10.0.0.1' }), '192.168.1.1/24', []).range_start).toMatch(/Outside/)
    expect(validateDHCP(dhcp(), '192.168.1.1/24', [dhcp()]).interface).toMatch(/already has/)
  })
  test('static lease checks', () => {
    const e = validateDHCP(dhcp({ static_leases: [{ mac: 'aa:bb:cc:dd:ee:01', ip: '192.168.1.10' }, { mac: 'AA:BB:CC:DD:EE:01', ip: '192.168.1.11' }, { mac: 'nope', ip: '1.1.1.1' }] }), '192.168.1.1/24', [])
    expect(e['static_leases.1']).toMatch(/duplicate MAC/)
    expect(e['static_leases.2']).toMatch(/MAC/)
  })
  test('cleanDHCP drops empty optionals', () => {
    expect(cleanDHCP(dhcp({ lease_seconds: 0, gateway: ' ', dns: [], static_leases: [{ mac: 'aa:bb:cc:dd:ee:01', ip: '192.168.1.10', hostname: '' }] }))).toEqual(
      dhcp({ static_leases: [{ mac: 'aa:bb:cc:dd:ee:01', ip: '192.168.1.10' }] })
    )
  })
})

test('validateDNS', () => {
  expect(validateDNS({ forwarders: ['1.1.1.1'], local_records: { 'nas.home.arpa': '192.168.1.5' } })).toEqual({})
  const e = validateDNS({ forwarders: ['dns.google'], local_records: { 'bad name': '1.2.3.4', ok: 'x' } })
  expect(e.forwarders).toMatch(/dns.google/)
  expect(e['record.bad name']).toMatch(/invalid host/)
  expect(e['record.ok']).toMatch(/IPv4/)
})

describe('validateLogExport', () => {
  const syslog = (x: Partial<Parameters<typeof validateLogExport>[0]> = {}) =>
    validateLogExport({ name: 'rsyslog', type: 'syslog', address: '10.0.0.2:514', protocol: 'udp', ...x }, [])
  const loki = (x: Partial<Parameters<typeof validateLogExport>[0]> = {}) =>
    validateLogExport({ name: 'grafana', type: 'loki', url: 'https://loki.example.com/loki/api/v1/push', ...x }, [])

  test('accepts valid syslog and loki entries', () => {
    expect(syslog()).toEqual({})
    expect(syslog({ protocol: 'tls', address: '[2001:db8::1]:6514' })).toEqual({})
    expect(loki()).toEqual({})
    expect(loki({ kinds: ['traffic', 'ips'] })).toEqual({})
  })

  test('name rules', () => {
    expect(syslog({ name: 'bad name' }).name).toMatch(/Name/)
    expect(syslog({ name: '' }).name).toBeDefined()
    expect(validateLogExport({ name: 'rsyslog', type: 'syslog', address: '1.2.3.4:514' }, [{ name: 'rsyslog' }]).name).toMatch(/already used/)
  })

  test('type and kinds', () => {
    expect(syslog({ type: 'splunk' as never }).type).toMatch(/syslog or loki/)
    expect(loki({ kinds: ['bogus'] }).kinds).toMatch(/Unknown kind bogus/)
  })

  test('syslog address and protocol', () => {
    expect(syslog({ address: '' }).address).toMatch(/required/)
    expect(syslog({ address: '10.0.0.2' }).address).toMatch(/host:port/)
    expect(syslog({ address: '10.0.0.2:99999' }).address).toMatch(/host:port/)
    expect(syslog({ protocol: 'sctp' as never }).protocol).toMatch(/udp, tcp or tls/)
  })

  test('loki url', () => {
    expect(loki({ url: '' }).url).toMatch(/required/)
    expect(loki({ url: 'ftp://x/loki/api/v1/push' }).url).toMatch(/https?:\/\//)
    expect(loki({ url: 'https://x/other' }).url).toMatch(/\/loki\/api\/v1\/push/)
  })
})
