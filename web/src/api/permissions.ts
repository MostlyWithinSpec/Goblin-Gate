// Which controls the signed-in user may see (task 150). The server decides
// every request anyway; this only hides what would come back as a 403.
//
// What the effective scopes mean is fixed by internal/api/scopes.go:
//   - editing a config section needs that section's write scope
//     (sectionScopes there),
//   - commit, confirm, discard and rollback need "commit",
//   - blocks, devices, diagnostics and the refreshes need "actions",
//   - accounts, roles, tokens, plugins, updates, reboot, restore, capture and
//     SSH keys are for full admins only (routeScope answers scopeAdmin).
// A full admin reports an empty permissions list and full_admin true, so a
// missing scope never hides something from them.
import { useEffect, useState } from 'react'
import { getMe, type Me } from './auth'
import { APIRequestError } from './client'
import { t } from '../i18n'

export type Scope = 'read' | 'write:firewall' | 'write:network' | 'write:system' | 'actions' | 'commit'

export interface Permissions {
  // can reports whether the user holds a scope. Unknown scopes are allowed:
  // only the six known ones gate a control.
  can: (scope: string) => boolean
  // mayAdmin is the full-admin gate: users, roles, tokens, plugins, updates,
  // reboot, restore, capture and SSH keys.
  mayAdmin: boolean
  // loaded is false while the session is still unknown (or /auth/me failed),
  // when a page must not hide anything yet.
  loaded: boolean
}

const none: Permissions = { can: () => true, mayAdmin: true, loaded: false }

export function permissionsFor(me: { permissions?: string[]; full_admin?: boolean } | null | undefined): Permissions {
  if (!me) return none
  const held = me.permissions ?? []
  return {
    can: (scope: string) => me.full_admin === true || !me.permissions || held.includes(scope),
    mayAdmin: me.full_admin === true || !me.permissions,
    loaded: true,
  }
}

// usePermissions reads the session once and keeps it in the component's own
// state, so any page can gate its controls without a QueryClientProvider of
// its own. getMe returns null when there is no session; the apiClient then
// already redirected to /login.
export function usePermissions(): Permissions {
  const [me, setMe] = useState<Me | null>(null)
  const [loaded, setLoaded] = useState(false)
  useEffect(() => {
    let live = true
    getMe()
      .then((m: Me | null) => live && setMe(m))
      .catch(() => live && setMe(null))
      .finally(() => live && setLoaded(true))
    return () => {
      live = false
    }
  }, [])
  const p = permissionsFor(me)
  return loaded ? p : { ...p, loaded: false }
}

// forbiddenMessage turns an API error into something to show the user. A 403
// from the API says only "your role does not allow this" and names the scope
// in details.needs (internal/api), so the scope is spelled out here.
export function forbiddenMessage(e: unknown): string {
  if (!(e instanceof APIRequestError)) return e instanceof Error ? e.message : String(e)
  if (e.status !== 403) return e.message
  const needs = (e.details as { needs?: string } | null)?.needs
  return needs ? t('permissions.refusedWithScope', { message: e.message, scope: needs }) : e.message
}
