// System → Admins → Single sign-on (task 148). The card edits the candidate
// "sso" section that task 147 added: the identity provider is reached by
// /api/v1/auth/sso/login, so nothing here talks to the provider directly.
// The client secret is a secret: the server blanks it for callers that may not
// see secrets, so an empty field keeps what is stored.
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Copy, Plus, Trash2 } from 'lucide-react'
import { getCandidate, patchSection } from '../api/objects'
import { getRoles } from '../api/roles'
import { inputClass, omitEmpty } from './ConfigList'
import { t } from '../i18n'

export interface SSORoleMap {
  group: string
  role: string
}

export interface SSOSettings {
  enabled: boolean
  name?: string
  issuer?: string
  client_id?: string
  client_secret?: string
  scopes?: string[]
  username_claim?: string
  groups_claim?: string
  roles?: SSORoleMap[]
  default_role?: string
  redirect_url?: string
  ca_certificate?: string
}

export interface SSOForm {
  enabled: boolean
  name: string
  issuer: string
  client_id: string
  client_secret: string
  scopes: string
  username_claim: string
  groups_claim: string
  roles: SSORoleMap[]
  default_role: string
  redirect_url: string
  ca_certificate: string
  // hasSecret is the form's memory that the server holds a client secret: an
  // empty field then keeps it instead of making the client public.
  hasSecret: boolean
}

const emptyForm: SSOForm = {
  enabled: false,
  name: '',
  issuer: '',
  client_id: '',
  client_secret: '',
  scopes: '',
  username_claim: '',
  groups_claim: '',
  roles: [],
  default_role: '',
  redirect_url: '',
  ca_certificate: '',
  hasSecret: false,
}

export function formFromSSO(s: SSOSettings | undefined): SSOForm {
  if (!s) return { ...emptyForm, roles: [] }
  return {
    enabled: !!s.enabled,
    name: s.name ?? '',
    issuer: s.issuer ?? '',
    client_id: s.client_id ?? '',
    client_secret: '',
    scopes: (s.scopes ?? []).join(' '),
    username_claim: s.username_claim ?? '',
    groups_claim: s.groups_claim ?? '',
    roles: (s.roles ?? []).map((r) => ({ group: r.group, role: r.role })),
    default_role: s.default_role ?? '',
    redirect_url: s.redirect_url ?? '',
    ca_certificate: s.ca_certificate ?? '',
    // An issuer cannot work without a URL, so a blank secret next to one means
    // the server blanked it rather than that none is stored.
    hasSecret: !!s.client_secret || !!s.issuer,
  }
}

// ssoRedirect is the callback URL to register with the provider: the configured
// one when set, else the address this browser is talking to.
export function ssoRedirect(loaded: SSOSettings | undefined, origin: string): string {
  const configured = (loaded?.redirect_url ?? '').trim()
  if (configured) return configured
  return `${origin}/api/v1/auth/sso/callback`
}

// buildSSO turns the form into the candidate section. An untouched secret is
// sent back empty (or left out when none is stored); the CommitBar commits it
// after. omitEmpty runs only over the fields the server may leave unset, so an
// explicit empty secret survives — the server puts the stored one back.
export function buildSSO(f: SSOForm): SSOSettings {
  const out: Record<string, unknown> = {
    enabled: f.enabled,
    name: f.name.trim(),
    issuer: f.issuer.trim(),
    client_id: f.client_id.trim(),
    username_claim: f.username_claim.trim(),
    groups_claim: f.groups_claim.trim(),
    default_role: f.default_role,
    redirect_url: f.redirect_url.trim(),
    ca_certificate: f.ca_certificate.trim(),
    roles: f.roles
      .map((r) => ({ group: r.group.trim(), role: r.role }))
      .filter((r) => r.group !== '' || r.role !== ''),
  }
  const scopes = f.scopes.split(/[\s,]+/).map((s) => s.trim()).filter(Boolean)
  if (scopes.length > 0) out.scopes = scopes
  const built = omitEmpty(out as unknown as SSOSettings)
  const typed = f.client_secret.trim()
  if (typed) built.client_secret = typed
  else if (f.hasSecret) built.client_secret = ''
  return built
}

const NAME_MAX = 40

// validateSSO mirrors the server rules (internal/config/sso.go) so a bad save
// shows in the form instead of coming back as a validation_errors list.
export function validateSSO(f: SSOForm, roles: string[]): Record<string, string> {
  const e: Record<string, string> = {}
  if (!f.enabled) return e
  const issuer = f.issuer.trim()
  let parsed: URL | null = null
  try {
    parsed = new URL(issuer)
  } catch {
    parsed = null
  }
  const loopback = parsed ? ['localhost', '127.0.0.1', '::1'].includes(parsed.hostname) : false
  if (!parsed || !parsed.hostname || (parsed.protocol !== 'https:' && !(parsed.protocol === 'http:' && loopback))) {
    e.issuer = t('sso.errIssuer')
  }
  if (!f.client_id.trim()) e.client_id = t('sso.errClientID')
  if (f.name.length > NAME_MAX) e.name = t('sso.errName')
  const badScope = f.scopes.split(/\s+/).some((s) => s.includes(','))
  if (badScope) e.scopes = t('sso.errScopes')
  if (/\s/.test(f.username_claim.trim())) e.username_claim = t('sso.errClaim')
  if (/\s/.test(f.groups_claim.trim())) e.groups_claim = t('sso.errClaim')
  f.roles.forEach((r, i) => {
    if (!r.group.trim()) e[`roles[${i}].group`] = t('sso.errGroup')
    if (!r.role) e[`roles[${i}].role`] = t('sso.errRole')
    else if (roles.length > 0 && !roles.includes(r.role)) e[`roles[${i}].role`] = t('sso.errRole')
  })
  if (f.default_role && roles.length > 0 && !roles.includes(f.default_role)) e.default_role = t('sso.errRole')
  if (!f.roles.some((r) => r.group.trim()) && !f.default_role) e.roles = t('sso.errNoMapping')
  const redirect = f.redirect_url.trim()
  if (redirect) {
    let u: URL | null = null
    try {
      u = new URL(redirect)
    } catch {
      u = null
    }
    if (!u || u.protocol !== 'https:' || !u.host || u.pathname !== '/api/v1/auth/sso/callback') {
      e.redirect_url = t('sso.errRedirect')
    }
  }
  if (f.ca_certificate.trim() && !f.ca_certificate.includes('BEGIN CERTIFICATE')) {
    e.ca_certificate = t('sso.errCA')
  }
  return e
}

export const SSOSection: React.FC = () => {
  const [form, setForm] = useState<SSOForm | null>(null)
  const [loaded, setLoaded] = useState<SSOSettings | undefined>(undefined)
  const [roles, setRoles] = useState<string[]>([])
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [message, setMessage] = useState<{ ok: boolean; text: string } | null>(null)
  const [copied, setCopied] = useState(false)

  const load = useCallback(() => {
    getCandidate()
      .then((c) => {
        const s = c.sso as SSOSettings | undefined
        setLoaded(s)
        setForm(formFromSSO(s))
      })
      .catch(() => setForm({ ...emptyForm, roles: [] }))
    // The role pickers list every role, custom ones included; a role a mapping
    // uses anyway stays in the list so the select can show it.
    getRoles()
      .then((r) => setRoles(r.roles.map((x) => x.name)))
      .catch(() => { /* the pickers fall back to what the config already uses */ })
  }, [])
  useEffect(load, [load])

  const redirect = useMemo(
    () => ssoRedirect(loaded, typeof window === 'undefined' ? '' : window.location.origin),
    [loaded],
  )

  const pickRole = (current: string): string[] =>
    roles.includes(current) ? roles : [...roles, current].filter(Boolean)

  const set = (patch: Partial<SSOForm>) => {
    setMessage(null)
    setErrors({})
    setForm((f) => (f ? { ...f, ...patch } : f))
  }

  const copyRedirect = async () => {
    try {
      await navigator.clipboard.writeText(redirect)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      /* clipboard unavailable (insecure context); the URL is on screen anyway */
    }
  }

  const save = async (ev: React.FormEvent) => {
    ev.preventDefault()
    if (!form) return
    const errs = validateSSO(form, roles)
    setErrors(errs)
    // drop an earlier save message: a form that becomes valid again must not
    // still show the old failure
    setMessage(null)
    if (Object.keys(errs).length > 0) return
    const value = buildSSO(form)
    try {
      const res = await patchSection('sso', value)
      const problems: Record<string, string> = {}
      for (const v of res.validation_errors ?? []) {
        if (!v.path.startsWith('sso.')) continue
        const field = v.path.slice('sso.'.length)
        if (!problems[field]) problems[field] = v.message
      }
      setErrors(problems)
      if (Object.keys(problems).length > 0) return
      // The save went through: a secret that was typed (or kept behind the
      // blank field) is what the server holds now.
      setForm((f) => (f ? { ...f, client_secret: '', hasSecret: f.hasSecret || !!value.client_secret } : f))
      // Remember what the server holds now, keeping the secret the caller read
      // (it is never shown again).
      setLoaded((c) => ({ ...c, ...value, client_secret: value.client_secret || c?.client_secret }))
      setMessage({ ok: true, text: t('sso.saved') })
    } catch (e) {
      setMessage({ ok: false, text: t('sso.saveFailed', { error: (e as Error).message }) })
    }
  }

  if (!form) return null

  return (
    <div className="mt-6 rounded-lg border border-gray-200 p-4 dark:border-gray-600">
      <h3 className="text-sm font-semibold text-gray-800 dark:text-white">{t('sso.title')}</h3>
      <p className="mt-1 text-sm text-gray-600 dark:text-gray-300">{t('sso.intro')}</p>
      <form onSubmit={save} aria-label={t('sso.title')} className="mt-3 max-w-2xl space-y-3">
        {message && (
          <div
            role={message.ok ? 'status' : 'alert'}
            className={`rounded p-2 text-sm ${message.ok ? 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-300' : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300'}`}
          >
            {message.text}
          </div>
        )}
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" aria-label={t('sso.enable')} checked={form.enabled} onChange={(e) => set({ enabled: e.target.checked })} />
          {t('sso.enable')}
        </label>

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label className="block text-sm">
            {t('sso.name')}
            <input className={inputClass} aria-label={t('sso.name')} value={form.name} onChange={(e) => set({ name: e.target.value })} />
            {errors.name && <p role="alert" className="mt-1 text-xs text-red-600 dark:text-red-400">{errors.name}</p>}
          </label>
          <label className="block text-sm">
            {t('sso.issuer')}
            <input className={inputClass} aria-label={t('sso.issuer')} placeholder="https://id.example.com/realms/main" value={form.issuer} onChange={(e) => set({ issuer: e.target.value })} />
            {errors.issuer && <p role="alert" className="mt-1 text-xs text-red-600 dark:text-red-400">{errors.issuer}</p>}
          </label>
          <label className="block text-sm">
            {t('sso.clientID')}
            <input className={inputClass} aria-label={t('sso.clientID')} value={form.client_id} onChange={(e) => set({ client_id: e.target.value })} />
            {errors.client_id && <p role="alert" className="mt-1 text-xs text-red-600 dark:text-red-400">{errors.client_id}</p>}
          </label>
          <label className="block text-sm">
            {t('sso.clientSecret')}
            <input
              type="password"
              className={inputClass}
              aria-label={t('sso.clientSecret')}
              placeholder={form.hasSecret ? t('sso.unchanged') : ''}
              value={form.client_secret}
              onChange={(e) => set({ client_secret: e.target.value })}
            />
          </label>
          <label className="block text-sm">
            {t('sso.scopes')}
            <input className={inputClass} aria-label={t('sso.scopes')} placeholder="profile email groups" value={form.scopes} onChange={(e) => set({ scopes: e.target.value })} />
            {errors.scopes && <p role="alert" className="mt-1 text-xs text-red-600 dark:text-red-400">{errors.scopes}</p>}
          </label>
          <label className="block text-sm">
            {t('sso.usernameClaim')}
            <input className={inputClass} aria-label={t('sso.usernameClaim')} placeholder="preferred_username" value={form.username_claim} onChange={(e) => set({ username_claim: e.target.value })} />
            {errors.username_claim && <p role="alert" className="mt-1 text-xs text-red-600 dark:text-red-400">{errors.username_claim}</p>}
          </label>
          <label className="block text-sm">
            {t('sso.groupsClaim')}
            <input className={inputClass} aria-label={t('sso.groupsClaim')} placeholder="groups" value={form.groups_claim} onChange={(e) => set({ groups_claim: e.target.value })} />
            {errors.groups_claim && <p role="alert" className="mt-1 text-xs text-red-600 dark:text-red-400">{errors.groups_claim}</p>}
          </label>
          <label className="block text-sm">
            {t('sso.defaultRole')}
            <select className={inputClass} aria-label={t('sso.defaultRole')} value={form.default_role} onChange={(e) => set({ default_role: e.target.value })}>
              <option value="">{t('sso.defaultRoleNone')}</option>
              {pickRole(form.default_role).map((r) => (
                <option key={r} value={r}>{r}</option>
              ))}
            </select>
            {errors.default_role && <p role="alert" className="mt-1 text-xs text-red-600 dark:text-red-400">{errors.default_role}</p>}
          </label>
        </div>

        <div>
          <p className="text-sm font-medium text-gray-700 dark:text-gray-300">{t('sso.roleMap')}</p>
          <p className="text-xs text-gray-500">{t('sso.roleMapHint')}</p>
          {form.roles.map((m, i) => (
            <div key={i} className="mt-2 flex flex-wrap items-center gap-2">
              <input
                className={inputClass}
                aria-label={t('sso.groupFor', { n: i + 1 })}
                placeholder={t('sso.groupPlaceholder')}
                value={m.group}
                onChange={(e) => set({ roles: form.roles.map((x, j) => (j === i ? { ...x, group: e.target.value } : x)) })}
              />
              <select
                className={inputClass}
                aria-label={t('sso.roleFor', { n: i + 1 })}
                value={m.role}
                onChange={(e) => set({ roles: form.roles.map((x, j) => (j === i ? { ...x, role: e.target.value } : x)) })}
              >
                <option value="">{t('sso.chooseRole')}</option>
                {pickRole(m.role).map((r) => (
                  <option key={r} value={r}>{r}</option>
                ))}
              </select>
              <button
                type="button"
                onClick={() => set({ roles: form.roles.filter((_, j) => j !== i) })}
                className="text-red-600"
                aria-label={t('sso.removeMapping')}
              >
                <Trash2 size={16} />
              </button>
            </div>
          ))}
          {errors.roles && <p role="alert" className="mt-1 text-sm text-red-600 dark:text-red-400">{errors.roles}</p>}
          {Object.entries(errors)
            .filter(([k]) => k.startsWith('roles['))
            .map(([k, v]) => (
              <p key={k} role="alert" className="mt-1 text-sm text-red-600 dark:text-red-400">{v}</p>
            ))}
          <button
            type="button"
            onClick={() => set({ roles: [...form.roles, { group: '', role: '' }] })}
            className="mt-2 flex items-center gap-1 rounded border border-gray-300 px-2 py-1 text-sm dark:border-gray-600"
          >
            <Plus size={14} /> {t('sso.addMapping')}
          </button>
          {/* saved settings can hold a half-filled row: without this the form
              could not get rid of it and every save would fail validation */}
          {form.roles.some((r) => r.group.trim() === '' || !r.role) && (
            <button
              type="button"
              onClick={() => set({ roles: [] })}
              className="ml-2 mt-2 text-sm text-red-600 hover:underline"
            >
              {t('sso.clearMappings')}
            </button>
          )}
        </div>

        <div className="rounded bg-gray-50 p-3 dark:bg-gray-900/40">
          <p className="text-sm font-medium text-gray-700 dark:text-gray-300">{t('sso.redirectTitle')}</p>
          <p className="mt-1 text-xs text-gray-500">{t('sso.redirectHint')}</p>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <code className="break-all text-sm">{redirect}</code>
            <button type="button" onClick={copyRedirect} className="flex items-center gap-1 text-sm text-blue-600 hover:underline">
              <Copy size={14} /> {copied ? t('sso.copied') : t('sso.copy')}
            </button>
          </div>
          <label className="mt-2 block text-sm">
            {t('sso.redirectOverride')}
            <input className={inputClass} aria-label={t('sso.redirectOverride')} placeholder={`https://gate.example.com${'/api/v1/auth/sso/callback'}`} value={form.redirect_url} onChange={(e) => set({ redirect_url: e.target.value })} />
          </label>
          {errors.redirect_url && <p role="alert" className="mt-1 text-sm text-red-600 dark:text-red-400">{errors.redirect_url}</p>}
        </div>

        <label className="block text-sm">
          {t('sso.caCertificate')}
          <textarea
            className={inputClass}
            rows={4}
            aria-label={t('sso.caCertificate')}
            placeholder="-----BEGIN CERTIFICATE-----"
            value={form.ca_certificate}
            onChange={(e) => set({ ca_certificate: e.target.value })}
          />
        </label>
        {errors.ca_certificate && <p role="alert" className="text-sm text-red-600 dark:text-red-400">{errors.ca_certificate}</p>}

        <button type="submit" className="rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700">
          {t('sso.save')}
        </button>
      </form>
    </div>
  )
}
