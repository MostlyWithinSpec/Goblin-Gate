import React, { useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { login } from '../api/auth'

const Login: React.FC = () => {
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [totpStep, setTotpStep] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const codeRef = useRef<HTMLInputElement>(null)
  const navigate = useNavigate()
  const location = useLocation()

  useEffect(() => {
    if (totpStep) codeRef.current?.focus()
  }, [totpStep])

  const finish = () => {
    const from = (location.state as { from?: string } | null)?.from
    navigate(from && from !== '/login' ? from : '/', { replace: true })
  }

  const handlePasswordSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!username || !password) {
      setError('Please enter both username and password')
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
      setError(`Cannot reach Goblin Gate: ${(err as Error).message}`)
    } finally {
      setBusy(false)
    }
  }

  const handleCodeSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!code) {
      setError('Enter the code from your authenticator app')
      return
    }
    setBusy(true)
    setError('')
    try {
      const res = await login(username, password, code)
      if (res.ok) finish()
      else if (res.code === 'totp_invalid') {
        setCode('')
        setError('That code is not right. Codes change every 30 seconds.')
      } else {
        // The credentials stopped matching; back to the first step.
        setTotpStep(false)
        setPassword('')
        setError(res.message)
      }
    } catch (err) {
      setError(`Cannot reach Goblin Gate: ${(err as Error).message}`)
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
            {totpStep ? 'Two-factor login' : 'Sign in to your account'}
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
                Username
              </label>
              <input
                id="username"
                type="text"
                autoComplete="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                className={inputClass}
                placeholder="Enter your username"
              />
            </div>
            <div className="mb-6">
              <label htmlFor="password" className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                Password
              </label>
              <input
                id="password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className={inputClass}
                placeholder="Enter your password"
              />
            </div>
            <button
              type="submit"
              disabled={busy}
              className="w-full bg-blue-600 hover:bg-blue-700 text-white py-2 px-4 rounded-md shadow-sm font-medium transition-colors disabled:opacity-50"
            >
              {busy ? 'Signing in…' : 'Sign in'}
            </button>
          </form>
        ) : (
          <form onSubmit={handleCodeSubmit}>
            <div className="mb-6">
              <label htmlFor="totp-code" className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                Code from your authenticator app
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
              className="w-full bg-blue-600 hover:bg-blue-700 text-white py-2 px-4 rounded-md shadow-sm font-medium transition-colors disabled:opacity-50"
            >
              {busy ? 'Verifying…' : 'Verify'}
            </button>
          </form>
        )}

        <p className="mt-6 text-center text-sm text-gray-600 dark:text-gray-400">
          Forgot the password? Use "Reset the admin password" on the box&apos;s console menu.
        </p>
      </div>
    </div>
  )
}

export default Login
