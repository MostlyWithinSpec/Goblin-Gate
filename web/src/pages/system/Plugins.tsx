// Plugins (task 069): manage installed plugins through the candidate config.
// A table lists every plugin from GET /api/v1/plugins with its state; an
// admin enables a plugin by approving capabilities, disables it, edits its
// settings (a form generated from the manifest's settings_schema), and opens
// its UI pages in sandboxed iframes. Enable/disable/settings change the
// candidate, so the commit bar reflects them.
import React, { useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Puzzle, Power, Settings as SettingsIcon, ExternalLink } from 'lucide-react'
import { Badge } from '../../components/Badge'
import { inputClass } from '../../components/ConfigList'
import { t } from '../../i18n'
import { getCandidate, type Policy } from '../../api/policy'
import {
  getPlugins,
  enablePlugin,
  disablePlugin,
  setPluginSettings,
  setPluginPolicies,
  CAPABILITY_DESCRIPTIONS,
  type PluginStatus,
  type PluginProperty
} from '../../api/plugins'

const STATE_BADGE: Record<PluginStatus['state'], 'success' | 'secondary' | 'error' | 'warning'> = {
  running: 'success',
  stopped: 'secondary',
  failed: 'error',
  invalid: 'error',
  not_installed: 'warning'
}

// defaultValue renders a schema default as placeholder text.
function placeholder(p: PluginProperty): string {
  if (p.default === undefined || p.default === null) return ''
  if (typeof p.default === 'boolean') return p.default ? 'true' : 'false'
  if (Array.isArray(p.default)) return p.default.join('\n')
  return String(p.default)
}

// PolicySwitchSection: shown when the admin approved policies:toggle for a
// plugin. It lists every policy of the candidate config with a checkbox; the
// ticked ones are the policies the plugin is allowed to switch on and off.
// Saving goes through PUT /api/v1/plugins/{name}/policies.
function PolicySwitchSection({
  plugin,
  policies
}: {
  plugin: PluginStatus
  policies: Policy[]
}) {
  const queryClient = useQueryClient()
  const [ids, setIds] = useState<number[]>(plugin.policies ?? [])
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)

  const toggle = (id: number, on: boolean) => {
    setSaved(false)
    setIds((prev) => (on ? [...prev.filter((x) => x !== id), id] : prev.filter((x) => x !== id)))
  }

  const save = async () => {
    setError(null)
    setSaved(false)
    try {
      const res = await setPluginPolicies(plugin.name, ids)
      if (res.validation_errors && res.validation_errors.length > 0) {
        setError(res.validation_errors.map(String).join('\n'))
        return
      }
      queryClient.invalidateQueries({ queryKey: ['configDiff'] })
      setSaved(true)
    } catch (e) {
      const err = e as Error & { details?: unknown }
      if (Array.isArray(err.details) && err.details.length > 0) {
        setError(err.details.map(String).join('\n'))
      } else {
        setError(err.message || t('plugins.errPolicies'))
      }
    }
  }

  return (
    <div className="mt-6 border-t pt-4">
      <h3 className="text-sm font-semibold">{t('plugins.policiesTitle')}</h3>
      <p role="note" className="mt-1 rounded bg-yellow-50 p-2 text-xs text-yellow-800 dark:bg-yellow-900/20 dark:text-yellow-200">
        {t('plugins.policiesWarning')}
      </p>
      {policies.length === 0 ? (
        <p className="mt-2 text-sm text-gray-500">{t('plugins.noPolicies')}</p>
      ) : (
        <div className="mt-2 space-y-1">
          {policies.map((p) => (
            <label key={p.id} className="flex cursor-pointer items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={ids.includes(p.id)}
                onChange={(e) => toggle(p.id, e.target.checked)}
              />
              <span>
                <code className="rounded bg-gray-100 px-1 text-xs dark:bg-gray-700">{p.id}</code>
                <span className="ml-2">{p.name}</span>
              </span>
            </label>
          ))}
        </div>
      )}
      {error && (
        <div role="alert" className="mt-3 whitespace-pre-line rounded bg-red-50 p-2 text-sm text-red-700">
          {error}
        </div>
      )}
      {saved && <p className="mt-3 text-sm text-green-700 dark:text-green-300">{t('plugins.policiesSaved')}</p>}
      <div className="mt-3">
        <button
          onClick={save}
          className="rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700"
        >
          {t('plugins.savePolicies')}
        </button>
      </div>
    </div>
  )
}

// SettingsDrawer: a form generated from the manifest's settings_schema.
function SettingsDrawer({
  plugin,
  onClose
}: {
  plugin: PluginStatus
  onClose: () => void
}) {
  const queryClient = useQueryClient()
  const schema = plugin.settings_schema
  const props = schema?.properties ?? {}
  const [values, setValues] = useState<Record<string, string>>({})
  const [bools, setBools] = useState<Record<string, boolean>>({})
  const [error, setError] = useState<string | null>(null)
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  // cleared lists the secret fields the admin pressed "Clear" on: they are
  // sent as null, so the stored value is dropped.
  const [cleared, setCleared] = useState<Record<string, boolean>>({})
  // policies:toggle is only useful once the admin approved it for this plugin.
  const canTogglePolicies = (plugin.capabilities ?? []).includes('policies:toggle')
  const [policies, setPolicies] = useState<Policy[]>([])

  // The candidate's policies feed the "policies it may switch" checkbox list.
  React.useEffect(() => {
    if (!canTogglePolicies) return
    getCandidate()
      .then((cfg) => setPolicies(cfg.policies ?? []))
      .catch(() => setPolicies([]))
  }, [canTogglePolicies])

  // Seed from the candidate's current settings. A secret is never returned by
  // the API, so its field always starts empty.
  React.useEffect(() => {
    const v: Record<string, string> = {}
    const b: Record<string, boolean> = {}
    for (const [key, p] of Object.entries(props)) {
      const cur = plugin.settings?.[key]
      if (p.type === 'boolean') {
        b[key] = typeof cur === 'boolean' ? cur : Boolean(p.default)
      } else if (p.type === 'array') {
        v[key] = Array.isArray(cur) ? (cur as unknown[]).map(String).join('\n') : placeholder(p)
      } else if (p.secret) {
        v[key] = ''
      } else {
        v[key] = cur === undefined || cur === null ? '' : String(cur)
      }
    }
    setValues(v)
    setBools(b)
    setCleared({})
  }, [plugin.name]) // eslint-disable-line react-hooks/exhaustive-deps

  const setStr = (key: string, val: string) => {
    setValues((prev) => ({ ...prev, [key]: val }))
    setCleared((prev) => (prev[key] ? { ...prev, [key]: false } : prev))
    setFieldErrors((prev) => {
      if (!prev[key]) return prev
      const next = { ...prev }
      delete next[key]
      return next
    })
  }

  const save = async () => {
    setError(null)
    setFieldErrors({})
    const settings: Record<string, unknown> = {}
    for (const [key, p] of Object.entries(props)) {
      if (p.type === 'boolean') {
        settings[key] = bools[key] ?? Boolean(p.default)
      } else if (p.type === 'array') {
        settings[key] = (values[key] ?? '')
          .split('\n')
          .map((s) => s.trim())
          .filter(Boolean)
      } else if (p.secret) {
        // "" keeps the stored secret, null clears it, text replaces it.
        const raw = values[key] ?? ''
        settings[key] = cleared[key] ? null : raw
      } else if (p.type === 'integer' || p.type === 'number') {
        const raw = values[key]?.trim()
        settings[key] = raw === '' ? undefined : Number(raw)
      } else {
        settings[key] = values[key] ?? ''
      }
    }
    // Drop explicit undefined (empty numbers).
    for (const k of Object.keys(settings)) {
      if (settings[k] === undefined) delete settings[k]
    }
    try {
      await setPluginSettings(plugin.name, settings)
      queryClient.invalidateQueries({ queryKey: ['configDiff'] })
      onClose()
    } catch (e) {
      const err = e as Error & { details?: unknown }
      if (err.details && Array.isArray(err.details)) {
        // 400 validation_failed: details is an array of "key: message".
        const fe: Record<string, string> = {}
        for (const d of err.details) {
          const s = String(d)
          const idx = s.indexOf(': ')
          if (idx > 0) {
            fe[s.slice(0, idx)] = s.slice(idx + 2)
          } else {
            fe[s] = ''
          }
        }
        setFieldErrors(fe)
        setError(t('plugins.errSettingsInvalid'))
      } else {
        setError(err.message || t('plugins.errSettings'))
      }
    }
  }

  const field = (key: string, p: PluginProperty) => {
    const label = key
    const desc = p.description ? <span className="ml-2 text-xs text-gray-500">{p.description}</span> : null
    const err = fieldErrors[key]
    switch (p.type) {
      case 'string':
        if (p.secret) {
          const isSet = (plugin.secrets_set ?? []).includes(key)
          return (
            <div key={key}>
              <label htmlFor={`set-${key}`} className="block text-sm font-medium">
                {label}
                {desc}
              </label>
              <div className="flex items-center gap-2">
                <input
                  id={`set-${key}`}
                  type="password"
                  autoComplete="new-password"
                  className={inputClass}
                  value={values[key] ?? ''}
                  maxLength={p.maxLength}
                  onChange={(e) => setStr(key, e.target.value)}
                />
                {isSet && !cleared[key] && (
                  <button
                    onClick={() => setCleared((prev) => ({ ...prev, [key]: true }))}
                    className="shrink-0 rounded border px-2 py-1.5 text-xs hover:bg-gray-100 dark:hover:bg-gray-700"
                  >
                    {t('plugins.clearSecret')}
                  </button>
                )}
              </div>
              <p className="mt-1 text-xs text-gray-500">
                {cleared[key]
                  ? t('plugins.secretWillClear')
                  : isSet
                    ? t('plugins.secretIsSet')
                    : t('plugins.secretNotSet')}
              </p>
              {err && <p className="mt-1 text-xs text-red-600">{key}: {err}</p>}
            </div>
          )
        }
        return (
          <div key={key}>
            <label htmlFor={`set-${key}`} className="block text-sm font-medium">
              {label}
              {desc}
            </label>
            <input
              id={`set-${key}`}
              type="text"
              className={inputClass}
              value={values[key] ?? ''}
              maxLength={p.maxLength}
              placeholder={placeholder(p)}
              onChange={(e) => setStr(key, e.target.value)}
            />
            {err && <p className="mt-1 text-xs text-red-600">{key}: {err}</p>}
          </div>
        )
      case 'integer':
      case 'number':
        return (
          <div key={key}>
            <label htmlFor={`set-${key}`} className="block text-sm font-medium">
              {label}
              {desc}
            </label>
            <input
              id={`set-${key}`}
              type="number"
              step={p.type === 'integer' ? 1 : 'any'}
              min={p.minimum}
              max={p.maximum}
              className={inputClass}
              value={values[key] ?? ''}
              placeholder={placeholder(p)}
              onChange={(e) => setStr(key, e.target.value)}
            />
            {err && <p className="mt-1 text-xs text-red-600">{key}: {err}</p>}
          </div>
        )
      case 'boolean':
        return (
          <div key={key} className="flex items-center">
            <label htmlFor={`set-${key}`} className="flex cursor-pointer items-center gap-2 text-sm font-medium">
              <input
                id={`set-${key}`}
                type="checkbox"
                checked={bools[key] ?? false}
                onChange={(e) => setBools((prev) => ({ ...prev, [key]: e.target.checked }))}
              />
              {label}
              {desc}
            </label>
            {err && <p className="ml-3 text-xs text-red-600">{key}: {err}</p>}
          </div>
        )
      case 'array':
        return (
          <div key={key}>
            <label htmlFor={`set-${key}`} className="block text-sm font-medium">
              {label}
              {desc}
            </label>
            <textarea
              id={`set-${key}`}
              rows={3}
              className={inputClass}
              value={values[key] ?? ''}
              placeholder={placeholder(p)}
              onChange={(e) => setStr(key, e.target.value)}
            />
            {err && <p className="mt-1 text-xs text-red-600">{key}: {err}</p>}
          </div>
        )
      default:
        return null
    }
  }

  const enumField = (key: string, p: PluginProperty) => {
    if (p.secret || !p.enum || p.enum.length === 0) return field(key, p)
    const label = key
    const desc = p.description ? <span className="ml-2 text-xs text-gray-500">{p.description}</span> : null
    const err = fieldErrors[key]
    return (
      <div key={key}>
        <label htmlFor={`set-${key}`} className="block text-sm font-medium">
          {label}
          {desc}
        </label>
        <select
          id={`set-${key}`}
          className={inputClass}
          value={values[key] ?? ''}
          onChange={(e) => setStr(key, e.target.value)}
        >
          <option value="" disabled>
            {t('plugins.selectOption')}
          </option>
          {p.enum.map((opt, i) => (
            <option key={i} value={String(opt)}>
              {String(opt)}
            </option>
          ))}
        </select>
        {err && <p className="mt-1 text-xs text-red-600">{key}: {err}</p>}
      </div>
    )
  }

  const hasSchema = Object.keys(props).length > 0

  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      <div className="absolute inset-0 bg-black/40" onClick={onClose} />
      <div className="relative h-full w-full max-w-md overflow-y-auto bg-white p-6 shadow-xl dark:bg-gray-800">
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold">{t('plugins.settingsFor', { name: plugin.name })}</h2>
          <button onClick={onClose} className="text-sm text-gray-500 hover:text-gray-700">
            {t('plugins.close')}
          </button>
        </div>
        {!hasSchema ? (
          <p className="text-sm text-gray-500">{t('plugins.noSettings')}</p>
        ) : (
          <div className="space-y-4">
            {Object.entries(props).map(([key, p]) => enumField(key, p))}
          </div>
        )}
        {error && (
          <div role="alert" className="mt-4 rounded bg-red-50 p-2 text-sm text-red-700">
            {error}
          </div>
        )}
        {hasSchema && (
          <div className="mt-6 flex justify-end gap-2">
            <button onClick={onClose} className="rounded border px-3 py-1.5 text-sm">
              {t('plugins.cancel')}
            </button>
            <button onClick={save} className="rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700">
              {t('plugins.save')}
            </button>
          </div>
        )}
        {canTogglePolicies && <PolicySwitchSection plugin={plugin} policies={policies} />}
      </div>
    </div>
  )
}

// EnableDialog: lists the capabilities a plugin asks for as checkboxes (all
// checked) with a plain explanation each; confirm grants only the checked ones.
function EnableDialog({
  plugin,
  onClose
}: {
  plugin: PluginStatus
  onClose: () => void
}) {
  const queryClient = useQueryClient()
  const [checked, setChecked] = useState<Record<string, boolean>>({})
  const [error, setError] = useState<string | null>(null)

  React.useEffect(() => {
    const c: Record<string, boolean> = {}
    for (const cap of plugin.requested_capabilities) c[cap] = true
    setChecked(c)
  }, [plugin.name]) // eslint-disable-line react-hooks/exhaustive-deps

  const confirm = async () => {
    setError(null)
    const caps = plugin.requested_capabilities.filter((c) => checked[c])
    try {
      await enablePlugin(plugin.name, caps)
      queryClient.invalidateQueries({ queryKey: ['configDiff'] })
      onClose()
    } catch (e) {
      setError((e as Error).message || t('plugins.errEnable'))
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="absolute inset-0 bg-black/40" onClick={onClose} />
      <div className="relative w-full max-w-lg rounded-lg bg-white p-6 shadow-xl dark:bg-gray-800">
        <h2 className="mb-1 text-lg font-semibold">{t('plugins.enableTitle', { name: plugin.name })}</h2>
        <p className="mb-4 text-sm text-gray-500">
          {t('plugins.enableIntro')}
        </p>
        <div className="space-y-2">
          {plugin.requested_capabilities.map((cap) => (
            <label key={cap} className="flex cursor-pointer items-start gap-2 text-sm">
              <input
                type="checkbox"
                checked={checked[cap] ?? false}
                onChange={(e) => setChecked((prev) => ({ ...prev, [cap]: e.target.checked }))}
              />
              <span>
                <code className="rounded bg-gray-100 px-1 text-xs dark:bg-gray-700">{cap}</code>
                <span className="ml-2 text-gray-600 dark:text-gray-300">
                  {CAPABILITY_DESCRIPTIONS[cap] ?? t('plugins.noDescription')}
                </span>
              </span>
            </label>
          ))}
        </div>
        {error && (
          <div role="alert" className="mt-4 rounded bg-red-50 p-2 text-sm text-red-700">
            {error}
          </div>
        )}
        <div className="mt-6 flex justify-end gap-2">
          <button onClick={onClose} className="rounded border px-3 py-1.5 text-sm">
            {t('plugins.cancel')}
          </button>
          <button onClick={confirm} className="rounded bg-blue-600 px-3 py-1.5 text-sm text-white hover:bg-blue-700">
            {t('plugins.enable')}
          </button>
        </div>
      </div>
    </div>
  )
}

export default function Plugins() {
  const queryClient = useQueryClient()
  const [plugins, setPlugins] = useState<PluginStatus[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [enabling, setEnabling] = useState<PluginStatus | null>(null)
  const [settingsFor, setSettingsFor] = useState<PluginStatus | null>(null)

  React.useEffect(() => {
    getPlugins()
      .then((res) => setPlugins(res.plugins))
      .catch((e: Error) => setError(e.message))
  }, [])

  const disable = async (p: PluginStatus) => {
    try {
      await disablePlugin(p.name)
      queryClient.invalidateQueries({ queryKey: ['configDiff'] })
      setPlugins((prev) =>
        prev ? prev.map((x) => (x.name === p.name ? { ...x, enabled: false } : x)) : prev
      )
    } catch (e) {
      setError((e as Error).message || t('plugins.errDisable'))
    }
  }

  if (error && !plugins) {
    return (
      <div>
        <h1 className="mb-4 text-2xl font-bold">{t('plugins.title')}</h1>
        <div role="alert" className="rounded bg-red-50 p-2 text-sm text-red-700">
          {error}
        </div>
      </div>
    )
  }

  if (!plugins) {
    return (
      <div>
        <h1 className="mb-4 text-2xl font-bold">{t('plugins.title')}</h1>
        <p className="text-sm text-gray-500">{t('common.loading')}</p>
      </div>
    )
  }

  return (
    <div>
      <h1 className="mb-2 text-2xl font-bold">{t('plugins.title')}</h1>
      <div role="note" className="mb-4 rounded border border-yellow-300 bg-yellow-50 p-3 text-sm text-yellow-800 dark:border-yellow-700 dark:bg-yellow-900/20 dark:text-yellow-200">
        {t('plugins.trustWarning')}
      </div>

      <table className="w-full border-collapse text-sm">
        <thead>
          <tr className="border-b text-left text-xs uppercase text-gray-500">
            <th className="py-2 pr-4">{t('plugins.colName')}</th>
            <th className="py-2 pr-4">{t('plugins.colVersion')}</th>
            <th className="py-2 pr-4">{t('plugins.colDescription')}</th>
            <th className="py-2 pr-4">{t('plugins.colState')}</th>
            <th className="py-2 pr-4">{t('plugins.colEnabled')}</th>
            <th className="py-2 pr-4">{t('plugins.colActions')}</th>
          </tr>
        </thead>
        <tbody>
          {plugins.map((p) => (
            <React.Fragment key={p.name}>
              <tr className="border-b border-gray-100 dark:border-gray-700">
                <td className="py-2 pr-4 font-medium">{p.name}</td>
                <td className="py-2 pr-4">{p.version ?? '—'}</td>
                <td className="py-2 pr-4 text-gray-600 dark:text-gray-300">{p.description || '—'}</td>
                <td className="py-2 pr-4">
                  <Badge variant={STATE_BADGE[p.state]}>{p.state}</Badge>
                  {p.problem && (
                    <span className="ml-2 text-xs text-red-600">{p.problem}</span>
                  )}
                </td>
                <td className="py-2 pr-4">
                  {p.enabled ? (
                    <Badge variant="success">{t('plugins.enabled')}</Badge>
                  ) : (
                    <Badge variant="secondary">{t('plugins.disabled')}</Badge>
                  )}
                </td>
                <td className="py-2 pr-4">
                  <div className="flex flex-wrap gap-2">
                    {!p.enabled && p.state !== 'invalid' && p.state !== 'not_installed' && (
                      <button
                        onClick={() => setEnabling(p)}
                        className="inline-flex items-center gap-1 rounded bg-blue-600 px-2 py-1 text-xs text-white hover:bg-blue-700"
                      >
                        <Power size={14} /> {t('plugins.enable')}
                      </button>
                    )}
                    {p.enabled && (
                      <button
                        onClick={() => disable(p)}
                        className="inline-flex items-center gap-1 rounded border px-2 py-1 text-xs hover:bg-gray-100 dark:hover:bg-gray-700"
                      >
                        <Power size={14} /> {t('plugins.disable')}
                      </button>
                    )}
                    {p.settings_schema && p.state !== 'not_installed' && (
                      <button
                        onClick={() => setSettingsFor(p)}
                        className="inline-flex items-center gap-1 rounded border px-2 py-1 text-xs hover:bg-gray-100 dark:hover:bg-gray-700"
                      >
                        <SettingsIcon size={14} /> {t('plugins.settings')}
                      </button>
                    )}
                  </div>
                </td>
              </tr>
              {p.ui?.pages && p.ui.pages.length > 0 && (
                <tr className="border-b border-gray-100 bg-gray-50 dark:border-gray-700 dark:bg-gray-800/40">
                  <td colSpan={6} className="py-2 pl-4">
                    <div className="flex flex-wrap items-center gap-3 text-xs text-gray-500">
                      <Puzzle size={14} />
                      {t('plugins.pages')}
                      {p.ui.pages.map((page) => (
                        <Link
                          key={page.path}
                          to={`/system/plugins/${p.name}/${page.path}`}
                          className="inline-flex items-center gap-1 text-blue-600 hover:underline dark:text-blue-300"
                        >
                          {page.title || page.path} <ExternalLink size={12} />
                        </Link>
                      ))}
                    </div>
                  </td>
                </tr>
              )}
            </React.Fragment>
          ))}
        </tbody>
      </table>

      {enabling && <EnableDialog plugin={enabling} onClose={() => setEnabling(null)} />}
      {settingsFor && <SettingsDrawer plugin={settingsFor} onClose={() => setSettingsFor(null)} />}
    </div>
  )
}
