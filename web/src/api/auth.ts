// Login, logout and the current user (internal/api/auth_handlers.go).
// The server sets two cookies on login: gg_session (HttpOnly) and gg_csrf,
// which apiClient echoes in X-CSRF-Token on every request. Plain fetch is
// used here because apiClient redirects to /login on 401.

export interface Me {
  user: string
  role: 'admin' | 'readonly'
  method: 'session' | 'token' | 'socket' | 'none'
}

export type LoginResult = { ok: true; me: Me } | { ok: false; message: string; retryAfter?: number }

export async function login(username: string, password: string): Promise<LoginResult> {
  const res = await fetch('/api/v1/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  const body = await res.json().catch(() => ({}))
  if (res.ok) return { ok: true, me: { user: body.user, role: body.role, method: 'session' } }
  if (res.status === 429) {
    const retryAfter = Number(res.headers.get('Retry-After')) || undefined
    const mins = retryAfter ? Math.ceil(retryAfter / 60) : 15
    return { ok: false, message: `Too many failed attempts. Try again in ${mins} minute${mins === 1 ? '' : 's'}.`, retryAfter }
  }
  if (res.status === 401) return { ok: false, message: 'Wrong user name or password.' }
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
