import type { SetupCurrent } from '../pages/setup/setupLogic'
// First-boot setup API (internal/api/setup.go). These endpoints work without
// a login while no admin has a password, so they use plain fetch instead of
// apiClient (which redirects to /login on 401).
import type { ValidationError } from './policy'

export type SecurityLevel = 'off' | 'basic' | 'strict'

export interface SetupRequest {
  admin_password: string
  hostname?: string
  wan: { device: string; mode: 'dhcp' | 'static'; address?: string; gateway?: string }
  lan: { device: string; address: string; dhcp_start?: string; dhcp_end?: string }
  security_level: SecurityLevel
}

export interface NetDevice {
  name: string
  mac?: string
  up: boolean
}

export type SetupResult = { ok: true; revision: number } | { ok: false; message: string; errors: ValidationError[] }

async function json<T>(res: Response): Promise<T> {
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json() as Promise<T>
}

export async function getSetupState(): Promise<{ needed: boolean; current?: SetupCurrent }> {
  return json(await fetch('/api/v1/setup/state'))
}

export async function getDevices(): Promise<NetDevice[]> {
  const body = await json<{ devices: NetDevice[] }>(await fetch('/api/v1/system/devices'))
  return body.devices ?? []
}

export async function submitSetup(req: SetupRequest): Promise<SetupResult> {
  const res = await fetch('/api/v1/setup', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  const body = await res.json().catch(() => ({}))
  if (res.ok) return { ok: true, revision: body.revision }
  const err = body?.error ?? {}
  return {
    ok: false,
    message: err.message ?? `${res.status} ${res.statusText}`,
    errors: Array.isArray(err.details) ? err.details : [],
  }
}
