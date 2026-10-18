import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import type { Candidate } from '../api/objects'

vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn(), patchSection: vi.fn() }
})
vi.mock('../api/system', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/system')>()
  return {
    ...real,
    getAdmins: vi.fn(),
    createAdmin: vi.fn(),
    updateAdmin: vi.fn(),
    deleteAdmin: vi.fn(),
    getTokens: vi.fn(),
    createToken: vi.fn(),
    deleteToken: vi.fn(),
    getSessionList: vi.fn(),
    deleteSession: vi.fn(),
    downloadBackup: vi.fn(),
    restoreBackup: vi.fn(),
    getSystemInfo: vi.fn(),
  }
})
vi.mock('../api/config', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/config')>()
  return { ...real, getConfigDiff: vi.fn() }
})
vi.mock('../api/auth', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/auth')>()
  return { ...real, getMe: vi.fn(), setup2FA: vi.fn(), enable2FA: vi.fn(), disable2FA: vi.fn() }
})

import * as api from '../api/objects'
import * as sys from '../api/system'
import * as cfgApi from '../api/config'
import * as auth from '../api/auth'
import System from '../pages/System'

const cfg: Candidate = {
  system: { hostname: 'gw1', timezone: 'UTC', ntp_servers: ['pool.ntp.org'], dns_servers: ['1.1.1.1'] },
}

const wrap = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>)

beforeEach(() => {
  vi.mocked(api.getCandidate).mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(sys.getAdmins).mockResolvedValue([])
  vi.mocked(sys.createAdmin).mockResolvedValue({ username: 'x', role: 'admin', has_password: true })
  vi.mocked(sys.updateAdmin).mockResolvedValue({ status: 'changed' })
  vi.mocked(sys.deleteAdmin).mockResolvedValue({ status: 'deleted' })
  vi.mocked(sys.getTokens).mockResolvedValue([])
  vi.mocked(sys.createToken).mockResolvedValue({ token: 'tok_abc', info: { id: '1', name: 't', user: 'root', role: 'admin', created: new Date().toISOString() } })
  vi.mocked(sys.deleteToken).mockResolvedValue({ status: 'deleted' })
  vi.mocked(sys.getSessionList).mockReset().mockResolvedValue([])
  vi.mocked(sys.deleteSession).mockReset().mockResolvedValue({})
  vi.mocked(sys.downloadBackup).mockResolvedValue(undefined)
  vi.mocked(sys.restoreBackup).mockResolvedValue({ status: 'candidate', hostname: 'gw1', message: 'ok' })
  vi.mocked(cfgApi.getConfigDiff).mockResolvedValue({ count: 0, changes: [] })
  vi.mocked(auth.getMe).mockResolvedValue({ user: 'root', role: 'admin', method: 'session' })
  vi.mocked(auth.setup2FA).mockReset().mockResolvedValue({ secret: 'JBSWY3DPEHPK3PXP', otpauth_url: 'otpauth://totp/goblin-gate:root?secret=JBSWY3DPEHPK3PXP', qr_svg: '<svg id="qr"></svg>' })
  vi.mocked(auth.enable2FA).mockReset().mockResolvedValue({ status: 'enabled' })
  vi.mocked(auth.disable2FA).mockReset().mockResolvedValue({ status: 'disabled' })
  vi.mocked(sys.getSystemInfo).mockResolvedValue({ version: '0.1.0', time: new Date().toISOString(), hostname: 'gw1', uptime_seconds: 3600, ntp_synchronized: true })
})

describe('General tab', () => {
  test('PATCHes the system section on save', async () => {
    wrap(<System />)
    // Wait for the form to load (hostname input appears).
    await screen.findByLabelText('Hostname')
    fireEvent.change(screen.getByLabelText('Hostname'), { target: { value: 'newhost' } })
    fireEvent.change(screen.getByLabelText('Time zone'), { target: { value: 'Europe/Berlin' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const calls = vi.mocked(api.patchSection).mock.calls
    expect(calls[0][0]).toBe('system')
    expect(calls[0][1]).toEqual({ hostname: 'newhost', timezone: 'Europe/Berlin', ntp_servers: ['pool.ntp.org'], dns_servers: ['1.1.1.1'] })
  })
})

describe('General tab keeps other settings', () => {
  test('SSH keys and the update channel survive a save', async () => {
    const c = structuredClone(cfg)
    c.system = { ...c.system!, ssh_keys: [{ name: 'me', key: 'ssh-ed25519 AAAA' }], update_channel: 'stable' }
    vi.mocked(api.getCandidate).mockResolvedValue(c)
    wrap(<System />)
    await screen.findByLabelText('Hostname')
    fireEvent.change(screen.getByLabelText('Hostname'), { target: { value: 'newhost' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const body = vi.mocked(api.patchSection).mock.calls[0][1] as Record<string, unknown>
    expect(body.ssh_keys).toEqual([{ name: 'me', key: 'ssh-ed25519 AAAA' }])
    expect(body.update_channel).toBe('stable')
    expect(body.hostname).toBe('newhost')
  })
})

describe('Admins tab', () => {
  test('validates admin create and does not call API on error', async () => {
    wrap(<System />)
    fireEvent.click(screen.getByRole('button', { name: 'Admins' }))
    // Wait for the admins table to render.
    await screen.findByText('Administrators')
    fireEvent.click(screen.getByRole('button', { name: /Add admin/ }))
    fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'bad name!' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByText(/Use 1-63 letters/)).toBeInTheDocument()
    expect(sys.createAdmin).not.toHaveBeenCalled()
  })

  test('last admin with password cannot be deleted', async () => {
    vi.mocked(sys.getAdmins).mockResolvedValue([{ username: 'root', role: 'admin', has_password: true }])
    vi.mocked(auth.getMe).mockResolvedValue({ user: 'other', role: 'admin', method: 'session' })
    wrap(<System />)
    fireEvent.click(screen.getByRole('button', { name: 'Admins' }))
    const delBtn = await screen.findByLabelText('Delete root')
    expect(delBtn).toBeDisabled()
  })

  test('active sessions card lists sessions and signs out via DELETE', async () => {
    vi.mocked(sys.getSessionList).mockResolvedValue([
      { id: 'a1b2c3d4e5f60718', user: 'root', role: 'admin', addr: '10.0.0.9', user_agent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0', created: new Date().toISOString(), last_seen: new Date().toISOString(), current: true },
      { id: 'ffeeddccbbaa9988', user: 'alice', role: 'readonly', addr: '10.0.0.42', user_agent: 'Mozilla/5.0 (X11; Linux x86_64) Firefox/121.0', created: new Date().toISOString(), last_seen: new Date(Date.now() - 120000).toISOString() },
    ])
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    wrap(<System />)
    fireEvent.click(screen.getByRole('button', { name: 'Admins' }))
    await screen.findByText('Active sessions')
    // Both sessions render; the caller's own is badged.
    expect(await screen.findByText('alice')).toBeInTheDocument()
    expect(screen.getByText('This session')).toBeInTheDocument()
    // Only the other user has a sign-out button.
    const btn = screen.getByRole('button', { name: 'Sign out alice' })
    fireEvent.click(btn)
    await waitFor(() => expect(sys.deleteSession).toHaveBeenCalledWith('ffeeddccbbaa9988'))
  })

  test('sign out is skipped when confirm is declined', async () => {
    vi.mocked(sys.getSessionList).mockResolvedValue([
      { id: 'ffeeddccbbaa9988', user: 'alice', role: 'readonly', addr: '10.0.0.42', user_agent: 'Firefox/121.0', created: new Date().toISOString(), last_seen: new Date().toISOString() },
    ])
    vi.spyOn(window, 'confirm').mockReturnValue(false)
    wrap(<System />)
    fireEvent.click(screen.getByRole('button', { name: 'Admins' }))
    const btn = await screen.findByRole('button', { name: 'Sign out alice' })
    fireEvent.click(btn)
    expect(sys.deleteSession).not.toHaveBeenCalled()
  })

  test('admins with two-factor enabled get a 2FA badge', async () => {
    vi.mocked(sys.getAdmins).mockResolvedValue([
      { username: 'root', role: 'admin', has_password: true, totp_enabled: true },
      { username: 'alice', role: 'readonly', has_password: true },
    ])
    wrap(<System />)
    fireEvent.click(screen.getByRole('button', { name: 'Admins' }))
    await screen.findByText('Administrators')
    // Only the totp_enabled admin (root) has the badge, not alice.
    const rows = screen.getAllByRole('row')
    const rootRow = rows.find((r) => r.textContent?.includes('root'))!
    expect(rootRow.textContent).toContain('2FA')
    const aliceRow = rows.find((r) => r.textContent?.includes('alice'))!
    expect(aliceRow.textContent).not.toContain('2FA')
  })

  test('two-factor setup shows the QR and secret, then enable sends the code', async () => {
    wrap(<System />)
    fireEvent.click(screen.getByRole('button', { name: 'Admins' }))
    await screen.findByText('Two-factor authentication')
    fireEvent.click(screen.getByRole('button', { name: /Set up/ }))
    // The QR svg and the secret are shown.
    expect(await screen.findByText('JBSWY3DPEHPK3PXP')).toBeInTheDocument()
    const qr = document.querySelector('svg#qr')
    expect(qr).not.toBeNull()
    fireEvent.change(screen.getByLabelText(/Code from your app/i), { target: { value: '123456' } })
    fireEvent.click(screen.getByRole('button', { name: /Turn on/ }))
    await waitFor(() => expect(auth.enable2FA).toHaveBeenCalledWith('123456'))
    expect(await screen.findByText('Two-factor login is on.')).toBeInTheDocument()
  })

  test('two-factor disable sends the current password', async () => {
    vi.mocked(auth.getMe).mockResolvedValue({ user: 'root', role: 'admin', method: 'session', totp_enabled: true })
    wrap(<System />)
    fireEvent.click(screen.getByRole('button', { name: 'Admins' }))
    await screen.findByText('Two-factor authentication')
    fireEvent.click(screen.getByRole('button', { name: /Turn off/ }))
    fireEvent.change(screen.getByLabelText(/Current password/i), { target: { value: 'hunter2' } })
    fireEvent.click(screen.getByRole('button', { name: /Turn off/ }))
    await waitFor(() => expect(auth.disable2FA).toHaveBeenCalledWith('hunter2'))
  })
})

describe('Backup tab', () => {
  test('restore shows the candidate diff', async () => {
    vi.mocked(cfgApi.getConfigDiff).mockResolvedValue({
      count: 1,
      changes: [{ path: 'system.hostname', old: 'oldhost', new: 'newhost' }],
    })
    wrap(<System />)
    fireEvent.click(screen.getByRole('button', { name: 'Backup' }))
    // Wait for the download button to appear.
    await screen.findByRole('button', { name: /Download backup/ })

    const file = new File(['fake zip'], 'backup.zip', { type: 'application/zip' })
    const input = document.querySelector('input[type="file"]') as HTMLInputElement
    Object.defineProperty(input, 'files', { value: [file], configurable: true })
    fireEvent.change(input)

    // Wait for the diff to appear.
    expect(await screen.findByText(/Candidate diff/)).toBeInTheDocument()
    expect(screen.getByText('system.hostname')).toBeInTheDocument()
    expect(screen.getByText('"oldhost"')).toBeInTheDocument()
    expect(screen.getByText('"newhost"')).toBeInTheDocument()
  })
})

describe('Metrics card', () => {
  test('saves enabled and the allow list to system.metrics', async () => {
    const c = structuredClone(cfg)
    c.system = { ...c.system!, metrics: { enabled: false, allow: ['10.0.0.5/32'] } }
    vi.mocked(api.getCandidate).mockResolvedValue(c)
    wrap(<System />)
    const box = await screen.findByLabelText('Enable metrics')
    expect(screen.getByLabelText('Metrics allow list')).toHaveValue('10.0.0.5/32')
    fireEvent.click(box)
    fireEvent.change(screen.getByLabelText('Metrics allow list'), { target: { value: '192.168.1.10/32\n 10.0.0.0/8 \n' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save metrics' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    expect(vi.mocked(api.patchSection).mock.calls[0]).toEqual(['system.metrics', { enabled: true, allow: ['192.168.1.10/32', '10.0.0.0/8'] }])
    expect(await screen.findByText('Saved. Use the commit bar to apply.')).toBeInTheDocument()
  })
})
