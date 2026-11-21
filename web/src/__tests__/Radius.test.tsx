import React from 'react'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import type { Candidate } from '../api/objects'
import type { AuthEvent } from '../api/radius'

vi.mock('../api/auth', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/auth')>()
  return { ...real, getMe: vi.fn().mockResolvedValue({ user: 'admin', role: 'admin', method: 'session', full_admin: true }) }
})
vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn(), patchSection: vi.fn() }
})
vi.mock('../api/radius', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/radius')>()
  return {
    ...real,
    getRadiusLog: vi.fn(),
    getRadiusSessions: vi.fn(),
    disconnectSession: vi.fn(),
    getPPSKs: vi.fn(),
    setPPSK: vi.fn(),
    deletePPSK: vi.fn(),
    issueCertificate: vi.fn(),
    revokeCertificates: vi.fn(),
  }
})

import * as api from '../api/objects'
import * as radius from '../api/radius'
import { getMe } from '../api/auth'
import Radius from '../pages/network/Radius'
import { addressOK, buildRadius, filterEvents, radiusFormOf, validateRadius } from '../pages/network/radiusLogic'

const cfg: Candidate = {
  interfaces: [{ name: 'lan', device: 'eth1', role: 'lan', mode: 'static', address: '192.168.1.1/24', enabled: true }],
  vlans: [
    { name: 'iot', parent: 'lan', vlan_id: 20, mode: 'static', address: '192.168.20.1/24', enabled: true },
    { name: 'quarantine', parent: 'lan', vlan_id: 99, mode: 'static', address: '192.168.99.1/24', enabled: true },
  ],
  internal_ca: { enabled: true },
  radius: {
    enabled: true,
    mab: true,
    unknown: 'quarantine',
    quarantine_vlan: 'quarantine',
    clients: [{ name: 'switch', address: '192.168.1.2', secret: '' }],
    groups: [{ name: 'staff', vlan: 'iot' }],
    users: [{ name: 'alice', enabled: true, password: '', group: 'staff' }],
  },
} as Candidate

const events: AuthEvent[] = [
  { time: '2026-11-20T12:02:00Z', client: 'switch', port: 'Gi1/0/3', mac: 'aabbcc000001', method: 'mab', result: 'accept', vlan: 'iot', vlan_id: 20 },
  { time: '2026-11-20T12:01:00Z', client: 'switch', mac: 'aabbcc0000ff', method: 'mab', result: 'quarantine', vlan: 'quarantine', vlan_id: 99 },
  { time: '2026-11-20T12:00:00Z', client: 'aps', mac: '0200000000ab', user: 'alice', method: 'peap', result: 'reject', reason: 'wrong password' },
]

const lastPatch = () => {
  const calls = vi.mocked(api.patchSection).mock.calls
  return calls[calls.length - 1]
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(getMe).mockResolvedValue({ user: 'admin', role: 'admin', method: 'session', full_admin: true } as never)
  vi.mocked(api.getCandidate).mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.patchSection).mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(radius.getRadiusLog).mockResolvedValue({ events: structuredClone(events) })
  vi.mocked(radius.getRadiusSessions).mockResolvedValue({
    sessions: [{ mac: 'aabbcc000001', method: 'mab', client: 'switch', nas: '192.168.1.2', port: 'Gi1/0/3', vlan: 'iot', started: '2026-11-20T12:02:00Z', updated: '2026-11-20T12:02:00Z' }],
  })
  vi.mocked(radius.getPPSKs).mockResolvedValue({ devices: [{ mac: 'aabbcc000001', psk: 'tv-wifi-pass', set: '' }] })
  vi.mocked(radius.setPPSK).mockResolvedValue({ status: 'ok' })
  vi.mocked(radius.disconnectSession).mockResolvedValue({ mac: 'aabbcc000001', client: 'switch', nas: '192.168.1.2', result: 'acked' })
})

describe('Network → RADIUS', () => {
  it('saves the settings, keeping stored secrets and adding a user', async () => {
    render(<Radius />)
    const clients = await screen.findByRole('table', { name: 'Switches and access points' })
    expect(within(clients).getByLabelText('Shared secret')).toHaveAttribute('placeholder', 'unchanged')
    fireEvent.click(screen.getByLabelText('802.1X (user name and password, or a certificate)'))
    fireEvent.click(screen.getByRole('button', { name: 'Add a user' }))
    const users = screen.getByRole('table', { name: 'Users (802.1X)' })
    const rows = within(users).getAllByRole('row')
    const row = rows[rows.length - 1]
    fireEvent.change(within(row).getByLabelText('User'), { target: { value: 'bob' } })
    fireEvent.change(within(row).getByLabelText('Password'), { target: { value: 'bob-password' } })
    fireEvent.change(within(row).getByLabelText('Group'), { target: { value: 'staff' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect(lastPatch()).toEqual([
      'radius',
      {
        enabled: true,
        mab: true,
        dot1x: true,
        unknown: 'quarantine',
        quarantine_vlan: 'quarantine',
        clients: [{ name: 'switch', address: '192.168.1.2' }],
        groups: [{ name: 'staff', vlan: 'iot' }],
        users: [
          { name: 'alice', enabled: true, group: 'staff' },
          { name: 'bob', enabled: true, password: 'bob-password', group: 'staff' },
        ],
      },
    ])
    expect(await screen.findByText('Saved. Commit to apply.')).toBeInTheDocument()
  })

  it('checks the form before saving', async () => {
    render(<Radius />)
    await screen.findByRole('table', { name: 'Switches and access points' })
    fireEvent.click(screen.getByRole('button', { name: 'Add a switch or access point' }))
    const rows = within(screen.getByRole('table', { name: 'Switches and access points' })).getAllByRole('row')
    const row = rows[rows.length - 1]
    fireEvent.change(within(row).getByLabelText('Name'), { target: { value: 'ap' } })
    fireEvent.change(within(row).getByLabelText('Address'), { target: { value: 'not-an-ip' } })
    fireEvent.change(within(row).getByLabelText('Shared secret'), { target: { value: 'has space' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Want an IP address or a subnet, e.g. 192.168.1.2 or 192.168.10.0/24.')).toBeInTheDocument()
    expect(screen.getByText('Want a shared secret of at least 8 characters, without spaces, quotes or backslashes.')).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  it('shows server validation errors next to the field', async () => {
    vi.mocked(api.patchSection).mockResolvedValue({ status: 'ok', validation_errors: [{ path: 'radius.clients[0].address', message: 'overlaps the WAN' }] })
    render(<Radius />)
    await screen.findByRole('table', { name: 'Switches and access points' })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('overlaps the WAN')).toBeInTheDocument()
    expect(screen.queryByText('Saved. Commit to apply.')).not.toBeInTheDocument()
  })

  it('is read-only for a role without write:network', async () => {
    vi.mocked(getMe).mockResolvedValue({ user: 'viewer', role: 'read_only', method: 'session', full_admin: false, permissions: ['read'] } as never)
    render(<Radius />)
    await screen.findByText('Your role can view the RADIUS settings but not change them.')
    expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Wi-Fi passwords per device' })).not.toBeInTheDocument()
    expect(radius.getPPSKs).not.toHaveBeenCalled()
  })

  it('lists and filters the recent authentications', async () => {
    render(<Radius />)
    const table = await screen.findByRole('table', { name: 'Recent authentications' })
    await waitFor(() => expect(within(table).getAllByRole('row')).toHaveLength(4))
    expect(within(table).getByText('switch · Gi1/0/3')).toBeInTheDocument()
    expect(within(table).getByText('wrong password')).toBeInTheDocument()
    expect(within(table).getByText('quarantine (99)')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Result'), { target: { value: 'reject' } })
    expect(within(table).getAllByRole('row')).toHaveLength(2)
    expect(within(table).getByText('alice')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Result'), { target: { value: '' } })
    fireEvent.change(screen.getByLabelText('Filter the authentications'), { target: { value: 'AA:BB:CC:00:00:FF' } })
    expect(within(table).getAllByRole('row')).toHaveLength(2)
    expect(within(table).getByText('aa:bb:cc:00:00:ff')).toBeInTheDocument()
  })

  it('disconnects a session', async () => {
    render(<Radius />)
    const btn = await screen.findByRole('button', { name: 'Disconnect aa:bb:cc:00:00:01' })
    fireEvent.click(btn)
    await waitFor(() => expect(radius.disconnectSession).toHaveBeenCalledWith('aabbcc000001'))
    expect(await screen.findByText('aa:bb:cc:00:00:01 was disconnected; it reconnects on its own.')).toBeInTheDocument()
  })

  it('issues a certificate and shows the downloads once', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue({ ...structuredClone(cfg), radius: { ...structuredClone(cfg.radius!), dot1x: true } })
    vi.mocked(radius.issueCertificate).mockResolvedValue({
      user: 'alice', serial: '0a', not_after: '2027-11-20T00:00:00Z', certificate: 'CERT', key: 'KEY', ca: 'CA', ca_sha256: 'abc123',
    })
    render(<Radius />)
    fireEvent.click(await screen.findByRole('button', { name: 'Certificate' }))
    await waitFor(() => expect(radius.issueCertificate).toHaveBeenCalledWith('alice', 365))
    expect(await screen.findByText('Save the key now: the gate does not keep it, and this is the only time it is shown.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Download the key' })).toHaveAttribute('download', 'alice.key')
    expect(screen.getByText('CA root SHA-256: abc123')).toBeInTheDocument()
  })

  it('sets a per-device Wi-Fi password', async () => {
    render(<Radius />)
    const table = await screen.findByRole('table', { name: 'Wi-Fi passwords per device' })
    expect(within(table).getByText('••••••••')).toBeInTheDocument()
    fireEvent.click(within(table).getByRole('button', { name: 'Show' }))
    expect(within(table).getByText('tv-wifi-pass')).toBeInTheDocument()
    fireEvent.change(screen.getByPlaceholderText('aa:bb:cc:dd:ee:ff'), { target: { value: 'aa:bb:cc:00:00:02' } })
    const inputs = screen.getAllByLabelText('Wi-Fi password')
    fireEvent.change(inputs[inputs.length - 1], { target: { value: 'short' } })
    fireEvent.click(screen.getByRole('button', { name: 'Set' }))
    expect(await screen.findByText('Want a Wi-Fi password of 8-63 characters.')).toBeInTheDocument()
    fireEvent.change(inputs[inputs.length - 1], { target: { value: 'printer-pass' } })
    fireEvent.click(screen.getByRole('button', { name: 'Set' }))
    await waitFor(() => expect(radius.setPPSK).toHaveBeenCalledWith('aa:bb:cc:00:00:02', 'printer-pass'))
  })
})

describe('radiusLogic', () => {
  it('validates like the server', () => {
    const f = radiusFormOf(cfg.radius)
    expect(validateRadius(f, ['iot', 'quarantine'], true)).toEqual({})
    f.dot1x = true
    expect(validateRadius(f, ['iot', 'quarantine'], false)['radius.dot1x']).toBeTruthy()
    f.users.push({ name: 'ALICE', enabled: true, password: '', group: 'nope', stored: false })
    const e = validateRadius(f, ['iot', 'quarantine'], true)
    expect(Object.keys(e).sort()).toEqual(['radius.users[1].group', 'radius.users[1].name', 'radius.users[1].password'])
  })

  it('builds the section without defaults', () => {
    const f = radiusFormOf(undefined)
    expect(buildRadius(f)).toEqual({ enabled: false })
    f.unknown = 'accept'
    f.quarantineVlan = 'quarantine' // ignored unless quarantine is picked
    expect(buildRadius(f)).toEqual({ enabled: false, unknown: 'accept' })
  })

  it('accepts addresses and subnets', () => {
    for (const ok of ['192.168.1.2', '10.0.0.0/8', 'fd00::2', 'fd00::/64']) expect(addressOK(ok)).toBe(true)
    for (const bad of ['', 'switch.lan', '192.168.1.300', '10.0.0.0/33', '1.2.3.4/8/9', 'fd00::/129']) expect(addressOK(bad)).toBe(false)
  })

  it('filters by MAC in any notation and by result', () => {
    expect(filterEvents(events, 'aa-bb-cc-00-00-01', '')).toHaveLength(1)
    expect(filterEvents(events, '', 'quarantine')).toHaveLength(1)
    expect(filterEvents(events, 'password', '')).toHaveLength(1)
    expect(filterEvents(events, '', 'disconnect')).toHaveLength(0)
  })
})
