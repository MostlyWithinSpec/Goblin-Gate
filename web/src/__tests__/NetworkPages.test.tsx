import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import type { NetworkConfig } from '../api/network'

// Mock the API module: no network. The pure helpers stay real.
vi.mock('../api/network', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/network')>()
  return { ...real, getNetworkConfig: vi.fn(), patchSection: vi.fn(), getLeases: vi.fn() }
})
import * as api from '../api/network'
import PortForwards from '../pages/network/PortForwards'
import DHCP from '../pages/network/DHCP'
import DNS from '../pages/network/DNS'

const cfg: NetworkConfig = {
  interfaces: [
    { name: 'wan1', role: 'wan', mode: 'dhcp', enabled: true },
    { name: 'lan', role: 'lan', mode: 'static', address: '192.168.1.1/24', enabled: true },
  ],
  vlans: [
    { name: 'guest', parent: 'lan', mode: 'static', address: '192.168.20.1/24', enabled: true },
    { name: 'isp', parent: 'wan1', mode: 'dhcp', enabled: true },
  ],
  nat: { port_forwards: [{ name: 'nas-https', in_intf: 'wan1', ext_port: '8443', protocol: 'tcp', to_addr: '192.168.1.5', to_port: '443' }] },
  dhcp: [{ interface: 'lan', range_start: '192.168.1.100', range_end: '192.168.1.199', static_leases: [{ mac: 'aa:bb:cc:dd:ee:01', ip: '192.168.1.10', hostname: 'printer' }] }],
  dns: { forwarders: ['1.1.1.1'], dnssec: true, local_records: { 'nas.home.arpa': '192.168.1.5' } },
}

const lastPatch = () => {
  const calls = vi.mocked(api.patchSection).mock.calls
  return calls[calls.length - 1]
}

beforeEach(() => {
  vi.mocked(api.getNetworkConfig).mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(api.getLeases).mockResolvedValue(null)
})

describe('PortForwards', () => {
  test('renders the table', async () => {
    render(<PortForwards />)
    expect(await screen.findByRole('button', { name: 'nas-https' })).toBeInTheDocument()
    expect(screen.getByText('192.168.1.5:443')).toBeInTheDocument()
  })

  test('offers only wan interfaces and validates the form', async () => {
    render(<PortForwards />)
    await screen.findByRole('button', { name: 'nas-https' })
    fireEvent.click(screen.getByRole('button', { name: /New port forward/ }))
    const opts = within(screen.getByLabelText('Incoming interface')).getAllByRole('option').map((o) => o.textContent)
    expect(opts).toEqual(['wan1', 'isp'])
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'nas-https' } })
    fireEvent.change(screen.getByLabelText('External port'), { target: { value: '99999' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Name is already used')).toBeInTheDocument()
    expect(screen.getByText(/Port 1-65535/)).toBeInTheDocument()
    expect(screen.getByText('Internal address must be IPv4')).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('saves a new forward to nat.port_forwards', async () => {
    render(<PortForwards />)
    await screen.findByRole('button', { name: 'nas-https' })
    fireEvent.click(screen.getByRole('button', { name: /New port forward/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'game' } })
    fireEvent.change(screen.getByLabelText('External port'), { target: { value: '27015' } })
    fireEvent.change(screen.getByLabelText('Protocol'), { target: { value: 'udp' } })
    fireEvent.change(screen.getByLabelText('Internal address'), { target: { value: '192.168.1.50' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, value] = lastPatch()
    expect(path).toBe('nat.port_forwards')
    expect(value).toEqual([cfg.nat!.port_forwards![0], { name: 'game', in_intf: 'wan1', ext_port: '27015', protocol: 'udp', to_addr: '192.168.1.50', to_port: '' }])
  })
})

describe('DHCP', () => {
  test('renders servers and says leases are not available yet', async () => {
    render(<DHCP />)
    expect(await screen.findByRole('button', { name: 'lan' })).toBeInTheDocument()
    expect(screen.getByText('192.168.1.100 – 192.168.1.199')).toBeInTheDocument()
    expect(await screen.findByText('Lease view not available yet.')).toBeInTheDocument()
  })

  test('shows active leases when the endpoint exists', async () => {
    vi.mocked(api.getLeases).mockResolvedValue([{ ip: '192.168.1.123', mac: 'aa:bb:cc:00:00:01', hostname: 'phone' }])
    render(<DHCP />)
    expect(await screen.findByText('192.168.1.123')).toBeInTheDocument()
  })

  test('validates range and static leases against the interface subnet', async () => {
    render(<DHCP />)
    fireEvent.click(await screen.findByRole('button', { name: 'lan' }))
    fireEvent.change(screen.getByLabelText('Range end'), { target: { value: '192.168.2.10' } })
    fireEvent.click(screen.getByText('+ Add static lease'))
    fireEvent.change(screen.getByLabelText('Lease 2 MAC'), { target: { value: 'aa:bb:cc:dd:ee:01' } })
    fireEvent.change(screen.getByLabelText('Lease 2 IP'), { target: { value: '192.168.1.11' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Outside 192.168.1.1/24')).toBeInTheDocument()
    expect(screen.getByText('Lease 2: duplicate MAC')).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('adds a server on a VLAN and saves the dhcp list', async () => {
    render(<DHCP />)
    await screen.findByRole('button', { name: 'lan' })
    fireEvent.click(screen.getByRole('button', { name: /New DHCP server/ }))
    expect(screen.getByLabelText('Interface')).toHaveValue('guest')
    fireEvent.change(screen.getByLabelText('Range start'), { target: { value: '192.168.20.100' } })
    fireEvent.change(screen.getByLabelText('Range end'), { target: { value: '192.168.20.150' } })
    fireEvent.change(screen.getByLabelText('DNS servers'), { target: { value: '1.1.1.1, 9.9.9.9' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, value] = lastPatch()
    expect(path).toBe('dhcp')
    expect((value as unknown[])[1]).toEqual({ interface: 'guest', range_start: '192.168.20.100', range_end: '192.168.20.150', dns: ['1.1.1.1', '9.9.9.9'] })
  })
})

describe('DNS', () => {
  test('loads settings and saves them', async () => {
    render(<DNS />)
    expect(await screen.findByDisplayValue('1.1.1.1')).toBeInTheDocument()
    expect(screen.getByDisplayValue('nas.home.arpa')).toBeInTheDocument()
    fireEvent.click(screen.getByText('+ Add record'))
    fireEvent.change(screen.getByLabelText('Record 2 name'), { target: { value: 'printer.home.arpa' } })
    fireEvent.change(screen.getByLabelText('Record 2 address'), { target: { value: '192.168.1.10' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect(lastPatch()).toEqual([
      'dns',
      { forwarders: ['1.1.1.1'], dnssec: true, local_records: { 'nas.home.arpa': '192.168.1.5', 'printer.home.arpa': '192.168.1.10' } },
    ])
  })

  test('rejects a non-IPv4 forwarder', async () => {
    render(<DNS />)
    fireEvent.change(await screen.findByLabelText('Forwarders'), { target: { value: 'dns.google' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Not IPv4: dns.google')).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('turns off DNS rebinding protection with the checkbox', async () => {
    render(<DNS />)
    await screen.findByDisplayValue('1.1.1.1')
    fireEvent.click(screen.getByLabelText('Allow public names to answer with private addresses (turns off DNS rebinding protection)'))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect(lastPatch()).toEqual([
      'dns',
      { forwarders: ['1.1.1.1'], dnssec: true, allow_private_answers: true, local_records: { 'nas.home.arpa': '192.168.1.5' } },
    ])
  })
})
