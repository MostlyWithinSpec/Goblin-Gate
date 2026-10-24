import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import type { Policy, PolicyConfig, SimulateResult } from '../api/policy'

// Mock the API module: no network. objectsOf stays real.
vi.mock('../api/policy', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/policy')>()
  return {
    ...real,
    getCandidate: vi.fn(),
    getPolicyHits: vi.fn(),
    savePolicies: vi.fn(),
    simulatePolicy: vi.fn(),
  }
})
import * as api from '../api/policy'
import Policies from '../pages/policy/Policies'

const policies: Policy[] = [
  { id: 1, name: 'lan-to-internet', enabled: true, src_intf: ['lan'], dst_intf: ['wan1'], src_addr: ['all'], dst_addr: ['all'], service: ['any'], action: 'accept', nat: true, ips_profile: 'basic', schedule: 'business-hours' },
  { id: 2, name: 'block-guest', enabled: false, src_intf: ['guest'], dst_intf: ['lan'], src_addr: ['all'], dst_addr: ['all'], service: ['any'], action: 'deny', log: 'all' },
]

const cfg: PolicyConfig = {
  interfaces: [{ name: 'wan1' }, { name: 'lan' }],
  vlans: [{ name: 'guest' }],
  addresses: [{ name: 'nas' }],
  services: [{ name: 'web' }],
  policies,
  profiles: { ips: [{ name: 'basic' }] },
  feeds: [{ name: 'drop' }],
  schedules: [{ name: 'business-hours' }],
  gateways: [{ name: 'wan1' }, { name: 'vpn' }],
  gateway_groups: [{ name: 'failover' }],
}

const saved = () => {
  const calls = vi.mocked(api.savePolicies).mock.calls
  return calls[calls.length - 1][0]
}

// Wrap in MemoryRouter so useLocation() works in tests.
const renderWithRouter = (url = '/policy') =>
  render(
    <MemoryRouter initialEntries={[url]}>
      <Policies />
    </MemoryRouter>,
  )

beforeEach(() => {
  vi.mocked(api.getCandidate).mockReset().mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.getPolicyHits).mockReset().mockResolvedValue({ 1: 1234 })
  vi.mocked(api.savePolicies).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(api.simulatePolicy).mockReset()
})

describe('Policies table', () => {
  test('renders policies in order with action, NAT, badges and hit counts', async () => {
    renderWithRouter()
    const row1 = await screen.findByTestId('policy-row-1')
    const row2 = screen.getByTestId('policy-row-2')
    expect(row1.compareDocumentPosition(row2) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(within(row1).getByText('ACCEPT')).toBeInTheDocument()
    expect(within(row1).getByText('NAT')).toBeInTheDocument()
    expect(within(row1).getByText('IPS')).toBeInTheDocument()
    expect(within(row1).getByText('business-hours')).toBeInTheDocument()
    expect(within(row1).getByText('1,234')).toBeInTheDocument()
    expect(within(row2).getByText('DENY')).toBeInTheDocument()
    expect(within(row2).getByText('—')).toBeInTheDocument()
    expect(within(row2).getByLabelText('Enable policy 2')).not.toBeChecked()
  })

  test('groups by interface pair', async () => {
    renderWithRouter()
    await screen.findByTestId('policy-row-1')
    fireEvent.click(screen.getByLabelText('Group by interface pair'))
    expect(screen.getByText('lan → wan1')).toBeInTheDocument()
    expect(screen.getByText('guest → lan')).toBeInTheDocument()
  })

  test('enable toggle saves the list', async () => {
    renderWithRouter()
    fireEvent.click(await screen.findByLabelText('Enable policy 2'))
    await waitFor(() => expect(api.savePolicies).toHaveBeenCalled())
    expect(saved().find((p) => p.id === 2)?.enabled).toBe(true)
  })

  test('kebab menu clones below the policy', async () => {
    renderWithRouter()
    fireEvent.click(await screen.findByLabelText('Actions for policy 1'))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Clone' }))
    await waitFor(() => expect(api.savePolicies).toHaveBeenCalled())
    expect(saved().map((p) => p.id)).toEqual([1, 3, 2])
    expect(saved()[1].name).toBe('lan-to-internet-copy')
  })

  test('a failed save keeps the old list and shows the error', async () => {
    vi.mocked(api.savePolicies).mockRejectedValue(new Error('server down'))
    renderWithRouter()
    fireEvent.click(await screen.findByLabelText('Enable policy 2'))
    expect(await screen.findByRole('alert')).toHaveTextContent('server down')
    expect(screen.getByLabelText('Enable policy 2')).not.toBeChecked()
  })

  test('shows server validation errors for policies', async () => {
    vi.mocked(api.savePolicies).mockResolvedValue({
      status: 'ok',
      validation_errors: [{ path: 'policies[id=2].src_intf', message: 'unknown interface' }],
    })
    renderWithRouter()
    fireEvent.click(await screen.findByLabelText('Enable policy 2'))
    expect(await screen.findByText(/unknown interface/)).toBeInTheDocument()
  })
})

describe('Policy editor', () => {
  test('blocks saving an incomplete policy and shows field errors', async () => {
    renderWithRouter()
    await screen.findByTestId('policy-row-1')
    fireEvent.click(screen.getByRole('button', { name: /New policy/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByText('Name is required')).toBeInTheDocument()
    expect(screen.getByText('Pick at least one incoming interface')).toBeInTheDocument()
    expect(api.savePolicies).not.toHaveBeenCalled()
  })

  test('rejects a duplicate name', async () => {
    renderWithRouter()
    await screen.findByTestId('policy-row-1')
    fireEvent.click(screen.getByRole('button', { name: /New policy/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'block-guest' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByText('Name is already used')).toBeInTheDocument()
  })

  test('creates a policy with profiles from the security level', async () => {
    renderWithRouter()
    await screen.findByTestId('policy-row-1')
    fireEvent.click(screen.getByRole('button', { name: /New policy/ }))
    const drawer = screen.getByRole('button', { name: 'Create' }).closest('form')!
    fireEvent.change(within(drawer).getByLabelText('Name'), { target: { value: 'guest-web' } })
    const incoming = within(drawer).getByText('Incoming interface').closest('fieldset')!
    fireEvent.click(within(incoming).getByLabelText('guest'))
    const outgoing = within(drawer).getByText('Outgoing interface').closest('fieldset')!
    fireEvent.click(within(outgoing).getByLabelText('wan1'))
    const service = within(drawer).getByText('Service').closest('fieldset')!
    fireEvent.click(within(service).getByLabelText('web')) // replaces "any"
    fireEvent.click(within(drawer).getByRole('button', { name: 'basic' }))
    fireEvent.click(within(drawer).getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(api.savePolicies).toHaveBeenCalled())
    const p = saved().find((x) => x.name === 'guest-web')!
    expect(p).toMatchObject({ id: 3, src_intf: ['guest'], dst_intf: ['wan1'], service: ['web'], ips_profile: 'basic', action: 'accept' })
  })

  test('edits an existing policy in place', async () => {
    renderWithRouter()
    fireEvent.click(await screen.findByRole('button', { name: 'block-guest' }))
    fireEvent.change(screen.getByLabelText('Action'), { target: { value: 'accept' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.savePolicies).toHaveBeenCalled())
    expect(saved().map((p) => p.id)).toEqual([1, 2])
    expect(saved()[1].action).toBe('accept')
  })

  test('offers the defined schedules in the Schedule select', async () => {
    renderWithRouter()
    fireEvent.click(await screen.findByRole('button', { name: 'block-guest' }))
    const sel = screen.getByLabelText('Schedule') as HTMLSelectElement
    expect(Array.from(sel.options).map((o) => o.value)).toEqual(['', 'business-hours'])
  })

  test('sets the schedule on a policy and saves it', async () => {
    renderWithRouter()
    fireEvent.click(await screen.findByRole('button', { name: 'block-guest' }))
    fireEvent.change(screen.getByLabelText('Schedule'), { target: { value: 'business-hours' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.savePolicies).toHaveBeenCalled())
    expect(saved().find((p) => p.id === 2)?.schedule).toBe('business-hours')
  })

  test('routes a policy through a gateway group and saves it', async () => {
    renderWithRouter()
    fireEvent.click(await screen.findByRole('button', { name: 'block-guest' }))
    const sel = screen.getByLabelText('Gateway') as HTMLSelectElement
    expect(Array.from(sel.options).map((o) => o.value)).toEqual(['', 'wan1', 'vpn', 'failover'])
    fireEvent.change(sel, { target: { value: 'failover' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.savePolicies).toHaveBeenCalled())
    expect(saved().find((p) => p.id === 2)?.gateway).toBe('failover')
    expect(saved().find((p) => p.id === 1)).not.toHaveProperty('gateway')
  })
})

describe('Policy simulator', () => {
  // The result view uses router Links, so render inside a MemoryRouter.
  const openSim = async (url: string) => {
    render(
      <MemoryRouter initialEntries={[url]}>
        <Policies />
      </MemoryRouter>,
    )
    await screen.findByTestId('policy-row-1')
    fireEvent.click(screen.getByRole('button', { name: 'Simulate' }))
    return screen.findByLabelText('Source IP')
  }

  test('sends the flow to the API; the port is included for tcp and omitted for icmp', async () => {
    await openSim('/policy')
    fireEvent.change(screen.getByLabelText('Source IP'), { target: { value: '192.168.1.10' } })
    fireEvent.change(screen.getByLabelText('Destination IP'), { target: { value: '8.8.8.8' } })
    fireEvent.change(screen.getByLabelText('Protocol'), { target: { value: 'tcp' } })
    fireEvent.change(screen.getByLabelText('Destination port'), { target: { value: '443' } })
    fireEvent.click(screen.getByRole('button', { name: 'Simulate connection' }))
    await waitFor(() => expect(api.simulatePolicy).toHaveBeenCalledTimes(1))
    expect(vi.mocked(api.simulatePolicy).mock.calls[0][0]).toEqual({ src_ip: '192.168.1.10', dst_ip: '8.8.8.8', protocol: 'tcp', dst_port: 443, config: 'running' })

    fireEvent.change(screen.getByLabelText('Protocol'), { target: { value: 'icmp' } })
    expect(screen.queryByLabelText('Destination port')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Simulate connection' }))
    await waitFor(() => expect(api.simulatePolicy).toHaveBeenCalledTimes(2))
    expect(vi.mocked(api.simulatePolicy).mock.calls[1][0]).toEqual({ src_ip: '192.168.1.10', dst_ip: '8.8.8.8', protocol: 'icmp', config: 'running' })
  })

  test('shows the verdict, the matched policy link and the steps', async () => {
    const result: SimulateResult = {
      verdict: 'accept',
      reason: 'matched by lan-to-internet',
      chain: 'forward',
      src_intf: 'lan',
      dst_intf: 'wan1',
      policy: { id: 1, name: 'lan-to-internet' },
      nat: true,
      profiles: { ips: 'basic' },
      steps: [
        { id: 2, name: 'block-guest', matched: false, reason: 'source interface does not match' },
        { id: 1, name: 'lan-to-internet', matched: true, reason: 'all criteria matched' },
      ],
    }
    vi.mocked(api.simulatePolicy).mockResolvedValue(result)
    await openSim('/policy')
    fireEvent.change(screen.getByLabelText('Source IP'), { target: { value: '192.168.1.10' } })
    fireEvent.change(screen.getByLabelText('Destination IP'), { target: { value: '8.8.8.8' } })
    fireEvent.click(screen.getByRole('button', { name: 'Simulate connection' }))
    expect(await screen.findByTestId('sim-verdict')).toHaveTextContent('ACCEPT')
    expect(screen.getByText('matched by lan-to-internet')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Matched policy: lan-to-internet/ })).toHaveAttribute('href', '/policy?edit=1')
    expect(screen.getByTestId('sim-step-2')).toHaveTextContent('block-guest')
    expect(screen.getByTestId('sim-step-1')).toHaveTextContent('all criteria matched')
  })

  test('a simulator error is shown in the panel', async () => {
    vi.mocked(api.simulatePolicy).mockRejectedValue(new Error('cannot_simulate: no gateway'))
    await openSim('/policy')
    fireEvent.change(screen.getByLabelText('Source IP'), { target: { value: '10.0.0.5' } })
    fireEvent.change(screen.getByLabelText('Destination IP'), { target: { value: '9.9.9.9' } })
    fireEvent.click(screen.getByRole('button', { name: 'Simulate connection' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('cannot_simulate: no gateway')
  })

  test('opens prefilled from the ?simulate=1 query (log drill-down)', async () => {
    await openSim('/policy?simulate=1&src_ip=192.168.1.20&dst_ip=8.8.8.8&protocol=udp&dst_port=53')
    expect(screen.getByLabelText('Source IP')).toHaveValue('192.168.1.20')
    expect(screen.getByLabelText('Destination IP')).toHaveValue('8.8.8.8')
    expect(screen.getByLabelText('Protocol')).toHaveValue('udp')
    expect(screen.getByLabelText('Destination port')).toHaveValue(53)
  })

  test('keeps the event time of a log drill-down', async () => {
    const at = new Date(2026, 9, 17, 14, 30).toISOString() // 14:30 local
    await openSim(`/policy?simulate=1&src_ip=192.168.1.20&dst_ip=8.8.8.8&at=${encodeURIComponent(at)}`)
    expect(screen.getByLabelText('Time')).toHaveValue('2026-10-17T14:30')
  })

  test('the matched policy link opens the editor in place of the simulator', async () => {
    vi.mocked(api.simulatePolicy).mockResolvedValue({
      verdict: 'accept', reason: 'ok', chain: 'forward', src_intf: 'lan', dst_intf: 'wan1',
      policy: { id: 1, name: 'lan-to-internet' },
      nat: false,
      steps: [{ id: 1, name: 'lan-to-internet', matched: true, reason: 'all criteria matched' }],
    })
    await openSim('/policy')
    fireEvent.change(screen.getByLabelText('Source IP'), { target: { value: '192.168.1.10' } })
    fireEvent.change(screen.getByLabelText('Destination IP'), { target: { value: '8.8.8.8' } })
    fireEvent.click(screen.getByRole('button', { name: 'Simulate connection' }))
    fireEvent.click(await screen.findByRole('link', { name: /Matched policy: lan-to-internet/ }))
    expect(await screen.findByRole('dialog', { name: 'Edit policy 1' })).toBeInTheDocument()
    expect(screen.queryByRole('dialog', { name: 'Simulate a connection' })).toBeNull()
  })
})
