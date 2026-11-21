import React from 'react'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import type { Candidate } from '../api/objects'
import type { VoucherBatch } from '../api/portal'

vi.mock('../api/auth', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/auth')>()
  return { ...real, getMe: vi.fn().mockResolvedValue({ user: 'admin', role: 'admin', method: 'session', full_admin: true }) }
})
vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn(), patchSection: vi.fn() }
})
vi.mock('../api/portal', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/portal')>()
  return {
    ...real,
    getPortalClients: vi.fn(),
    kickPortalClient: vi.fn(),
    getVoucherBatches: vi.fn(),
    createVoucherBatch: vi.fn(),
    revokeVouchers: vi.fn(),
    deleteVoucherBatch: vi.fn(),
  }
})

import * as api from '../api/objects'
import * as portal from '../api/portal'
import { getMe } from '../api/auth'
import CaptivePortal from '../pages/network/CaptivePortal'
import { buildPortal, formatLeft, portalFormOf, printableBatch, validatePortal } from '../pages/network/portalLogic'

const cfg: Candidate = {
  interfaces: [
    { name: 'wan1', device: 'eth0', role: 'wan', mode: 'dhcp', enabled: true },
    { name: 'lan', device: 'eth1', role: 'lan', mode: 'static', address: '192.168.1.1/24', enabled: true },
  ],
  vlans: [
    { name: 'guest', parent: 'lan', vlan_id: 30, mode: 'static', address: '192.168.30.1/24', enabled: true },
    { name: 'iot', parent: 'lan', vlan_id: 40, mode: 'dhcp', enabled: true },
  ],
  captive_portals: [{ interface: 'guest', enabled: true, mode: 'password', password: '', idle_minutes: 30, title: 'Guest Wi-Fi', text: 'Be nice.' }],
}

const batch: VoucherBatch = {
  id: 'B1',
  note: 'party',
  minutes: 240,
  uses: 1,
  created: '2026-11-20T12:00:00Z',
  counts: { unused: 1, used_up: 1 },
  codes: [
    { code: 'ABCD-EFGH', used: 0, state: 'unused' },
    { code: 'JKMN-PQRS', used: 1, state: 'used_up' },
  ],
}

const lastPatch = () => {
  const calls = vi.mocked(api.patchSection).mock.calls
  return calls[calls.length - 1]
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.getCandidate).mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.patchSection).mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(portal.getVoucherBatches).mockResolvedValue({ batches: [structuredClone(batch)] })
  vi.mocked(portal.getPortalClients).mockResolvedValue({
    clients: [{ interface: 'guest', mac: 'aa:bb:cc:00:00:01', ip: '192.168.30.100', method: 'voucher', voucher: 'JKMN-PQRS', batch: 'B1', started: '', expires: '', seconds_left: 5400 }],
  })
})

describe('Network → Captive portal', () => {
  it('shows a portal with its preview and saves changes, keeping the stored password', async () => {
    render(<CaptivePortal />)
    const card = await screen.findByRole('region', { name: 'Portal on guest' })
    expect(within(card).getByTitle('Portal page preview for guest')).toHaveAttribute('src', '/api/v1/portal/preview/guest?v=0')
    const pw = within(card).getByLabelText('Password')
    expect(pw).toHaveAttribute('placeholder', 'unchanged')
    fireEvent.change(within(card).getByLabelText('Limit per guest (Mbit/s)'), { target: { value: '20' } })
    fireEvent.click(within(card).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, value] = lastPatch()
    expect(path).toBe('captive_portals')
    expect(value).toEqual([{ interface: 'guest', enabled: true, mode: 'password', idle_minutes: 30, rate_limit_mbps: 20, title: 'Guest Wi-Fi', text: 'Be nice.' }])
    expect(await screen.findByText('Saved. Commit to apply.')).toBeInTheDocument()
    expect(within(card).getByTitle('Portal page preview for guest')).toHaveAttribute('src', '/api/v1/portal/preview/guest?v=1')
  })

  it('checks the fields before saving', async () => {
    render(<CaptivePortal />)
    const card = await screen.findByRole('region', { name: 'Portal on guest' })
    fireEvent.change(within(card).getByLabelText('Idle timeout (min)'), { target: { value: '2' } })
    fireEvent.click(within(card).getByRole('button', { name: 'Save' }))
    expect(await within(card).findByText(/Want 5-1440 minutes/)).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  it('shows the server validation messages on the portal', async () => {
    vi.mocked(api.patchSection).mockResolvedValue({ status: 'ok', validation_errors: [{ path: 'captive_portals[0].logo', message: 'want a data: URI of a PNG' }] })
    render(<CaptivePortal />)
    const card = await screen.findByRole('region', { name: 'Portal on guest' })
    fireEvent.click(within(card).getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('want a data: URI of a PNG')).toBeInTheDocument()
  })

  it('adds a portal for an interface with a static address only', async () => {
    render(<CaptivePortal />)
    const select = await screen.findByLabelText('New portal for')
    expect(within(select).getByRole('option', { name: 'iot (needs a static IPv4 address)' })).toBeDisabled()
    expect(within(select).queryByRole('option', { name: 'wan1' })).not.toBeInTheDocument()
    fireEvent.change(select, { target: { value: 'lan' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add a portal' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect((lastPatch()[1] as unknown[])[1]).toEqual({ interface: 'lan', enabled: true, mode: 'click' })
  })

  it('removes a portal', async () => {
    render(<CaptivePortal />)
    fireEvent.click(await screen.findByRole('button', { name: 'Remove the portal on guest' }))
    await waitFor(() => expect(lastPatch()).toEqual(['captive_portals', []]))
  })

  it('makes a voucher batch, lists codes with their state, revokes and prints', async () => {
    vi.mocked(portal.createVoucherBatch).mockResolvedValue(structuredClone(batch))
    vi.mocked(portal.revokeVouchers).mockResolvedValue({ status: 'ok' })
    render(<CaptivePortal />)
    const form = await screen.findByRole('form', { name: 'New voucher batch' })
    fireEvent.change(within(form).getByLabelText('Codes'), { target: { value: '501' } })
    fireEvent.click(within(form).getByRole('button', { name: 'Make the codes' }))
    expect(await within(form).findByText('Want 1-500 codes.')).toBeInTheDocument()
    fireEvent.change(within(form).getByLabelText('Codes'), { target: { value: '20' } })
    fireEvent.change(within(form).getByLabelText('Note (printed on the codes)'), { target: { value: 'party' } })
    fireEvent.click(within(form).getByRole('button', { name: 'Make the codes' }))
    await waitFor(() => expect(portal.createVoucherBatch).toHaveBeenCalledWith({ count: 20, minutes: 240, uses: 1, valid_days: 30, note: 'party', interface: undefined }))

    const codes = await screen.findByRole('table', { name: 'Codes of party' })
    expect(within(codes).getByText('ABCD-EFGH')).toBeInTheDocument()
    expect(within(codes).getByText('Used up')).toBeInTheDocument()
    expect(screen.getByTestId('counts-B1')).toHaveTextContent('1 unused, 1 used')
    // only a usable code can be revoked on its own
    expect(within(codes).queryByRole('button', { name: 'Revoke JKMN-PQRS' })).not.toBeInTheDocument()
    fireEvent.click(within(codes).getByRole('button', { name: 'Revoke ABCD-EFGH' }))
    await waitFor(() => expect(portal.revokeVouchers).toHaveBeenCalledWith('B1', 'ABCD-EFGH'))

    fireEvent.click(screen.getByRole('button', { name: 'Revoke the batch party' }))
    const dialog = await screen.findByRole('dialog', { name: 'Revoke the batch' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Revoke' }))
    await waitFor(() => expect(portal.revokeVouchers).toHaveBeenCalledWith('B1'))

    const doc = { write: vi.fn(), close: vi.fn() }
    const open = vi.spyOn(window, 'open').mockReturnValue({ document: doc, focus: vi.fn(), print: vi.fn() } as unknown as Window)
    fireEvent.click(screen.getByRole('button', { name: 'Print the codes of party' }))
    expect(open).toHaveBeenCalled()
    expect(doc.write.mock.calls[0][0]).toContain('ABCD-EFGH')
    expect(doc.write.mock.calls[0][0]).not.toContain('JKMN-PQRS')
    open.mockRestore()
  })

  it('lists the guests, polls, and removes one', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.mocked(portal.kickPortalClient).mockResolvedValue({ status: 'ok' })
    try {
      render(<CaptivePortal />)
      const table = await screen.findByRole('table', { name: 'Guests online' })
      expect(within(table).getByText('192.168.30.100')).toBeInTheDocument()
      expect(within(table).getByText('JKMN-PQRS')).toBeInTheDocument()
      expect(within(table).getByText('90 min')).toBeInTheDocument()
      const before = vi.mocked(portal.getPortalClients).mock.calls.length
      await act(async () => {
        vi.advanceTimersByTime(10_000)
      })
      expect(vi.mocked(portal.getPortalClients).mock.calls.length).toBeGreaterThan(before)
      fireEvent.click(within(table).getByRole('button', { name: 'Remove 192.168.30.100' }))
      await waitFor(() => expect(portal.kickPortalClient).toHaveBeenCalledWith('aa:bb:cc:00:00:01'))
    } finally {
      vi.useRealTimers()
    }
  })

  it('is read-only without write:network and actions', async () => {
    vi.mocked(getMe).mockResolvedValue({ user: 'ro', role: 'readonly', method: 'session', full_admin: false, permissions: ['read'] } as never)
    render(<CaptivePortal />)
    expect(await screen.findByText(/can view the portal settings/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument()
    expect(screen.queryByRole('form', { name: 'New voucher batch' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Remove 192.168.30.100' })).not.toBeInTheDocument()
    vi.mocked(getMe).mockResolvedValue({ user: 'admin', role: 'admin', method: 'session', full_admin: true } as never)
  })
})

describe('portalLogic', () => {
  it('builds and validates', () => {
    const f = portalFormOf(undefined, 'guest')
    expect(buildPortal(f)).toEqual({ interface: 'guest', enabled: true, mode: 'click' })
    expect(validatePortal({ ...f, mode: 'password', password: '' }, false).password).toBeTruthy()
    expect(validatePortal({ ...f, mode: 'password', password: '' }, true).password).toBeUndefined()
    expect(validatePortal({ ...f, session: '3' }, false).session).toBeTruthy()
    expect(validatePortal({ ...f, rate: '0' }, false).rate).toBeTruthy()
    expect(buildPortal({ ...f, mode: 'password', password: 'secret', session: '60' })).toEqual({ interface: 'guest', enabled: true, mode: 'password', password: 'secret', session_minutes: 60 })
    expect(formatLeft(5400)).toBe('90 min')
    expect(formatLeft(3 * 3600)).toBe('3 h 0 min')
  })

  it('escapes the printable page', () => {
    const html = printableBatch({ ...batch, codes: [{ code: '<b>', used: 0, state: 'unused' }] }, { title: 'A & B', valid: '4 h', uses: 'one use' })
    expect(html).toContain('&lt;b&gt;')
    expect(html).toContain('A &amp; B')
  })
})
