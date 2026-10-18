import { beforeEach, describe, expect, test, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import '@testing-library/jest-dom'
import { MemoryRouter, Route, Routes } from 'react-router-dom'

vi.mock('../api/auth', () => ({ login: vi.fn() }))
import * as api from '../api/auth'
import Login from '../pages/Login'

const renderLogin = () =>
  render(
    <MemoryRouter initialEntries={['/login']}>
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/" element={<p>dashboard</p>} />
      </Routes>
    </MemoryRouter>
  )

const signIn = (password: string) => {
  fireEvent.change(screen.getByLabelText(/Password/i), { target: { value: password } })
  fireEvent.click(screen.getByRole('button', { name: /Sign in/i }))
}

describe('Login', () => {
  beforeEach(() => {
    vi.mocked(api.login).mockReset()
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
})
