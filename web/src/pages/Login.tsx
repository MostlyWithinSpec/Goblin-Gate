import React, { useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { LogIn } from 'lucide-react'
import { getSSOStatus, login } from '../api/auth'
import { t } from '../i18n'

// ssoLoginPath is where the sign-in button takes the browser: the gate starts
// the flow and redirects on to the provider.
export const ssoLoginPath = '/api/v1/auth/sso/login'

// ssoErrorText turns the code the gate sends the browser back with
// (/login?sso_error=<code>) into a message. An unknown code says so plainly:
// the provider or the gate changed, not the user.
const ssoErrorText = (code: string): string => {
  switch (code) {
    case 'disabled':
      return t('login.ssoDisabled')
    case 'state':
      return t('login.ssoState')
    case 'provider':
      return t('login.ssoProvider')
    case 'exchange':
      return t('login.ssoExchange')
    case 'token':
      return t('login.ssoToken')
    case 'refused':
      return t('login.ssoRefused')
    case 'no_username':
      return t('login.ssoNoUsername')
    case 'local_user':
      return t('login.ssoLocalUser')
    case 'no_role':
      return t('login.ssoNoRole')
    case 'too_many_attempts':
      return t('login.ssoTooMany')
    case 'busy':
      return t('login.ssoBusy')
    default:
      return t('login.ssoUnknown', { code })
  }
}

const Login: React.FC = () => {
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [totpStep, setTotpStep] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [sso, setSso] = useState<{ enabled: boolean; name: string }>({ enabled: false, name: '' })
  const codeRef = useRef<HTMLInputElement>(null)
  const navigate = useNavigate()
  const location = useLocation()
  const [params] = useSearchParams()
  const ssoError = params.get('sso_error')

  useEffect(() => {
    getSSOStatus().then(setSso).catch(() => setSso({ enabled: false, name: '' }))
  }, [])

  useEffect(() => {
    if (totpStep) codeRef.current?.focus()
  }, [totpStep])

  // The provider sends a failed sign-in back to /login?sso_error=<code>.
  useEffect(() => {
    if (ssoError) setError(ssoErrorText(ssoError))
  }, [ssoError])

  // The provider page needs a real navigation, not fetch: the browser must
  // follow the redirect to the identity provider and come back with cookies.
  const startSSO = () => {
    window.location.href = ssoLoginPath
  }

  const finish = () => {
    const from = (location.state as { from?: string } | null)?.from
    navigate(from && from !== '/login' ? from : '/', { replace: true })
  }

  const handlePasswordSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!username || !password) {
      setError(t('login.missing'))
      return
    }
    setBusy(true)
    setError('')
    try {
      const res = await login(username, password)
      if (res.ok) finish()
      else if (res.code === 'totp_required') {
        setTotpStep(true)
        setCode('')
        setError('')
      } else {
        setError(res.message)
        setPassword('')
      }
    } catch (err) {
      setError(t('login.unreachable', { error: (err as Error).message }))
    } finally {
      setBusy(false)
    }
  }

  const handleCodeSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!code) {
      setError(t('login.codeMissing'))
      return
    }
    setBusy(true)
    setError('')
    try {
      const res = await login(username, password, code)
      if (res.ok) finish()
      else if (res.code === 'totp_invalid') {
        setCode('')
        setError(t('login.codeWrong'))
      } else {
        // The credentials stopped matching; back to the first step.
        setTotpStep(false)
        setPassword('')
        setError(res.message)
      }
    } catch (err) {
      setError(t('login.unreachable', { error: (err as Error).message }))
    } finally {
      setBusy(false)
    }
  }

  const inputClass =
    'w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-md shadow-sm focus:outline-none focus:ring-blue-500 focus:border-blue-500 dark:bg-gray-700 dark:text-white'

  return (
    <div className="min-h-screen flex items-center justify-center bg-gray-50 dark:bg-transparent p-4">
      <div className="max-w-md w-full bg-white dark:bg-gray-800/90 border border-gray-200 dark:border-gray-700 rounded-xl shadow-lg p-8">
        <div className="text-center mb-8">
          <img src="/goblin-128.png" alt="" className="mx-auto w-24 h-24 mb-4 goblin-glow" />
          <h1 className="text-2xl font-extrabold text-gray-800 dark:text-gray-100">
            Goblin <span className="text-green-600 dark:text-green-400">Gate</span>
          </h1>
          <p className="text-gray-600 dark:text-gray-400 mt-2">
            {totpStep ? t('login.twoFactor') : t('login.subtitle')}
          </p>
        </div>

        {error && (
          <div role="alert" className="mb-4 p-3 bg-red-50 text-red-700 rounded-lg text-sm">
            {error}
          </div>
        )}

        {!totpStep ? (
          <form onSubmit={handlePasswordSubmit}>
            <div className="mb-4">
              <label htmlFor="username" className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                {t('login.username')}
              </label>
              <input
                id="username"
                type="text"
                autoComplete="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                className={inputClass}
                placeholder={t('login.usernamePlaceholder')}
              />
            </div>
            <div className="mb-6">
              <label htmlFor="password" className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                {t('login.password')}
              </label>
              <input
                id="password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className={inputClass}
                placeholder={t('login.passwordPlaceholder')}
              />
            </div>
            <button
              type="submit"
              disabled={busy}
              className="w-full bg-blue-600 hover:bg-blue-700 text-white py-2 px-4 rounded-md shadow-sm font-medium transition-colors disabled:opacity-50 min-h-[40px]"
            >
              {busy ? t('login.signingIn') : t('login.signIn')}
            </button>
          </form>
        ) : (
          <form onSubmit={handleCodeSubmit}>
            <div className="mb-6">
              <label htmlFor="totp-code" className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                {t('login.codeLabel')}
              </label>
              <input
                id="totp-code"
                ref={codeRef}
                type="text"
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={6}
                autoFocus
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))}
                className={inputClass}
                placeholder="123456"
              />
            </div>
            <button
              type="submit"
              disabled={busy}
              className="w-full bg-blue-600 hover:bg-blue-700 text-white py-2 px-4 rounded-md shadow-sm font-medium transition-colors disabled:opacity-50 min-h-[40px]"
            >
              {busy ? t('login.verifying') : t('login.verify')}
            </button>
            <button
              type="button"
              onClick={() => {
                setTotpStep(false)
                setPassword('')
                setCode('')
                setError('')
              }}
              className="mt-3 w-full text-sm text-blue-600 hover:underline dark:text-blue-400"
            >
              {t('login.back')}
            </button>
          </form>
        )}

        {sso.enabled && !totpStep && (
          <div className="mt-6">
            <div className="flex items-center gap-3 text-xs uppercase text-gray-400">
              <span className="h-px flex-1 bg-gray-200 dark:bg-gray-700" />
              {t('login.or')}
              <span className="h-px flex-1 bg-gray-200 dark:bg-gray-700" />
            </div>
            <button
              type="button"
              onClick={startSSO}
              className="mt-3 flex w-full items-center justify-center gap-2 rounded-md border border-gray-300 py-2 px-4 font-medium text-gray-700 transition-colors hover:bg-gray-50 dark:border-gray-600 dark:text-gray-200 dark:hover:bg-gray-700 min-h-[40px]"
            >
              <LogIn size={16} /> {t('login.signInWith', { name: sso.name })}
            </button>
          </div>
        )}

        <p className="mt-6 text-center text-sm text-gray-600 dark:text-gray-400">
          {t('login.forgot')}
        </p>
      </div>
    </div>
  )
}

export default Login
