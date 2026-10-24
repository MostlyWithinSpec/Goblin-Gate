import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { ChevronDown, LogOut, Power, User } from 'lucide-react'
import { getMe, logout, type Me } from '../api/auth'
import { getSystemStatus } from '../api/config'
import { rebootBox } from '../api/system'

// UserMenu: who is signed in, as a button that opens a small menu with
// "Log out" and, for admins, "Restart Goblin Gate…" (behind a confirmation:
// a restart cuts the network for about a minute). In --dev mode (no
// authentication) the server reports a stand-in "dev" user.
export default function UserMenu() {
  const [me, setMe] = useState<Me | null>(null)
  const [open, setOpen] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [restarting, setRestarting] = useState<'no' | 'waiting' | 'back' | 'failed'>('no')
  const [error, setError] = useState<string | null>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const navigate = useNavigate()

  useEffect(() => {
    getMe().then(setMe).catch(() => setMe(null))
  }, [])

  // close on a click elsewhere and on Escape
  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  const signOut = async () => {
    setOpen(false)
    await logout()
    navigate('/login', { replace: true })
  }

  const restart = async () => {
    setError(null)
    try {
      await rebootBox()
    } catch (e) {
      setError((e as Error).message)
      return
    }
    setConfirming(false)
    setRestarting('waiting')
    // wait for it to go away and come back (up to 5 minutes)
    const started = Date.now()
    let wentDown = false
    const poll = async () => {
      try {
        await getSystemStatus()
        if (wentDown || Date.now() - started > 90_000) {
          setRestarting('back')
          return
        }
      } catch {
        wentDown = true
      }
      if (Date.now() - started > 300_000) {
        setRestarting('failed')
        return
      }
      setTimeout(poll, 3000)
    }
    setTimeout(poll, 5000)
  }

  const canRestart = me?.role === 'admin'
  const canSignOut = me?.method === 'session'

  return (
    <div className="relative text-sm text-gray-700 dark:text-gray-300" ref={menuRef}>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Account menu"
        className="inline-flex h-10 items-center gap-1 rounded-full bg-gray-200 px-3 hover:bg-gray-300 dark:bg-gray-700 dark:hover:bg-gray-600"
      >
        <User size={16} />
        <span>{me?.user ?? '…'}</span>
        {me?.role === 'readonly' && <span className="text-xs text-gray-500">(read-only)</span>}
        <ChevronDown size={14} />
      </button>
      {open && (
        <div role="menu" className="absolute right-0 z-50 mt-1 w-56 overflow-hidden rounded-md border border-gray-200 bg-white shadow-lg dark:border-gray-700 dark:bg-gray-800">
          {canRestart && (
            <button
              type="button"
              role="menuitem"
              onClick={() => {
                setOpen(false)
                setConfirming(true)
              }}
              className="flex w-full items-center gap-2 px-3 py-2.5 text-left hover:bg-gray-100 dark:hover:bg-gray-700"
            >
              <Power size={16} /> Restart Goblin Gate…
            </button>
          )}
          {canSignOut && (
            <button
              type="button"
              role="menuitem"
              onClick={signOut}
              className="flex w-full items-center gap-2 px-3 py-2.5 text-left hover:bg-gray-100 dark:hover:bg-gray-700"
            >
              <LogOut size={16} /> Log out
            </button>
          )}
          {!canRestart && !canSignOut && <p className="px-3 py-2.5 text-gray-500">Nothing to do here.</p>}
        </div>
      )}

      {/* in a portal: the header's backdrop blur would trap a fixed overlay inside it */}
      {confirming && createPortal(
        <div className="fixed inset-0 z-[70] flex items-center justify-center bg-black/50 px-4" role="presentation">
          <div role="alertdialog" aria-modal="true" aria-labelledby="restart-title" className="w-full max-w-sm rounded-lg bg-white p-5 shadow-xl dark:bg-gray-800">
            <h2 id="restart-title" className="text-lg font-semibold text-gray-900 dark:text-white">
              Restart Goblin Gate?
            </h2>
            <p className="mt-2 text-sm text-gray-600 dark:text-gray-300">
              The internet and everything the box serves (DHCP, DNS, VPN) stop for about a minute while it restarts.
            </p>
            {error && (
              <p role="alert" className="mt-2 text-sm text-red-600">
                {error}
              </p>
            )}
            <div className="mt-4 flex justify-end gap-2">
              <button
                type="button"
                autoFocus
                onClick={() => setConfirming(false)}
                className="rounded border border-gray-300 px-3 py-1.5 text-sm dark:border-gray-600"
              >
                Cancel
              </button>
              <button type="button" onClick={restart} className="rounded bg-red-600 px-3 py-1.5 text-sm text-white hover:bg-red-700">
                Restart
              </button>
            </div>
          </div>
        </div>,
        document.body,
      )}

      {restarting !== 'no' && createPortal(
        <div className="fixed inset-0 z-[70] flex items-center justify-center bg-black/50 px-4">
          <div role="status" className="w-full max-w-sm rounded-lg bg-white p-5 text-sm shadow-xl dark:bg-gray-800 dark:text-gray-200">
            {restarting === 'waiting' && <p>Restarting… this page reconnects when the box is back.</p>}
            {restarting === 'back' && (
              <>
                <p>Goblin Gate is back.</p>
                <button type="button" onClick={() => window.location.reload()} className="mt-3 rounded bg-blue-600 px-3 py-1.5 text-white">
                  Reload
                </button>
              </>
            )}
            {restarting === 'failed' && (
              <>
                <p>The box has not answered for 5 minutes. Check its console.</p>
                <div className="mt-3 flex gap-2">
                  <button type="button" onClick={() => window.location.reload()} className="rounded bg-blue-600 px-3 py-1.5 text-white">
                    Reload
                  </button>
                  <button type="button" onClick={() => setRestarting('no')} className="rounded border border-gray-300 px-3 py-1.5 dark:border-gray-600">
                    Close
                  </button>
                </div>
              </>
            )}
          </div>
        </div>,
        document.body,
      )}
    </div>
  )
}
