import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import type { Policy, PolicyConfig } from '../api/policy'

// Mock the API module: no network. objectsOf stays real.
vi.mock('../api/policy', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/policy')>()
  return {
    ...real,
    getCandidate: vi.fn(),
    getPolicyHits: vi.fn(),
    savePolicies: vi.fn(),
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

beforeEach(() => {
  vi.mocked(api.getCandidate).mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.getPolicyHits).mockResolvedValue({ 1: 1234 })
  vi.mocked(api.savePolicies).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
})

describe('Policies table', () => {
  test('renders policies in order with action, NAT, badges and hit counts', async () => {
    render(<Policies />)
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
    render(<Policies />)
    await screen.findByTestId('policy-row-1')
    fireEvent.click(screen.getByLabelText('Group by interface pair'))
    expect(screen.getByText('lan → wan1')).toBeInTheDocument()
    expect(screen.getByText('guest → lan')).toBeInTheDocument()
  })

  test('enable toggle saves the list', async () => {
    render(<Policies />)
    fireEvent.click(await screen.findByLabelText('Enable policy 2'))
    await waitFor(() => expect(api.savePolicies).toHaveBeenCalled())
    expect(saved().find((p) => p.id === 2)?.enabled).toBe(true)
  })

  test('kebab menu clones below the policy', async () => {
    render(<Policies />)
    fireEvent.click(await screen.findByLabelText('Actions for policy 1'))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Clone' }))
    await waitFor(() => expect(api.savePolicies).toHaveBeenCalled())
    expect(saved().map((p) => p.id)).toEqual([1, 3, 2])
    expect(saved()[1].name).toBe('lan-to-internet-copy')
  })

  test('a failed save keeps the old list and shows the error', async () => {
    vi.mocked(api.savePolicies).mockRejectedValue(new Error('server down'))
    render(<Policies />)
    fireEvent.click(await screen.findByLabelText('Enable policy 2'))
    expect(await screen.findByRole('alert')).toHaveTextContent('server down')
    expect(screen.getByLabelText('Enable policy 2')).not.toBeChecked()
  })

  test('shows server validation errors for policies', async () => {
    vi.mocked(api.savePolicies).mockResolvedValue({
      status: 'ok',
      validation_errors: [{ path: 'policies[id=2].src_intf', message: 'unknown interface' }],
    })
    render(<Policies />)
    fireEvent.click(await screen.findByLabelText('Enable policy 2'))
    expect(await screen.findByText(/unknown interface/)).toBeInTheDocument()
  })
})

describe('Policy editor', () => {
  test('blocks saving an incomplete policy and shows field errors', async () => {
    render(<Policies />)
    await screen.findByTestId('policy-row-1')
    fireEvent.click(screen.getByRole('button', { name: /New policy/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByText('Name is required')).toBeInTheDocument()
    expect(screen.getByText('Pick at least one incoming interface')).toBeInTheDocument()
    expect(api.savePolicies).not.toHaveBeenCalled()
  })

  test('rejects a duplicate name', async () => {
    render(<Policies />)
    await screen.findByTestId('policy-row-1')
    fireEvent.click(screen.getByRole('button', { name: /New policy/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'block-guest' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByText('Name is already used')).toBeInTheDocument()
  })

  test('creates a policy with profiles from the security level', async () => {
    render(<Policies />)
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
    render(<Policies />)
    fireEvent.click(await screen.findByRole('button', { name: 'block-guest' }))
    fireEvent.change(screen.getByLabelText('Action'), { target: { value: 'accept' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.savePolicies).toHaveBeenCalled())
    expect(saved().map((p) => p.id)).toEqual([1, 2])
    expect(saved()[1].action).toBe('accept')
  })

  test('offers the defined schedules in the Schedule select', async () => {
    render(<Policies />)
    fireEvent.click(await screen.findByRole('button', { name: 'block-guest' }))
    const sel = screen.getByLabelText('Schedule') as HTMLSelectElement
    expect(Array.from(sel.options).map((o) => o.value)).toEqual(['', 'business-hours'])
  })

  test('sets the schedule on a policy and saves it', async () => {
    render(<Policies />)
    fireEvent.click(await screen.findByRole('button', { name: 'block-guest' }))
    fireEvent.change(screen.getByLabelText('Schedule'), { target: { value: 'business-hours' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.savePolicies).toHaveBeenCalled())
    expect(saved().find((p) => p.id === 2)?.schedule).toBe('business-hours')
  })

  test('routes a policy through a gateway group and saves it', async () => {
    render(<Policies />)
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
