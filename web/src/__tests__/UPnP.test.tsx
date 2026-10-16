import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import type { NetworkConfig } from '../api/network'

// Mock the network API module: no network. The pure helpers stay real.
vi.mock('../api/network', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/network')>()
  return {
    ...real,
    getNetworkConfig: vi.fn(),
    patchSection: vi.fn(),
    getUpnpMappings: vi.fn(),
  }
})
import * as api from '../api/network'
import UPnP from '../pages/network/UPnP'

const cfg: NetworkConfig = {
  interfaces: [
    { name: 'wan1', role: 'wan', mode: 'dhcp', enabled: true },
    { name: 'lan', role: 'lan', mode: 'static', address: '192.168.1.1/24', enabled: true },
  ],
  vlans: [{ name: 'guest', parent: 'lan', mode: 'static', address: '192.168.20.1/24', enabled: true }],
}

const lastPatch = () => {
  const calls = vi.mocked(api.patchSection).mock.calls
  return calls[calls.length - 1]
}
const wrap = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>)

beforeEach(() => {
  vi.mocked(api.getNetworkConfig).mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(api.getUpnpMappings).mockResolvedValue({ enabled: true, mappings: [] })
})

describe('UPnP page', () => {
  test('enabling and saving sends the upnp section', async () => {
    wrap(<UPnP />)
    await screen.findByText('Enable UPnP')

    fireEvent.click(screen.getByLabelText('Enable UPnP'))
    fireEvent.click(screen.getByLabelText('lan'))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(await vi.mocked(api.patchSection).mock.calls[0]).toEqual(['upnp', { enabled: true, interfaces: ['lan'] }])
  })

  test('keeps STUN and shows NAT-PMP on by default', async () => {
    vi.mocked(api.getNetworkConfig).mockResolvedValue({ ...structuredClone(cfg), upnp: { enabled: true, interfaces: ['lan'], stun: true } })
    wrap(<UPnP />)
    await screen.findByText('Enable UPnP')
    expect(screen.getByLabelText('NAT-PMP')).toBeChecked()
    expect(screen.getByLabelText('STUN (behind another router)')).toBeChecked()
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect(lastPatch()).toEqual(['upnp', { enabled: true, interfaces: ['lan'], stun: true }])
  })

  test('saves with secure mode and an allow entry when set', async () => {
    wrap(<UPnP />)
    await screen.findByText('Enable UPnP')

    fireEvent.click(screen.getByLabelText('Enable UPnP'))
    fireEvent.click(screen.getByLabelText('lan'))
    // Toggle secure mode off then on so the state value is defined.
    fireEvent.click(screen.getByLabelText('Secure mode'))
    fireEvent.click(screen.getByLabelText('Secure mode'))
    fireEvent.click(screen.getByRole('button', { name: 'Add allow entry' }))
    fireEvent.change(screen.getByLabelText('Allow 1 address'), { target: { value: '192.168.1.0/24' } })
    fireEvent.change(screen.getByLabelText('Allow 1 ports'), { target: { value: '80-443' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(lastPatch()).toEqual([
      'upnp',
      { enabled: true, interfaces: ['lan'], secure_mode: true, allow: [{ address: '192.168.1.0/24', ports: '80-443' }] },
    ])
  })

  test('rejects saving with no interface selected', async () => {
    wrap(<UPnP />)
    await screen.findByText('Enable UPnP')

    fireEvent.click(screen.getByLabelText('Enable UPnP'))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('Select at least one interface when UPnP is enabled.')).toBeTruthy()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('shows an invalid port range error and does not save', async () => {
    wrap(<UPnP />)
    await screen.findByText('Enable UPnP')

    fireEvent.click(screen.getByLabelText('Enable UPnP'))
    fireEvent.click(screen.getByLabelText('lan'))
    fireEvent.click(screen.getByRole('button', { name: 'Add allow entry' }))
    fireEvent.change(screen.getByLabelText('Allow 1 ports'), { target: { value: '90000' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('Invalid port range (use 1-65535, e.g. 80 or 1000-2000.)')).toBeTruthy()

    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('shows a required port range error when the field is empty', async () => {
    wrap(<UPnP />)
    await screen.findByText('Enable UPnP')

    fireEvent.click(screen.getByLabelText('Enable UPnP'))
    fireEvent.click(screen.getByLabelText('lan'))
    fireEvent.click(screen.getByRole('button', { name: 'Add allow entry' }))
    fireEvent.change(screen.getByLabelText('Allow 1 address'), { target: { value: '192.168.1.50' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(await screen.findByText('Port range is required.')).toBeTruthy()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('renders the active mappings table', async () => {
    vi.mocked(api.getUpnpMappings).mockResolvedValue({
      enabled: true,
      mappings: [
        { protocol: 'tcp', external_port: 3000, internal_ip: '192.168.1.50', internal_port: 3000, description: 'game', expires: new Date(Date.now() + 7200_000).toISOString() },
      ],
    })

    wrap(<UPnP />)
    await screen.findByText('Active mappings')

    expect(screen.getByText('192.168.1.50')).toBeTruthy()
    expect(screen.getByText('TCP')).toBeTruthy()
    expect(screen.getByText('3000')).toBeTruthy()
    expect(screen.getByText('192.168.1.50:3000')).toBeTruthy()
    expect(screen.getByText('game')).toBeTruthy()
  })

  test('shows the disabled state when there are no mappings', async () => {
    vi.mocked(api.getUpnpMappings).mockResolvedValue({ enabled: false, mappings: [] })

    wrap(<UPnP />)
    await screen.findByText('Active mappings')

    expect(await screen.findByText('UPnP is disabled.')).toBeTruthy()
  })
})
