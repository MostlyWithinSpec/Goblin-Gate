// System → Admins → Single sign-on (task 148).
import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import type { Candidate } from '../api/objects'

vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn(), patchSection: vi.fn() }
})
vi.mock('../api/roles', () => ({ getRoles: vi.fn() }))

import * as api from '../api/objects'
import * as rolesApi from '../api/roles'
import { SSOSection, buildSSO, formFromSSO, ssoRedirect, validateSSO, type SSOForm, type SSOSettings } from '../components/SSOSection'

const form = (patch: Partial<SSOForm> = {}): SSOForm => ({
  enabled: true,
  name: 'Authentik',
  issuer: 'https://id.example.com/realms/main',
  client_id: 'gate',
  client_secret: '',
  scopes: '',
  username_claim: '',
  groups_claim: '',
  roles: [{ group: 'fw-admins', role: 'admin' }],
  default_role: '',
  redirect_url: '',
  ca_certificate: '',
  hasSecret: false,
  ...patch,
})

const candidate = (sso?: SSOSettings): Candidate => ({ sso }) as Candidate

beforeEach(() => {
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(rolesApi.getRoles).mockReset().mockResolvedValue({
    roles: [
      { name: 'admin', description: '', scopes: [], builtin: true, users: [] },
      { name: 'readonly', description: '', scopes: [], builtin: true, users: [] },
      { name: 'fwops', description: '', scopes: ['read'], builtin: false, users: [] },
    ],
    scopes: [],
  })
})

describe('buildSSO', () => {
  test('a secret left blank is not sent, so the server keeps the stored one', () => {
    expect(buildSSO(form({ client_secret: '', hasSecret: false })).client_secret).toBeUndefined()
  })

  test('a stored secret hidden behind the blank field is kept', () => {
    expect(buildSSO(form({ client_secret: '', hasSecret: true })).client_secret).toBe('')
  })

  test('a typed secret wins and surrounding spaces go', () => {
    expect(buildSSO(form({ client_secret: ' s3cret ' })).client_secret).toBe('s3cret')
  })

  test('empty optional fields stay unset', () => {
    const built = buildSSO(form({ name: '', scopes: '', roles: [] }))
    expect('name' in built).toBe(false)
    expect('scopes' in built).toBe(false)
    expect('roles' in built).toBe(false)
  })

  test('scopes become a list of words', () => {
    expect(buildSSO(form({ scopes: 'profile  email' })).scopes).toEqual(['profile', 'email'])
  })

  test('a half-filled role map row is dropped', () => {
    const built = buildSSO(form({ roles: [{ group: 'a', role: 'admin' }, { group: '', role: '' }] }))
    expect(built.roles).toEqual([{ group: 'a', role: 'admin' }])
  })
})

describe('formFromSSO', () => {
  test('reads the stored section and treats a blank secret next to an issuer as hidden', () => {
    const f = formFromSSO({ enabled: true, issuer: 'https://id/x', client_secret: '' })
    expect(f.issuer).toBe('https://id/x')
    expect(f.hasSecret).toBe(true)
    expect(f.client_secret).toBe('')
  })

  test('no section at all gives an empty form', () => {
    expect(formFromSSO(undefined).enabled).toBe(false)
  })
})

describe('ssoRedirect', () => {
  test('the address this browser uses', () => {
    expect(ssoRedirect(undefined, 'https://gate.example.com')).toBe('https://gate.example.com/api/v1/auth/sso/callback')
  })

  test('a configured redirect URL wins', () => {
    expect(ssoRedirect({ enabled: true, redirect_url: 'https://fw.example.com/api/v1/auth/sso/callback' }, 'https://10.0.0.1')).toBe(
      'https://fw.example.com/api/v1/auth/sso/callback',
    )
  })
})

describe('validateSSO', () => {
  test('nothing to check while single sign-on is off', () => {
    expect(validateSSO(form({ enabled: false, issuer: '', client_id: '' }), ['admin'])).toEqual({})
  })

  test('mirrors the server: issuer must be https and a client id is required', () => {
    const errs = validateSSO(form({ issuer: 'http://id.example.com', client_id: '  ' }), ['admin'])
    expect(errs.issuer).toMatch(/https URL/)
    expect(errs.client_id).toMatch(/required/)
  })

  test('a mapping needs a group and a known role', () => {
    const errs = validateSSO(form({ roles: [{ group: ' ', role: 'ghost' }] }), ['admin', 'readonly'])
    expect(errs['roles[0].group']).toMatch(/group is required/)
    expect(errs['roles[0].role']).toMatch(/unknown role/)
  })

  test('with no mapping and no default role nobody could sign in', () => {
    expect(validateSSO(form({ roles: [], default_role: '' }), ['admin']).roles).toMatch(/at least one group/)
  })

  test('a custom role from the roles API is accepted', () => {
    expect(validateSSO(form({ roles: [{ group: 'g', role: 'fwops' }] }), ['admin', 'fwops'])).toEqual({})
  })

  test('a redirect URL must be the gate callback over https', () => {
    expect(validateSSO(form({ redirect_url: 'http://gate.example.com/api/v1/auth/sso/callback' }), ['admin']).redirect_url).toMatch(/callback/)
    expect(validateSSO(form({ redirect_url: 'https://gate.example.com/other' }), ['admin']).redirect_url).toMatch(/callback/)
    expect(validateSSO(form({ redirect_url: 'https://gate.example.com/api/v1/auth/sso/callback' }), ['admin'])).toEqual({})
  })
})

describe('SSOSection', () => {
  const wrap = () => render(<MemoryRouter><SSOSection /></MemoryRouter>)

  test('shows the stored settings and the redirect URL to register', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue(
      candidate({ enabled: true, name: 'Authentik', issuer: 'https://id.example.com/realms/main', client_id: 'gate', client_secret: '', roles: [{ group: 'fw-admins', role: 'admin' }] }),
    )
    wrap()
    expect(await screen.findByRole('heading', { name: 'Single sign-on' })).toBeInTheDocument()
    expect(screen.getByLabelText('Issuer URL')).toHaveValue('https://id.example.com/realms/main')
    expect(screen.getByLabelText('Client secret')).toHaveValue('')
    expect(screen.getByLabelText('Client secret')).toHaveAttribute('placeholder', 'unchanged')
    expect(screen.getByText(`${window.location.origin}/api/v1/auth/sso/callback`)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Copy/ })).toBeInTheDocument()
  })

  test('saves the form as the sso section and offers the commit hint', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue(candidate())
    wrap()
    await screen.findByRole('heading', { name: 'Single sign-on' })
    fireEvent.click(screen.getByLabelText('Sign in with an identity provider (single sign-on)'))
    fireEvent.change(screen.getByLabelText('Issuer URL'), { target: { value: 'https://id.example.com' } })
    fireEvent.change(screen.getByLabelText('Client ID'), { target: { value: 'gate' } })
    fireEvent.change(screen.getByLabelText('Client secret'), { target: { value: 'shh' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add a group' }))
    fireEvent.change(screen.getByLabelText('Provider group 1'), { target: { value: 'fw-admins' } })
    fireEvent.change(screen.getByLabelText('Gate role 1'), { target: { value: 'admin' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(api.patchSection).toHaveBeenCalledWith('sso', {
        enabled: true,
        issuer: 'https://id.example.com',
        client_id: 'gate',
        client_secret: 'shh',
        roles: [{ group: 'fw-admins', role: 'admin' }],
      }),
    )
    expect(await screen.findByRole('status')).toHaveTextContent('Saved. Use the commit bar to apply.')
  })

  test('a validation error from the server lands on its field', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue(candidate())
    vi.mocked(api.patchSection).mockResolvedValue({
      status: 'ok',
      validation_errors: [{ path: 'sso.roles[0].role', message: 'unknown role "ghost"' }],
    })
    wrap()
    await screen.findByRole('heading', { name: 'Single sign-on' })
    fireEvent.click(screen.getByLabelText('Sign in with an identity provider (single sign-on)'))
    fireEvent.change(screen.getByLabelText('Issuer URL'), { target: { value: 'https://id.example.com' } })
    fireEvent.change(screen.getByLabelText('Client ID'), { target: { value: 'gate' } })
    fireEvent.click(screen.getByRole('button', { name: 'Add a group' }))
    fireEvent.change(screen.getByLabelText('Provider group 1'), { target: { value: 'g' } })
    fireEvent.change(screen.getByLabelText('Gate role 1'), { target: { value: 'admin' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('unknown role "ghost"')
  })

  test('an unknown issuer is refused before saving', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue(candidate())
    wrap()
    await screen.findByRole('heading', { name: 'Single sign-on' })
    fireEvent.click(screen.getByLabelText('Sign in with an identity provider (single sign-on)'))
    // enabled with nothing filled in: several fields are wrong at once, so read
    // the one under the issuer field instead of the first alert on the page
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    const issuer = await screen.findByLabelText('Issuer URL')
    expect(issuer.parentElement).toHaveTextContent('issuer must be an https URL')
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('a half-filled saved row can be cleared so the form saves again', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue(
      candidate({ enabled: true, issuer: 'https://id.example.com', client_id: 'gate', roles: [{ group: 'g', role: '' }] }),
    )
    wrap()
    await screen.findByRole('heading', { name: 'Single sign-on' })
    fireEvent.click(screen.getByRole('button', { name: 'Remove these groups' }))
    fireEvent.change(screen.getByLabelText('Role for a user in none of these groups'), { target: { value: 'readonly' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(api.patchSection).toHaveBeenCalledWith('sso', {
        enabled: true,
        issuer: 'https://id.example.com',
        client_id: 'gate',
        // the issuer was loaded with a blank (hidden) secret, so it goes back blank
        client_secret: '',
        default_role: 'readonly',
      }),
    )
  })

  test('a role an SSO user may get is listed in the pickers', async () => {
    vi.mocked(api.getCandidate).mockResolvedValue(candidate({ enabled: true, issuer: 'https://id.example.com', client_id: 'gate', roles: [{ group: 'g', role: 'fwops' }] }))
    wrap()
    await screen.findByRole('heading', { name: 'Single sign-on' })
    const select = screen.getByLabelText('Gate role 1')
    await waitFor(() => expect(within(select).getByRole('option', { name: 'fwops' })).toBeInTheDocument())
    expect(within(select).getByRole('option', { name: 'fwops' })).toHaveValue('fwops')
  })
})
