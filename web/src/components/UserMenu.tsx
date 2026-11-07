import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useNavigate } from 'react-router-dom'
import { ChevronDown, LogOut, Power, User } from 'lucide-react'
import { getMe, logout, type Me } from '../api/auth'
import { getSystemStatus } from '../api/config'
import { rebootBox } from '../api/system'
import { getPrefs, savePrefs } from '../api/prefs'
import { language, languages, setLanguage, t } from '../i18n'

// UserMenu: who is signed in, as a button that opens a small menu with
// the language (when there is more than one), "Log out" and, for admins,
// "Restart Goblin Gate…" (behind a confirmation: a restart cuts the network
// for about a minute). In --dev mode (no authentication) the server reports
// a stand-in "dev" user. The language is stored per admin on the box.
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
    // the admin's stored language wins over this browser's last one
    getPrefs()
      .then((p) => {
        if (p.language && p.language !== language()) return setLanguage(p.language)
      })
      .catch(() => { /* no stored preference */ })
  }, [])

  const langs = languages()
  const changeLanguage = async (code: string) => {
    setOpen(false)
    await setLanguage(code)
    try {
      const p = await getPrefs().catch(() => ({}))
      await savePrefs({ ...p, language: code })
    } catch {
      /* kept in this browser only */
    }
  }

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
        aria-label={t('menu.account')}
        className="inline-flex h-10 items-center gap-1 rounded-full bg-gray-200 px-3 hover:bg-gray-300 dark:bg-gray-700 dark:hover:bg-gray-600"
      >
        <User size={16} />
        <span>{me?.user ?? '…'}</span>
        {me?.role === 'readonly' && <span className="text-xs text-gray-500">{t('menu.readOnly')}</span>}
        <ChevronDown size={14} />
      </button>
      {open && (
        <div role="menu" className="absolute right-0 z-50 mt-1 w-56 overflow-hidden rounded-md border border-gray-200 bg-white shadow-lg dark:border-gray-700 dark:bg-gray-800">
          {langs.length > 1 && (
            <label className="block border-b border-gray-200 px-3 py-2.5 dark:border-gray-700">
              <span className="mb-1 block text-xs text-gray-500 dark:text-gray-400">{t('menu.language')}</span>
              <select
                value={language()}
                onChange={(e) => changeLanguage(e.target.value)}
                className="w-full rounded border border-gray-300 bg-white px-2 py-1 dark:border-gray-600 dark:bg-gray-900"
              >
                {langs.map((l) => (
                  <option key={l.code} value={l.code}>
                    {l.name}
                  </option>
                ))}
              </select>
            </label>
          )}
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
              <Power size={16} /> {t('menu.restart')}
            </button>
          )}
          {canSignOut && (
            <button
              type="button"
              role="menuitem"
              onClick={signOut}
              className="flex w-full items-center gap-2 px-3 py-2.5 text-left hover:bg-gray-100 dark:hover:bg-gray-700"
            >
              <LogOut size={16} /> {t('menu.logOut')}
            </button>
          )}
          {!canRestart && !canSignOut && langs.length < 2 && <p className="px-3 py-2.5 text-gray-500">{t('menu.nothing')}</p>}
        </div>
      )}

      {/* in a portal: the header's backdrop blur would trap a fixed overlay inside it */}
      {confirming && createPortal(
        <div className="fixed inset-0 z-[70] flex items-center justify-center bg-black/50 px-4" role="presentation">
          <div role="alertdialog" aria-modal="true" aria-labelledby="restart-title" className="w-full max-w-sm rounded-lg bg-white p-5 shadow-xl dark:bg-gray-800">
            <h2 id="restart-title" className="text-lg font-semibold text-gray-900 dark:text-white">
              {t('restart.title')}
            </h2>
            <p className="mt-2 text-sm text-gray-600 dark:text-gray-300">
              {t('restart.warning')}
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
                {t('common.cancel')}
              </button>
              <button type="button" onClick={restart} className="rounded bg-red-600 px-3 py-1.5 text-sm text-white hover:bg-red-700">
                {t('restart.confirm')}
              </button>
            </div>
          </div>
        </div>,
        document.body,
      )}

      {restarting !== 'no' && createPortal(
        <div className="fixed inset-0 z-[70] flex items-center justify-center bg-black/50 px-4">
          <div role="status" className="w-full max-w-sm rounded-lg bg-white p-5 text-sm shadow-xl dark:bg-gray-800 dark:text-gray-200">
            {restarting === 'waiting' && <p>{t('restart.waiting')}</p>}
            {restarting === 'back' && (
              <>
                <p>{t('restart.back')}</p>
                <button type="button" onClick={() => window.location.reload()} className="mt-3 rounded bg-blue-600 px-3 py-1.5 text-white">
                  {t('common.reload')}
                </button>
              </>
            )}
            {restarting === 'failed' && (
              <>
                <p>{t('restart.failed')}</p>
                <div className="mt-3 flex gap-2">
                  <button type="button" onClick={() => window.location.reload()} className="rounded bg-blue-600 px-3 py-1.5 text-white">
                    {t('common.reload')}
                  </button>
                  <button type="button" onClick={() => setRestarting('no')} className="rounded border border-gray-300 px-3 py-1.5 dark:border-gray-600">
                    {t('common.close')}
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
