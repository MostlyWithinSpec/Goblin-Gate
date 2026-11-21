import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import '@testing-library/jest-dom'
import { MemoryRouter, Route, Routes } from 'react-router-dom'

vi.mock('../api/auth', () => ({ login: vi.fn(), getSSOStatus: vi.fn() }))
import * as api from '../api/auth'
import Login from '../pages/Login'

const renderLogin = (path = '/login') =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/" element={<p>dashboard</p>} />
      </Routes>
    </MemoryRouter>,
  )

const signIn = (password: string) => {
  fireEvent.change(screen.getByLabelText(/Password/i), { target: { value: password } })
  fireEvent.click(screen.getByRole('button', { name: /Sign in/i }))
}

describe('Login', () => {
  beforeEach(() => {
    vi.mocked(api.login).mockReset()
    vi.mocked(api.getSSOStatus).mockReset().mockResolvedValue({ enabled: false, name: '' })
  })

  test('renders login form', () => {
    renderLogin()
    expect(screen.getByRole('heading', { name: /Goblin Gate/i })).toBeInTheDocument()
    expect(screen.getByLabelText(/Username/i)).toHaveValue('admin')
    expect(screen.getByLabelText(/Password/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Sign in/i })).toBeInTheDocument()
  })

  test('shows error when submitting empty form', () => {
    renderLogin()
    fireEvent.change(screen.getByLabelText(/Username/i), { target: { value: '' } })
    fireEvent.click(screen.getByRole('button', { name: /Sign in/i }))
    expect(screen.getByText(/Please enter both username and password/i)).toBeInTheDocument()
    expect(api.login).not.toHaveBeenCalled()
  })

  test('signs in and goes to the dashboard', async () => {
    vi.mocked(api.login).mockResolvedValue({ ok: true, me: { user: 'admin', role: 'admin', method: 'session' } })
    renderLogin()
    signIn('correct horse battery')
    expect(await screen.findByText('dashboard')).toBeInTheDocument()
    expect(api.login).toHaveBeenCalledWith('admin', 'correct horse battery')
  })

  test('shows the server message on failure and clears the password', async () => {
    vi.mocked(api.login).mockResolvedValue({ ok: false, message: 'Too many failed attempts. Try again in 15 minutes.' })
    renderLogin()
    signIn('wrong')
    expect(await screen.findByRole('alert')).toHaveTextContent('Too many failed attempts')
    expect(screen.getByLabelText(/Password/i)).toHaveValue('')
  })

  test('two-factor: asks for the code, then sends username, password and code', async () => {
    vi.mocked(api.login)
      .mockResolvedValueOnce({ ok: false, message: 'This account uses two-factor login. Enter the code from your authenticator app.', code: 'totp_required' })
      .mockResolvedValueOnce({ ok: true, me: { user: 'admin', role: 'admin', method: 'session' } })
    renderLogin()
    signIn('pw1234')
    const codeInput = await screen.findByLabelText(/Code from your authenticator app/i)
    expect(codeInput).toHaveAttribute('autocomplete', 'one-time-code')
    fireEvent.change(codeInput, { target: { value: '123456' } })
    fireEvent.click(screen.getByRole('button', { name: /Verify/i }))
    expect(await screen.findByText('dashboard')).toBeInTheDocument()
    expect(api.login).toHaveBeenNthCalledWith(2, 'admin', 'pw1234', '123456')
  })

  test('two-factor: an invalid code keeps the second step and shows the message', async () => {
    vi.mocked(api.login)
      .mockResolvedValueOnce({ ok: false, message: 'This account uses two-factor login. Enter the code from your authenticator app.', code: 'totp_required' })
      .mockResolvedValueOnce({ ok: false, message: 'That code is not right. Codes change every 30 seconds.', code: 'totp_invalid' })
    renderLogin()
    signIn('pw1234')
    const codeInput = await screen.findByLabelText(/Code from your authenticator app/i)
    fireEvent.change(codeInput, { target: { value: '654321' } })
    fireEvent.click(screen.getByRole('button', { name: /Verify/i }))
    expect(await screen.findByRole('alert')).toHaveTextContent('That code is not right. Codes change every 30 seconds.')
    // still on the second step, code cleared to try again
    expect(screen.getByLabelText(/Code from your authenticator app/i)).toHaveValue('')
  })

  test('no single sign-on button while it is off', async () => {
    renderLogin()
    await waitFor(() => expect(api.getSSOStatus).toHaveBeenCalled())
    expect(screen.queryByRole('button', { name: /Sign in with/i })).toBeNull()
  })

  test('single sign-on: the button uses the provider name and navigates for real', async () => {
    vi.mocked(api.getSSOStatus).mockResolvedValue({ enabled: true, name: 'Authentik' })
    // jsdom cannot navigate: swap window.location for a plain object that
    // records the href the page sets.
    const descriptor = Object.getOwnPropertyDescriptor(window, 'location')
    const stub = { href: '', origin: 'http://localhost:3000' }
    const w = window as unknown as { location: unknown }
    delete w.location
    w.location = stub
    try {
      renderLogin()
      const btn = await screen.findByRole('button', { name: 'Sign in with Authentik' })
      // the local login stays next to it
      expect(screen.getByLabelText(/Username/i)).toBeInTheDocument()
      fireEvent.click(btn)
      expect(stub.href).toBe('/api/v1/auth/sso/login')
    } finally {
      delete w.location
      if (descriptor) Object.defineProperty(window, 'location', descriptor)
    }
  })

  test('single sign-on: a failed attempt shows a message for its code', async () => {
    renderLogin('/login?sso_error=no_role')
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Your groups do not have a role on this firewall. Ask an administrator.',
    )
  })

  test('single sign-on: every code the gate sends back has its own message', async () => {
    const messages: [string, string][] = [
      ['disabled', 'Single sign-on is off now.'],
      ['state', 'That sign-in took too long'],
      ['provider', 'cannot reach the identity provider'],
      ['exchange', 'did not accept the sign-in code'],
      ['token', "did not check out"],
      ['refused', 'did not let you sign in'],
      ['no_username', 'did not give a user name'],
      ['local_user', 'A local account has the same name'],
      ['no_role', 'groups do not have a role'],
      ['too_many_attempts', 'Too many failed sign-ins'],
      ['busy', 'Too many sign-ins are in progress'],
    ]
    for (const [code, text] of messages) {
      const { unmount } = renderLogin(`/login?sso_error=${code}`)
      expect(await screen.findByRole('alert')).toHaveTextContent(text)
      unmount()
    }
  })

  test('single sign-on: an unknown code is shown as such', async () => {
    renderLogin('/login?sso_error=weird_code')
    expect(await screen.findByRole('alert')).toHaveTextContent('weird_code')
  })
})
