// System API: admins, API tokens, backup/restore and system info.
// Types mirror internal/api/system_handlers.go and auth/tokens.go by JSON name.
import { apiClient } from './client'
import { csrfToken } from './auth'

export interface AdminInfo {
  username: string
  role: 'admin' | 'readonly'
  has_password: boolean
}

export interface TokenInfo {
  id: string
  name: string
  user: string
  role: string
  created: string
  last_used?: string
}

// --- Admins -----------------------------------------------------------------

export const getAdmins = () =>
  apiClient<{ admins: AdminInfo[] }>('/api/v1/admins').then((r) => r.admins ?? [])

export function createAdmin(body: { username: string; role: string; password: string }): Promise<AdminInfo> {
  return apiClient<{ admin: AdminInfo }>('/api/v1/admins', {
    method: 'POST',
    body: JSON.stringify(body),
  }).then((r) => r.admin)
}

export const updateAdmin = (name: string, body: { role?: string; password?: string }) =>
  apiClient<{ status: string }>(`/api/v1/admins/${encodeURIComponent(name)}`, {
    method: 'PATCH',
    body: JSON.stringify(body),
  })

export const deleteAdmin = (name: string) =>
  apiClient<{ status: string }>(`/api/v1/admins/${encodeURIComponent(name)}`, { method: 'DELETE' })

// --- API tokens ---------------------------------------------------------------

export const getTokens = () =>
  apiClient<{ tokens: TokenInfo[] }>('/api/v1/auth/tokens').then((r) => r.tokens ?? [])

export function createToken(body: { name: string; readonly?: boolean }): Promise<{ token: string; info: TokenInfo }> {
  return apiClient<{ token: string; info: TokenInfo }>('/api/v1/auth/tokens', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

export const deleteToken = (id: string) =>
  apiClient<{ status: string }>(`/api/v1/auth/tokens/${encodeURIComponent(id)}`, { method: 'DELETE' })

// --- Backup / restore ---------------------------------------------------------

export async function downloadBackup(): Promise<void> {
  const res = await fetch('/api/v1/system/backup', { credentials: 'same-origin' })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const blob = await res.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `goblin-gate-backup-${new Date().toISOString().slice(0, 10)}.zip`
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

export function restoreBackup(file: File): Promise<{ status: string; hostname?: string; message?: string }> {
  const form = new FormData()
  form.append('backup', file)
  return fetch('/api/v1/system/restore', {
    method: 'POST',
    body: form,
    credentials: 'same-origin',
    headers: { 'X-CSRF-Token': csrfToken() },
  }).then(async (res) => {
    if (!res.ok) {
      let message = `HTTP ${res.status}`
      try {
        const body = await res.json()
        if (body?.error?.message) message = body.error.message
      } catch { /* not JSON */ }
      throw new Error(message)
    }
    return res.json() as Promise<{ status: string; hostname?: string; message?: string }>
  })
}

// --- System info ----------------------------------------------------------------

export interface SystemInfo {
  version: string
  time: string
  hostname?: string
  kernel?: string
  uptime_seconds?: number
  timezone?: string
  ntp_synchronized?: boolean
}

export const getSystemInfo = () => apiClient<SystemInfo>('/api/v1/system/info')
