// Live address objects on the Addresses page (task 141): the live type in the
// form (plugin select, objects:write approval), the row's member summary and
// the read-only members drawer.
import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Candidate } from '../api/objects'

vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn(), patchSection: vi.fn(), getLiveObjects: vi.fn() }
})
vi.mock('../api/plugins', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/plugins')>()
  return { ...real, getPlugins: vi.fn() }
})
import * as api from '../api/objects'
import * as pluginsApi from '../api/plugins'
import Addresses from '../pages/network/Addresses'

const cfg: Candidate = {
  addresses: [
    { name: 'nas', type: 'host', value: '192.168.1.5' },
    { name: 'web', type: 'live', value: 'homelab' },
    { name: 'containers', type: 'live', value: 'homelab' },
    { name: 'lxd', type: 'live', value: 'other' },
  ],
  plugins: [{ name: 'homelab', enabled: true, capabilities: ['objects:write', 'devices:read'] }],
}

const liveObjects = [
  {
    name: 'web',
    plugin: 'homelab',
    members: ['10.20.0.5/32', 'fd00:20::/64'],
    updated: '2026-11-07T09:30:00Z',
  },
  { name: 'containers', plugin: 'homelab', members: [] },
]

const installed = [
  { name: 'homelab', requested_capabilities: ['objects:write', 'devices:read'], capabilities: ['objects:write', 'devices:read'], enabled: true, secrets_set: [], policies: [], state: 'running' as const },
  { name: 'uptime', requested_capabilities: ['objects:write'], capabilities: [], enabled: false, secrets_set: [], policies: [], state: 'stopped' as const },
]

const lastPatch = () => {
  const calls = vi.mocked(api.patchSection).mock.calls
  return calls[calls.length - 1]
}

const wrap = (ui: React.ReactElement) =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>,
  )

beforeEach(() => {
  vi.mocked(api.getCandidate).mockReset().mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(api.getLiveObjects).mockReset().mockResolvedValue(structuredClone(liveObjects))
  vi.mocked(pluginsApi.getPlugins).mockReset().mockResolvedValue({ plugins: structuredClone(installed) })
})

const row = async (name: string) => screen.findByRole('button', { name })

describe('Addresses: live objects', () => {
  test('a live row shows the member count, the plugin and the last update', async () => {
    wrap(<Addresses />)
    const rowEl = (await row('web')).closest('tr')!
    expect(within(rowEl as HTMLElement).getByText('2 members, kept up to date by homelab')).toBeInTheDocument()
    expect(
      within(rowEl as HTMLElement).getByText(`last update ${new Date('2026-11-07T09:30:00Z').toLocaleString()}`),
    ).toBeInTheDocument()
  })

  test('a live object without members yet says the plugin will fill it', async () => {
    wrap(<Addresses />)
    await row('nas')
    const cell = screen.getByText('empty until homelab fills it').closest('td')!
    expect(within(cell as HTMLElement).getByText('no update yet')).toBeInTheDocument()
  })

  test('a live object the running config does not have yet shows as empty', async () => {
    wrap(<Addresses />)
    await row('nas')
    // lxd is in the candidate but the status has no entry for it: same wording
    // as an object its plugin has not filled.
    const cell = screen.getByText('empty until other fills it').closest('td')!
    expect(within(cell as HTMLElement).getByText('no update yet')).toBeInTheDocument()
  })

  test('the members drawer lists the members read-only', async () => {
    wrap(<Addresses />)
    fireEvent.click(await screen.findByRole('button', { name: /2 members, kept up to date by homelab/ }))
    const drawer = await screen.findByRole('dialog')
    expect(within(drawer).getByText('Members')).toBeInTheDocument()
    expect(within(drawer).getAllByText(/^(10\.20\.0\.5\/32|fd00:20::\/64)$/).length).toBe(2)
    expect(within(drawer).queryByRole('textbox')).toBeNull()
    fireEvent.click(within(drawer).getByRole('button', { name: 'Close' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  test('the empty object opens a drawer that repeats the plugin will fill it', async () => {
    wrap(<Addresses />)
    fireEvent.click(await screen.findByRole('button', { name: /empty until homelab fills it/ }))
    const drawer = await screen.findByRole('dialog')
    expect(within(drawer).getAllByText('empty until homelab fills it').length).toBeGreaterThan(0)
    expect(
      within(drawer).getByText(
        'The members change without a commit: the plugin sends them and the firewall uses them within a second.',
      ),
    ).toBeInTheDocument()
  })

  test('the create form offers the live type and a plugin select of approved plugins', async () => {
    wrap(<Addresses />)
    await row('nas')
    fireEvent.click(screen.getByRole('button', { name: /New address/ }))
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'live' } })
    const sel = screen.getByLabelText('Plugin') as HTMLSelectElement
    const opts = within(sel).getAllByRole('option').map((o) => o.textContent)
    expect(opts).toContain('homelab')
    expect(opts.some((o) => o?.startsWith('uptime') && o.includes('needs objects:write approved'))).toBe(true)
    expect(opts.some((o) => o === 'uptime')).toBe(false)
    expect(within(sel).getByRole('option', { name: 'Select…' })).toBeInTheDocument()
  })

  test('saving a live address PATCHes type live with the plugin as value', async () => {
    wrap(<Addresses />)
    await row('nas')
    fireEvent.click(screen.getByRole('button', { name: /New address/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'vm' } })
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'live' } })
    fireEvent.change(screen.getByLabelText('Plugin'), { target: { value: 'homelab' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect(lastPatch()).toEqual(['addresses', [...(cfg.addresses ?? []), { name: 'vm', type: 'live', value: 'homelab' }]])
  })

  test('a plugin without objects:write approved is refused with a field error on value', async () => {
    wrap(<Addresses />)
    await row('nas')
    fireEvent.click(screen.getByRole('button', { name: /New address/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'vm' } })
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'live' } })
    fireEvent.change(screen.getByLabelText('Plugin'), { target: { value: 'uptime' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Plugin uptime does not have objects:write approved')
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('no plugin picked is refused too', async () => {
    wrap(<Addresses />)
    await row('nas')
    fireEvent.click(screen.getByRole('button', { name: /New address/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'vm' } })
    fireEvent.change(screen.getByLabelText('Type'), { target: { value: 'live' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Select the plugin that keeps this address up to date')
  })
})
