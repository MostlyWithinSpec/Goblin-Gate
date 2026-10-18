// Login, logout and the current user (internal/api/auth_handlers.go).
// The server sets two cookies on login: gg_session (HttpOnly) and gg_csrf,
// which apiClient echoes in X-CSRF-Token on every request. Plain fetch is
// used here because apiClient redirects to /login on 401.

import { apiClient } from './client'

export interface Me {
  user: string
  role: 'admin' | 'readonly'
  method: 'session' | 'token' | 'socket' | 'none'
  totp_enabled?: boolean
}

export type LoginResult =
  | { ok: true; me: Me }
  | { ok: false; message: string; retryAfter?: number; code?: 'totp_required' | 'totp_invalid' }

export async function login(username: string, password: string, code?: string): Promise<LoginResult> {
  const res = await fetch('/api/v1/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(code ? { username, password, code } : { username, password }),
  })
  const body = await res.json().catch(() => ({}))
  if (res.ok) return { ok: true, me: { user: body.user, role: body.role, method: 'session', totp_enabled: body.totp_enabled ?? undefined } }
  if (res.status === 429) {
    const retryAfter = Number(res.headers.get('Retry-After')) || undefined
    const mins = retryAfter ? Math.ceil(retryAfter / 60) : 15
    return { ok: false, message: `Too many failed attempts. Try again in ${mins} minute${mins === 1 ? '' : 's'}.`, retryAfter }
  }
  if (res.status === 401) {
    const errCode = body?.error?.code
    if (errCode === 'totp_required') return { ok: false, message: 'This account uses two-factor login. Enter the code from your authenticator app.', code: 'totp_required' }
    if (errCode === 'totp_invalid') return { ok: false, message: 'That code is not right. Codes change every 30 seconds.', code: 'totp_invalid' }
    return { ok: false, message: 'Wrong user name or password.' }
  }
  return { ok: false, message: body?.error?.message ?? `Login failed (${res.status})` }
}

export function csrfToken(): string {
  const m = /(?:^|;\s*)gg_csrf=([^;]*)/.exec(document.cookie)
  return m ? decodeURIComponent(m[1]) : ''
}

export async function logout(): Promise<void> {
  await fetch('/api/v1/auth/logout', { method: 'POST', headers: { 'X-CSRF-Token': csrfToken() } }).catch(() => {})
}

// getMe returns the signed-in user, or null when not signed in.
export async function getMe(): Promise<Me | null> {
  const res = await fetch('/api/v1/auth/me')
  if (!res.ok) return null
  return res.json()
}

// Two-factor (TOTP) login management (internal/api/twofactor_handlers.go).
// These use apiClient because they only run while signed in; the server's
// error codes surface as APIRequestError.code.

export interface TwoFactorSetup {
  secret: string
  otpauth_url: string
  qr_svg: string
}

// setup2FA starts enrollment: the server stores a pending secret for ten minutes.
export function setup2FA(): Promise<TwoFactorSetup> {
  return apiClient<TwoFactorSetup>('/api/v1/auth/2fa/setup', { method: 'POST' })
}

// enable2FA confirms enrollment with a code from the authenticator app.
export function enable2FA(code: string): Promise<{ status: string }> {
  return apiClient<{ status: string }>('/api/v1/auth/2fa/enable', { method: 'POST', body: JSON.stringify({ code }) })
}

// disable2FA turns two-factor off; the current password is required.
export function disable2FA(password: string): Promise<{ status: string }> {
  return apiClient<{ status: string }>('/api/v1/auth/2fa/disable', { method: 'POST', body: JSON.stringify({ password }) })
}
