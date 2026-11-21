// Task 150: the roles editor on System → Admins, the role picker listing every
// role, and what a limited role sees (read-only lists, no commit, no admin tabs).
import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import type { Candidate } from '../api/objects'
import { APIRequestError } from '../api/client'
import { forbiddenMessage } from '../api/permissions'
import type { RoleInfo } from '../api/roles'

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
    getSessionList: vi.fn(),
    deleteSession: vi.fn(),
    getSystemInfo: vi.fn(),
  }
})
vi.mock('../api/roles', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/roles')>()
  return { ...real, getRoles: vi.fn(), createRole: vi.fn(), updateRole: vi.fn(), deleteRole: vi.fn() }
})
vi.mock('../api/auth', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/auth')>()
  return { ...real, getMe: vi.fn(), setup2FA: vi.fn(), enable2FA: vi.fn(), disable2FA: vi.fn() }
})

import * as api from '../api/objects'
import * as sys from '../api/system'
import * as rolesApi from '../api/roles'
import * as auth from '../api/auth'
import System from '../pages/System'
import Addresses from '../pages/network/Addresses'

const cfg: Candidate = {
  system: { hostname: 'gw1', timezone: 'UTC' },
  addresses: [{ name: 'web', type: 'host', value: '10.0.0.5' }],
}

const scopes = [
  { id: 'read', description: 'See everything except secrets' },
  { id: 'write:firewall', description: 'Edit objects, policies, NAT, security profiles, feeds and the reverse proxy' },
  { id: 'write:network', description: 'Edit interfaces, VLANs, DHCP, DNS, gateways, routes and VPNs' },
  { id: 'write:system', description: 'Edit system settings, notifications, log and flow export, snapshots' },
  { id: 'actions', description: 'Block addresses, manage devices, run diagnostics and refreshes' },
  { id: 'commit', description: 'Commit, confirm, discard and roll back changes' },
]

const roleList: RoleInfo[] = [
  { name: 'admin', description: 'Everything', scopes: ['read', 'write:firewall', 'write:network', 'write:system', 'actions', 'commit'], builtin: true, users: ['root'] },
  { name: 'readonly', description: 'See everything except secrets', scopes: ['read'], builtin: true, users: ['kim'] },
  { name: 'helpdesk', description: 'Look and block', scopes: ['read', 'actions'], builtin: false, users: ['sam'] },
]

const fullAdmin = { user: 'root', role: 'admin', method: 'session' as const, permissions: ['read', 'write:firewall', 'write:network', 'write:system', 'actions', 'commit'], full_admin: true }
const helpdeskMe = { user: 'sam', role: 'helpdesk', method: 'session' as const, permissions: ['read', 'actions'], full_admin: false }

const wrap = (ui: React.ReactElement) => render(<MemoryRouter>{ui}</MemoryRouter>)
const openAdmins = async () => {
  fireEvent.click(screen.getByRole('button', { name: 'Admins' }))
  return screen.findByText('Roles')
}

beforeEach(() => {
  vi.mocked(api.getCandidate).mockReset().mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(sys.getAdmins).mockReset().mockResolvedValue([{ username: 'root', role: 'admin', has_password: true }])
  vi.mocked(sys.createAdmin).mockReset().mockResolvedValue({ username: 'x', role: 'admin', has_password: true })
  vi.mocked(sys.updateAdmin).mockReset().mockResolvedValue({ status: 'changed' })
  vi.mocked(sys.deleteAdmin).mockReset().mockResolvedValue({ status: 'deleted' })
  vi.mocked(sys.getSessionList).mockReset().mockResolvedValue([])
  vi.mocked(sys.deleteSession).mockReset().mockResolvedValue({})
  vi.mocked(sys.getSystemInfo).mockReset().mockResolvedValue({ version: '0.3.0', time: new Date().toISOString(), hostname: 'gw1', uptime_seconds: 60, ntp_synchronized: true })
  vi.mocked(rolesApi.getRoles).mockReset().mockResolvedValue({ roles: structuredClone(roleList), scopes })
  vi.mocked(rolesApi.createRole).mockReset().mockResolvedValue({ name: 'fwops', description: '', scopes: ['read', 'write:firewall'], builtin: false, users: [] })
  vi.mocked(rolesApi.updateRole).mockReset().mockResolvedValue({ status: 'changed' })
  vi.mocked(rolesApi.deleteRole).mockReset().mockResolvedValue({ status: 'deleted' })
  vi.mocked(auth.getMe).mockReset().mockResolvedValue(structuredClone(fullAdmin))
  vi.spyOn(window, 'confirm').mockReturnValue(true)
  vi.spyOn(window, 'prompt').mockReturnValue('')
})

describe('the Roles section', () => {
  test('lists the built-in roles read-only and custom roles with their users', async () => {
    wrap(<System />)
    await openAdmins()
    const row = await screen.findByRole('row', { name: /helpdesk/ })
    // The scope labels come from the API descriptions, not the raw ids.
    expect(within(row).getByText('Block addresses, manage devices, run diagnostics and refreshes')).toBeInTheDocument()
    expect(within(row).getByText('sam')).toBeInTheDocument()
    // Built-ins have no edit or delete control; the custom role does.
    const adminRow = screen.getByRole('row', { name: /^admin/ })
    expect(within(adminRow).queryByRole('button', { name: 'Edit' })).toBeNull()
    expect(within(row).getByRole('button', { name: 'Edit' })).toBeInTheDocument()
    expect(within(row).getByRole('button', { name: 'Delete role helpdesk' })).toBeInTheDocument()
  })

  test('creating a role sends its name, description and scopes', async () => {
    wrap(<System />)
    await openAdmins()
    fireEvent.click(screen.getByRole('button', { name: /New role/ }))
    fireEvent.change(screen.getByLabelText('Role', { selector: 'input' }), { target: { value: 'fwops' } })
    fireEvent.change(screen.getByLabelText('Description'), { target: { value: 'Firewall only' } })
    fireEvent.click(screen.getByLabelText('Edit objects, policies, NAT, security profiles, feeds and the reverse proxy'))
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() =>
      expect(rolesApi.createRole).toHaveBeenCalledWith({ name: 'fwops', description: 'Firewall only', scopes: ['read', 'write:firewall'] }),
    )
  })

  test('a bad name is refused locally and the API is not called', async () => {
    wrap(<System />)
    await openAdmins()
    fireEvent.click(screen.getByRole('button', { name: /New role/ }))
    fireEvent.change(screen.getByLabelText('Role', { selector: 'input' }), { target: { value: 'bad name' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByText('Use 1-63 letters, digits, ".", "_" or "-"')).toBeInTheDocument()
    expect(rolesApi.createRole).not.toHaveBeenCalled()
  })

  test('editing a role sends the new scopes', async () => {
    wrap(<System />)
    await openAdmins()
    const row = await screen.findByRole('row', { name: /helpdesk/ })
    fireEvent.click(within(row).getByRole('button', { name: 'Edit' }))
    // read and actions come pre-ticked; add commit and drop read.
    const commit = await screen.findByLabelText('Commit, confirm, discard and roll back changes')
    expect(commit).not.toBeChecked()
    fireEvent.click(commit)
    fireEvent.click(screen.getByLabelText('See everything except secrets'))
    // the single sign-on card below has its own Save button: take this form's
    fireEvent.click(within(screen.getByRole('form', { name: 'Edit role helpdesk' })).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(rolesApi.updateRole).toHaveBeenCalledWith('helpdesk', { description: 'Look and block', scopes: ['actions', 'commit'] }))
  })

  test('deleting asks first and a conflict names the user', async () => {
    wrap(<System />)
    await openAdmins()
    const row = await screen.findByRole('row', { name: /helpdesk/ })
    fireEvent.click(within(row).getByRole('button', { name: 'Delete role helpdesk' }))
    await waitFor(() => expect(rolesApi.deleteRole).toHaveBeenCalledWith('helpdesk'))
    // A 409 from the server (a user still has the role) is shown, not swallowed.
    vi.mocked(rolesApi.deleteRole).mockRejectedValueOnce(
      new APIRequestError('role helpdesk is still given to sam; give them another role first', 409, 'role_conflict', null),
    )
    fireEvent.click(within(row).getByRole('button', { name: 'Delete role helpdesk' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('still given to sam')
  })
})

describe('assigning a role to a user', () => {
  test('the picker lists every role, custom ones included', async () => {
    vi.mocked(sys.getAdmins).mockResolvedValue([
      { username: 'root', role: 'admin', has_password: true },
      { username: 'sam', role: 'helpdesk', has_password: true },
    ])
    wrap(<System />)
    await openAdmins()
    const select = await screen.findByLabelText('Role for sam')
    expect(within(select).getByRole('option', { name: 'helpdesk' })).toBeInTheDocument()
    fireEvent.change(select, { target: { value: 'readonly' } })
    await waitFor(() => expect(sys.updateAdmin).toHaveBeenCalledWith('sam', { role: 'readonly' }))
  })

  test('the create form offers every role', async () => {
    wrap(<System />)
    await openAdmins()
    fireEvent.click(await screen.findByRole('button', { name: /Add admin/ }))
    const select = screen.getByLabelText('Role', { selector: 'select' })
    expect(within(select).getByRole('option', { name: 'helpdesk' })).toBeInTheDocument()
    fireEvent.change(select, { target: { value: 'helpdesk' } })
    fireEvent.change(screen.getByLabelText('Username'), { target: { value: 'sam' } })
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'hunter2hunt' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(sys.createAdmin).toHaveBeenCalledWith({ username: 'sam', role: 'helpdesk', password: 'hunter2hunt' }))
  })
})

describe('a limited role', () => {
  test('the admin tabs are hidden and the object list is read-only', async () => {
    vi.mocked(auth.getMe).mockResolvedValue(structuredClone(helpdeskMe))
    wrap(<Addresses />)
    // write:firewall is missing, so the create button and the delete column go.
    expect(await screen.findByText('Addresses')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^New/ })).toBeNull()
    expect(screen.queryByLabelText('Delete web')).toBeNull()
    expect(screen.getByText('Your role cannot change these objects; you can still see them.')).toBeInTheDocument()
    // The row is still readable.
    expect(screen.getByText('web')).toBeInTheDocument()
  })

  test('a role with the section scope can still edit', async () => {
    vi.mocked(auth.getMe).mockResolvedValue({ ...helpdeskMe, permissions: ['read', 'write:firewall'], role: 'fwops' })
    wrap(<Addresses />)
    await screen.findByText('Addresses')
    expect(screen.getByRole('button', { name: /^New/ })).toBeEnabled()
    expect(screen.getByLabelText('Delete web')).toBeInTheDocument()
  })

  test('System hides the admin-only tabs without full_admin, but keeps their own account', async () => {
    vi.mocked(auth.getMe).mockResolvedValue(structuredClone(helpdeskMe))
    wrap(<System />)
    await screen.findByLabelText('Hostname')
    expect(screen.queryByRole('button', { name: 'API Tokens' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Backup' })).toBeNull()
    expect(screen.getByRole('button', { name: 'General' })).toBeInTheDocument()
    // Admins shows only their own two-factor login and sessions: no account list, no roles editor
    fireEvent.click(screen.getByRole('button', { name: 'Admins' }))
    expect(await screen.findByRole('button', { name: /Set up/ })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Add admin/ })).toBeNull()
    expect(sys.getAdmins).not.toHaveBeenCalled()
  })

  test('a 403 from the API names the scope it needs', async () => {
    vi.mocked(auth.getMe).mockResolvedValue(structuredClone(helpdeskMe))
    // A page that tries anyway gets a clear message, not a broken page.
    expect(forbiddenMessage(new APIRequestError('your role does not allow this', 403, 'forbidden', { needs: 'write:system' }))).toBe(
      'your role does not allow this (your role needs the write:system scope)',
    )
    expect(forbiddenMessage(new APIRequestError('admins only', 403, 'forbidden', null))).toBe('admins only')
    expect(forbiddenMessage(new APIRequestError('too many requests', 429, 'busy', null))).toBe('too many requests')
  })
})
