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
})
