import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Candidate } from '../api/objects'

// Mock the API modules: no network. ConfigList calls getCandidate + patchSection.
vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn(), patchSection: vi.fn() }
})
vi.mock('../api/gateways', () => ({
  getGatewayStatus: vi.fn(),
  getWireGuardClientPublicKey: vi.fn(),
  setWireGuardClientPrivateKey: vi.fn(),
}))
import * as api from '../api/objects'
import * as gatewaysApi from '../api/gateways'
import Gateways from '../pages/network/Gateways'
import { validateGatewayGroup, validateRoute } from '../pages/network/networkLogic'

const cfg: Candidate = {
  interfaces: [
    { name: 'wan1', device: 'eth0', role: 'wan', enabled: true },
    { name: 'wan2', device: 'eth1', role: 'wan', enabled: true },
  ] as unknown as Candidate['interfaces'],
  gateways: [
    { name: 'isp1', interface: 'wan1', address: 'dhcp' },
    { name: 'isp2', interface: 'wan2', address: 'dhcp' },
  ],
  gateway_groups: [{ name: 'failover', members: [{ gateway: 'isp1', tier: 1 }, { gateway: 'isp2', tier: 2 }] }],
}

const renderPage = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Gateways />
    </QueryClientProvider>,
  )

beforeEach(() => {
  vi.mocked(api.getCandidate).mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(gatewaysApi.getGatewayStatus).mockResolvedValue({
    enabled: true,
    gateways: [
      { name: 'isp1', interface: 'wan1', device: 'eth0', state: 'down', loss_percent: 100, latency_ms: 0, reason: '100% packet loss' },
      { name: 'isp2', interface: 'wan2', device: 'eth1', state: 'up', loss_percent: 0, latency_ms: 9 },
    ],
    targets: [{ name: 'failover', group: true, active: ['isp2'], mode: 'gateways' }],
    default: { name: 'default', active: ['isp2'], mode: 'gateways' },
  })
})

describe('gateway validation', () => {
  test('group members need a known gateway, tier 1-5 and weight 1-100', () => {
    expect(validateGatewayGroup({ name: 'g', members: [] }, cfg).members).toBeTruthy()
    expect(validateGatewayGroup({ name: 'g', members: [{ gateway: 'isp1', tier: 1, weight: 100 }] }, cfg)).toEqual({})
    expect(validateGatewayGroup({ name: 'g', members: [{ gateway: 'isp1', tier: 0 }] }, cfg)['members.0']).toBeTruthy()
    expect(validateGatewayGroup({ name: 'g', members: [{ gateway: 'isp1', weight: 101 }] }, cfg)['members.0']).toBeTruthy()
    expect(validateGatewayGroup({ name: 'g', members: [{ gateway: 'nope' }] }, cfg)['members.0.gateway']).toBeTruthy()
    const dup = validateGatewayGroup({ name: 'g', members: [{ gateway: 'isp1' }, { gateway: 'isp1' }] }, cfg)
    expect(dup['members.1.gateway']).toMatch(/twice/)
  })

  test('static routes need an IPv4 subnet and a gateway, not a group', () => {
    expect(validateRoute({ destination: '192.168.2.0/24', gateway: 'isp1' }, cfg)).toEqual({})
    expect(validateRoute({ destination: '2001:db8::/64', gateway: 'isp1' }, cfg).destination).toBeTruthy()
    expect(validateRoute({ destination: '10.0.0.0/8', gateway: 'failover' }, cfg).gateway).toBeTruthy()
  })
})

describe('Gateways page', () => {
  test('shows status badges, the reason and the active routing targets', async () => {
    renderPage()
    expect(await screen.findByText('100% packet loss')).toBeInTheDocument()
    expect(screen.getByText('down')).toBeInTheDocument()
    expect(screen.getByText('up')).toBeInTheDocument()
    const targets = screen.getByRole('list', { name: 'Routing targets' })
    expect(targets).toHaveTextContent('Default route: isp2 · gateways')
    expect(targets).toHaveTextContent('Group failover: isp2 · gateways')
  })

  test('changing the default route PATCHes default_gateway', async () => {
    renderPage()
    const sel = (await screen.findByLabelText('Default route')) as HTMLSelectElement
    expect(Array.from(sel.options).map((o) => o.value)).toEqual(['', 'isp1', 'isp2', 'failover'])
    fireEvent.change(sel, { target: { value: 'failover' } })
    await waitFor(() => expect(api.patchSection).toHaveBeenCalledWith('default_gateway', 'failover'))
  })
})
