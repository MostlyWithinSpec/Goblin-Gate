import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'

vi.mock('../api/auth', async (orig) => {
  const real = await orig<typeof import('../api/auth')>()
  return { ...real, getMe: vi.fn(), logout: vi.fn() }
})
vi.mock('../api/system', async (orig) => {
  const real = await orig<typeof import('../api/system')>()
  return { ...real, rebootBox: vi.fn() }
})
import * as auth from '../api/auth'
import * as sys from '../api/system'
import UserMenu from '../components/UserMenu'

const show = () => render(<MemoryRouter><UserMenu /></MemoryRouter>)
const openMenu = async () => fireEvent.click(await screen.findByRole('button', { name: 'Account menu' }))

beforeEach(() => {
  vi.mocked(auth.getMe).mockReset().mockResolvedValue({ user: 'admin', role: 'admin', method: 'session' })
  vi.mocked(auth.logout).mockReset().mockResolvedValue(undefined)
  vi.mocked(sys.rebootBox).mockReset().mockResolvedValue({ status: 'restarting' })
})

describe('UserMenu', () => {
  test('the name opens a menu with Log out and Restart', async () => {
    show()
    await waitFor(() => expect(screen.getByText('admin')).toBeInTheDocument())
    expect(screen.queryByRole('menu')).toBeNull()
    await openMenu()
    expect(screen.getByRole('menuitem', { name: /Log out/ })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: /Restart Goblin Gate/ })).toBeInTheDocument()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByRole('menu')).toBeNull()
  })

  test('restart asks first; Cancel does nothing', async () => {
    show()
    await openMenu()
    fireEvent.click(screen.getByRole('menuitem', { name: /Restart Goblin Gate/ }))
    expect(screen.getByRole('alertdialog')).toHaveTextContent('stop for about a minute')
    expect(sys.rebootBox).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(sys.rebootBox).not.toHaveBeenCalled()
  })

  test('confirming restarts', async () => {
    show()
    await openMenu()
    fireEvent.click(screen.getByRole('menuitem', { name: /Restart Goblin Gate/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Restart' }))
    await waitFor(() => expect(sys.rebootBox).toHaveBeenCalledTimes(1))
    expect(await screen.findByRole('status')).toHaveTextContent('Restarting')
  })

  test('read-only users get no restart', async () => {
    vi.mocked(auth.getMe).mockResolvedValue({ user: 'viewer', role: 'readonly', method: 'session' })
    show()
    await waitFor(() => expect(screen.getByText('viewer')).toBeInTheDocument())
    await openMenu()
    expect(screen.queryByRole('menuitem', { name: /Restart/ })).toBeNull()
    fireEvent.click(screen.getByRole('menuitem', { name: /Log out/ }))
    await waitFor(() => expect(auth.logout).toHaveBeenCalled())
  })
})
