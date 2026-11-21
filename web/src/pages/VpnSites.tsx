// VPN → Sites (task 156): join Goblin Gates into one private network. This
// gate's own settings, the remote sites with their tunnel status, and the
// pairing dialogs: "Invite a site" makes a one-time code for the other gate
// and takes its reply; "Join a site" takes a code, shows what it brings and
// answers with the reply code. Pairing needs a full admin (a code lets another
// gate into the network); the settings and sites need write:network.
import React, { useCallback, useEffect, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { getCandidate, patchSection, type Candidate } from '../api/objects'
import type { ValidationError } from '../api/policy'
import { APIRequestError } from '../api/client'
import { usePermissions } from '../api/permissions'
import {
  acceptReply,
  cancelInvitation,
  checkJoin,
  createInvitation,
  getInvitations,
  getSitesStatus,
  joinSite,
  type NewInvitation,
  type Site,
  type SiteOffer,
  type SiteProblem,
  type SitesSettings,
} from '../api/sites'
import { Dialog } from '../components/Dialog'
import { EditDrawer } from '../components/EditDrawer'
import { inputClass } from '../components/ConfigList'
import {
  buildSettings,
  buildSite,
  formatAge,
  formatBytes,
  hubs,
  minutesLeft,
  networkOptions,
  settingsOf,
  siteBadge,
  siteFormOf,
  sitesWikiURL,
  takenNames,
  validateSettings,
  validateSite,
  type BadgeKind,
  type SettingsForm,
  type SiteErrors,
  type SiteForm,
} from './vpn/sitesLogic'
import { t } from '../i18n'

const box = 'rounded bg-white p-4 text-sm text-gray-700 shadow-sm dark:bg-gray-800 dark:text-gray-300'
const check = 'h-4 w-4 rounded border-gray-300 dark:border-gray-600'
const sub = 'mt-1 text-xs text-gray-500'
const btn = 'min-h-[40px] rounded border border-gray-300 px-3 text-sm dark:border-gray-600 disabled:opacity-50'
const primary = 'min-h-[40px] rounded bg-blue-600 px-4 text-sm text-white hover:bg-blue-700 disabled:opacity-50'
const codeBox = 'w-full break-all rounded border border-gray-300 bg-gray-50 p-2 font-mono text-xs dark:border-gray-600 dark:bg-gray-900'

const badgeCls: Record<BadgeKind, string> = {
  ok: 'rounded bg-green-100 px-1.5 py-0.5 text-xs text-green-800 dark:bg-green-900/40 dark:text-green-200',
  warn: 'rounded bg-yellow-100 px-1.5 py-0.5 text-xs text-yellow-800 dark:bg-yellow-900/40 dark:text-yellow-200',
  bad: 'rounded bg-red-100 px-1.5 py-0.5 text-xs text-red-700 dark:bg-red-900/40 dark:text-red-200',
  off: 'rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600 dark:bg-gray-700 dark:text-gray-300',
}

const errMessage = (e: unknown) => (e instanceof Error ? e.message : String(e))
const problemsOf = (e: unknown): SiteProblem[] =>
  e instanceof APIRequestError && Array.isArray(e.details) ? (e.details as SiteProblem[]).filter((p) => p && typeof p.message === 'string') : []

// Copy puts a code on the clipboard and says so.
function Copy({ text, label }: { text: string; label: string }) {
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      className={btn}
      aria-label={label}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text)
          setDone(true)
        } catch {
          setDone(false)
        }
      }}
    >
      {done ? t('sites.copied') : t('sites.copy')}
    </button>
  )
}

function Problems({ list }: { list: SiteProblem[] }) {
  if (list.length === 0) return null
  return (
    <ul role="alert" className="space-y-1 rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900/30 dark:text-red-200">
      {list.map((p) => (
        <li key={p.code + (p.network ?? '') + p.message}>{p.message}</li>
      ))}
    </ul>
  )
}

// InviteDialog: name the other site, make the code, then paste its reply.
function InviteDialog({ open, onClose, onDone, taken }: { open: boolean; onClose: () => void; onDone: () => void; taken: string[] }) {
  const [name, setName] = useState('')
  const [ttl, setTTL] = useState('60')
  const [inv, setInv] = useState<NewInvitation | null>(null)
  const [reply, setReply] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [problems, setProblems] = useState<SiteProblem[]>([])
  const [busy, setBusy] = useState(false)
  const [accepted, setAccepted] = useState<string | null>(null)
  useEffect(() => {
    if (open) {
      setName('')
      setTTL('60')
      setInv(null)
      setReply('')
      setError(null)
      setProblems([])
      setAccepted(null)
    }
  }, [open])
  const nameErr = name && validateSite({ ...siteFormOf(), name }, [], taken, true).name
  const make = async () => {
    setBusy(true)
    setError(null)
    try {
      setInv(await createInvitation(name.trim(), Number(ttl)))
      onDone()
    } catch (e) {
      setError(errMessage(e))
    } finally {
      setBusy(false)
    }
  }
  const accept = async () => {
    setBusy(true)
    setError(null)
    setProblems([])
    try {
      const r = await acceptReply(reply)
      setAccepted(r.site.name)
      onDone()
    } catch (e) {
      setError(errMessage(e))
      setProblems(problemsOf(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog open={open} onClose={onClose} title={t('sites.inviteTitle')} size="large">
      <div className="space-y-3 text-sm">
        {!inv && (
          <>
            <p className="text-gray-600 dark:text-gray-400">{t('sites.inviteIntro')}</p>
            <label className="block">
              {t('sites.inviteName')}
              <input className={inputClass} value={name} placeholder="cabin" onChange={(e) => setName(e.target.value.toLowerCase())} />
              <span className={sub}>{t('sites.nameHint')}</span>
              {nameErr && (
                <p role="alert" className="mt-1 text-xs text-red-600">
                  {nameErr === 'taken' ? t('sites.errNameTaken') : t('sites.errName')}
                </p>
              )}
            </label>
            <label className="block">
              {t('sites.inviteValid')}
              <select className={inputClass} value={ttl} onChange={(e) => setTTL(e.target.value)}>
                <option value="15">{t('sites.ttl15')}</option>
                <option value="60">{t('sites.ttl60')}</option>
                <option value="1440">{t('sites.ttl1440')}</option>
              </select>
            </label>
            {error && (
              <p role="alert" className="text-red-600">
                {error}
              </p>
            )}
            <div className="flex justify-end gap-2">
              <button type="button" className={btn} onClick={onClose}>
                {t('common.cancel')}
              </button>
              <button type="button" className={primary} disabled={busy || !name || !!nameErr} onClick={make}>
                {t('sites.makeCode')}
              </button>
            </div>
          </>
        )}
        {inv && !accepted && (
          <>
            <p>{t('sites.inviteStep2', { site: inv.site })}</p>
            <textarea readOnly aria-label={t('sites.invitationCode')} className={codeBox} rows={4} value={inv.code} />
            <div className="flex flex-wrap items-center gap-2">
              <Copy text={inv.code} label={t('sites.copyInvitation')} />
              <span className="text-xs text-gray-500">{t('sites.expiresIn', { minutes: minutesLeft(inv.expires) })}</span>
            </div>
            {inv.sites_enabled && <p className="text-xs text-amber-700 dark:text-amber-400">{t('sites.sitesTurnedOn')}</p>}
            <label className="block">
              {t('sites.pasteReply')}
              <textarea aria-label={t('sites.replyCode')} className={codeBox} rows={4} value={reply} onChange={(e) => setReply(e.target.value)} placeholder="GGREPLY1-…" />
            </label>
            {error && problems.length === 0 && (
              <p role="alert" className="text-red-600">
                {error}
              </p>
            )}
            <Problems list={problems} />
            <div className="flex justify-end gap-2">
              <button type="button" className={btn} onClick={onClose}>
                {t('sites.later')}
              </button>
              <button type="button" className={primary} disabled={busy || !reply.trim()} onClick={accept}>
                {t('sites.acceptReply')}
              </button>
            </div>
            <p className={sub}>{t('sites.laterHint')}</p>
          </>
        )}
        {accepted && (
          <>
            <p role="status" className="rounded bg-green-50 p-2 text-green-700 dark:bg-green-900/30 dark:text-green-200">
              {t('sites.acceptedCommit', { site: accepted })}
            </p>
            <div className="flex justify-end">
              <button type="button" className={primary} onClick={onClose}>
                {t('common.close')}
              </button>
            </div>
          </>
        )}
      </div>
    </Dialog>
  )
}

// AcceptDialog takes a reply for an invitation made earlier.
function AcceptDialog({ open, onClose, onDone }: { open: boolean; onClose: () => void; onDone: () => void }) {
  const [reply, setReply] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [problems, setProblems] = useState<SiteProblem[]>([])
  const [accepted, setAccepted] = useState<string | null>(null)
  useEffect(() => {
    if (open) {
      setReply('')
      setError(null)
      setProblems([])
      setAccepted(null)
    }
  }, [open])
  const accept = async () => {
    setError(null)
    setProblems([])
    try {
      setAccepted((await acceptReply(reply)).site.name)
      onDone()
    } catch (e) {
      setError(errMessage(e))
      setProblems(problemsOf(e))
    }
  }
  return (
    <Dialog open={open} onClose={onClose} title={t('sites.acceptTitle')}>
      <div className="space-y-3 text-sm">
        {accepted ? (
          <p role="status" className="rounded bg-green-50 p-2 text-green-700 dark:bg-green-900/30 dark:text-green-200">
            {t('sites.acceptedCommit', { site: accepted })}
          </p>
        ) : (
          <>
            <label className="block">
              {t('sites.pasteReply')}
              <textarea aria-label={t('sites.replyCode')} className={codeBox} rows={4} value={reply} onChange={(e) => setReply(e.target.value)} placeholder="GGREPLY1-…" />
            </label>
            {error && problems.length === 0 && (
              <p role="alert" className="text-red-600">
                {error}
              </p>
            )}
            <Problems list={problems} />
          </>
        )}
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            {accepted ? t('common.close') : t('common.cancel')}
          </button>
          {!accepted && (
            <button type="button" className={primary} disabled={!reply.trim()} onClick={accept}>
              {t('sites.acceptReply')}
            </button>
          )}
        </div>
      </div>
    </Dialog>
  )
}

// JoinDialog: paste a code, review what it brings, join, show the reply.
function JoinDialog({ open, onClose, onDone }: { open: boolean; onClose: () => void; onDone: () => void }) {
  const [code, setCode] = useState('')
  const [offer, setOffer] = useState<SiteOffer | null>(null)
  const [name, setName] = useState('')
  const [reply, setReply] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [problems, setProblems] = useState<SiteProblem[]>([])
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (open) {
      setCode('')
      setOffer(null)
      setName('')
      setReply(null)
      setError(null)
      setProblems([])
    }
  }, [open])
  const review = async (asName?: string) => {
    setBusy(true)
    setError(null)
    try {
      const o = await checkJoin(code, asName)
      setOffer(o)
      setProblems(o.problems)
      if (!asName) setName(o.site)
    } catch (e) {
      setError(errMessage(e))
      setOffer(null)
    } finally {
      setBusy(false)
    }
  }
  const join = async () => {
    setBusy(true)
    setError(null)
    try {
      const r = await joinSite(code, name.trim())
      setReply(r.reply)
      onDone()
    } catch (e) {
      setError(errMessage(e))
      setProblems(problemsOf(e))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog open={open} onClose={onClose} title={t('sites.joinTitle')} size="large">
      <div className="space-y-3 text-sm">
        {!offer && (
          <>
            <p className="text-gray-600 dark:text-gray-400">{t('sites.joinIntro')}</p>
            <textarea aria-label={t('sites.invitationCode')} className={codeBox} rows={4} value={code} onChange={(e) => setCode(e.target.value)} placeholder="GGSITE1-…" />
            {error && (
              <p role="alert" className="text-red-600">
                {error}
              </p>
            )}
            <div className="flex justify-end gap-2">
              <button type="button" className={btn} onClick={onClose}>
                {t('common.cancel')}
              </button>
              <button type="button" className={primary} disabled={busy || !code.trim()} onClick={() => review()}>
                {t('sites.reviewCode')}
              </button>
            </div>
          </>
        )}
        {offer && !reply && (
          <>
            <dl className="grid grid-cols-[auto,1fr] gap-x-4 gap-y-1">
              <dt className="text-gray-500">{t('sites.theirEndpoint')}</dt>
              <dd>{offer.endpoint || t('sites.dialsOut')}</dd>
              <dt className="text-gray-500">{t('sites.theirAddress')}</dt>
              <dd className="font-mono text-xs">{offer.address}</dd>
              <dt className="text-gray-500">{t('sites.theirNetworks')}</dt>
              <dd className="font-mono text-xs" data-testid="offer-networks">
                {offer.networks.length > 0 ? offer.networks.join(', ') : t('sites.noNetworks')}
              </dd>
            </dl>
            <label className="block">
              {t('sites.joinName')}
              <input className={inputClass} value={name} onChange={(e) => setName(e.target.value.toLowerCase())} onBlur={() => name && name !== offer.site && review(name)} />
              <span className={sub}>{t('sites.nameHint')}</span>
            </label>
            <Problems list={problems} />
            {error && problems.length === 0 && (
              <p role="alert" className="text-red-600">
                {error}
              </p>
            )}
            <div className="flex justify-end gap-2">
              <button type="button" className={btn} onClick={onClose}>
                {t('common.cancel')}
              </button>
              <button type="button" className={primary} disabled={busy || problems.length > 0 || !name} onClick={join}>
                {t('sites.joinAccept')}
              </button>
            </div>
          </>
        )}
        {reply && (
          <>
            <p role="status" className="rounded bg-green-50 p-2 text-green-700 dark:bg-green-900/30 dark:text-green-200">
              {t('sites.joinedStep', { site: name })}
            </p>
            <textarea readOnly aria-label={t('sites.replyCode')} className={codeBox} rows={4} value={reply} />
            <div className="flex justify-end gap-2">
              <Copy text={reply} label={t('sites.copyReply')} />
              <button type="button" className={primary} onClick={onClose}>
                {t('common.close')}
              </button>
            </div>
          </>
        )}
      </div>
    </Dialog>
  )
}

// SiteDrawer edits a site, or adds one reached through another site.
function SiteDrawer({
  site,
  remotes,
  taken,
  readOnly,
  server,
  onSave,
  onClose,
}: {
  site: Site | null // null = new site reached through another
  remotes: Site[]
  taken: string[]
  readOnly: boolean
  server: ValidationError[]
  onSave: (next: Site) => Promise<void>
  onClose: () => void
}) {
  const [f, setF] = useState<SiteForm>(() => {
    const form = siteFormOf(site ?? undefined)
    if (!site) form.via = hubs(remotes)[0] ?? ''
    return form
  })
  const [errors, setErrors] = useState<SiteErrors>({})
  const [saveError, setSaveError] = useState<string | null>(null)
  const set = (p: Partial<SiteForm>) => setF({ ...f, ...p })
  const isNew = site === null
  const paired = !!site && !site.via
  const others = remotes.filter((r) => r !== site)
  const save = async () => {
    const e = validateSite(f, others, taken, isNew)
    setErrors(e)
    if (Object.keys(e).length > 0) return
    setSaveError(null)
    try {
      await onSave(buildSite(f, site ?? undefined))
    } catch (err) {
      setSaveError(errMessage(err))
    }
  }
  const idx = site ? remotes.indexOf(site) : remotes.length
  const fromServer = (field: string) => server.find((v) => v.path.startsWith(`vpn.sites.remotes[${idx}].${field}`))?.message
  const msg = (field: keyof SiteErrors, text: string) => {
    const m = errors[field] !== undefined ? text : fromServer(field)
    return m ? (
      <p role="alert" className="mt-1 text-xs text-red-600">
        {m}
      </p>
    ) : null
  }
  return (
    <EditDrawer isOpen onClose={onClose} title={isNew ? t('sites.addViaTitle') : t('sites.editTitle', { site: site.name })}>
      <div className="space-y-4 text-sm">
        {saveError && (
          <div role="alert" className="rounded bg-red-50 p-2 text-red-700 dark:bg-red-900/30 dark:text-red-200">
            {saveError}
          </div>
        )}
        <label className="block">
          {t('sites.name')}
          <input className={inputClass} value={f.name} disabled={!isNew || readOnly} onChange={(e) => set({ name: e.target.value.toLowerCase() })} />
          {!isNew && <span className={sub}>{t('sites.nameFixed')}</span>}
          {msg('name', errors.name === 'taken' ? t('sites.errNameTaken') : t('sites.errName'))}
        </label>
        <label className="flex items-center gap-2">
          <input type="checkbox" className={check} checked={f.enabled} disabled={readOnly} onChange={(e) => set({ enabled: e.target.checked })} />
          {t('sites.siteEnabled')}
        </label>
        <label className="block">
          {t('sites.description')}
          <input className={inputClass} maxLength={80} value={f.description} disabled={readOnly} onChange={(e) => set({ description: e.target.value })} />
        </label>
        {paired && (
          <>
            <label className="block">
              {t('sites.endpoint')}
              <input className={inputClass} value={f.endpoint} disabled={readOnly} placeholder="cabin.example.net:51830" onChange={(e) => set({ endpoint: e.target.value })} />
              <span className={sub}>{t('sites.endpointHint')}</span>
              {msg('endpoint', t('sites.errHostPort'))}
            </label>
            <label className="block">
              {t('sites.keepalive')}
              <input className={inputClass} inputMode="numeric" value={f.keepalive} disabled={readOnly} placeholder="25" onChange={(e) => set({ keepalive: e.target.value })} />
              <span className={sub}>{t('sites.keepaliveHint')}</span>
              {msg('keepalive', t('sites.errKeepalive'))}
            </label>
            <dl className="grid grid-cols-[auto,1fr] gap-x-4 gap-y-1 text-xs">
              <dt className="text-gray-500">{t('sites.theirAddress')}</dt>
              <dd className="font-mono">{site.address}</dd>
              <dt className="text-gray-500">{t('sites.listenPort')}</dt>
              <dd className="font-mono">{site.listen_port}</dd>
              <dt className="text-gray-500">{t('sites.publicKey')}</dt>
              <dd className="break-all font-mono">{site.public_key}</dd>
            </dl>
          </>
        )}
        {!paired && (
          <>
            <label className="block">
              {t('sites.via')}
              <select className={inputClass} value={f.via} disabled={readOnly} onChange={(e) => set({ via: e.target.value })}>
                {hubs(remotes, site?.name).map((h) => (
                  <option key={h} value={h}>
                    {h}
                  </option>
                ))}
              </select>
              <span className={sub}>{t('sites.viaHint')}</span>
              {msg('via', t('sites.errVia'))}
            </label>
            <label className="block">
              {t('sites.theirAddress')}
              <input className={inputClass} value={f.address} disabled={readOnly} placeholder="10.254.3.3" onChange={(e) => set({ address: e.target.value })} />
              <span className={sub}>{t('sites.viaAddressHint')}</span>
              {msg('address', t('sites.errAddress'))}
            </label>
          </>
        )}
        <label className="block">
          {t('sites.theirNetworks')}
          <textarea className={inputClass + ' font-mono text-xs'} rows={3} value={f.networks} disabled={readOnly} placeholder="192.168.50.0/24" onChange={(e) => set({ networks: e.target.value })} />
          <span className={sub}>{t('sites.networksHint')}</span>
          {msg('networks', errors.networks ? t('sites.errNetwork', { network: errors.networks }) : t('sites.errNetworksNeeded'))}
        </label>
        <div className="flex justify-end gap-2">
          <button type="button" className={btn} onClick={onClose}>
            {t('common.cancel')}
          </button>
          {!readOnly && (
            <button type="button" className={primary} onClick={save}>
              {t('sites.save')}
            </button>
          )}
        </div>
      </div>
    </EditDrawer>
  )
}

const VpnSites: React.FC = () => {
  const perms = usePermissions()
  const canWrite = perms.can('write:network')
  const queryClient = useQueryClient()
  const [cfg, setCfg] = useState<Candidate | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [form, setForm] = useState<SettingsForm>(settingsOf())
  const [formErr, setFormErr] = useState<{ endpoint?: string }>({})
  const [saved, setSaved] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [server, setServer] = useState<ValidationError[]>([])
  const [dialog, setDialog] = useState<'invite' | 'join' | 'accept' | null>(null)
  const [editing, setEditing] = useState<Site | null | undefined>(undefined) // undefined = closed, null = new via-site
  const [removing, setRemoving] = useState<Site | null>(null)

  const load = useCallback(async () => {
    try {
      const c = await getCandidate()
      setCfg(c)
      setForm(settingsOf(c.vpn?.sites))
      setLoadError(null)
    } catch (e) {
      setLoadError(errMessage(e))
    }
  }, [])
  useEffect(() => {
    load()
  }, [load])

  const { data: status, isError: statusFailed } = useQuery({ queryKey: ['sites-status'], queryFn: getSitesStatus, refetchInterval: 10000, retry: false })
  const { data: invitations } = useQuery({
    queryKey: ['site-invitations'],
    queryFn: getInvitations,
    enabled: perms.mayAdmin,
    retry: false,
  })

  const stored = cfg?.vpn?.sites
  const remotes = stored?.remotes ?? []
  const taken = takenNames(cfg ?? undefined)

  // saveSection writes the whole section; validation errors under vpn.sites
  // come back to the fields that own them.
  const saveSection = async (next: SitesSettings) => {
    const res = await patchSection('vpn.sites', next)
    const mine = (res.validation_errors ?? []).filter((v) => v.path.startsWith('vpn.sites'))
    setServer(mine)
    await load()
    return mine
  }

  const saveSettings = async (ev: React.FormEvent) => {
    ev.preventDefault()
    setSaved(false)
    setSaveError(null)
    const e = validateSettings(form)
    setFormErr(e)
    if (Object.keys(e).length > 0) return
    try {
      await saveSection(buildSettings(form, stored))
      setSaved(true)
    } catch (err) {
      setSaveError(errMessage(err))
    }
  }

  const saveSite = async (next: Site) => {
    const list = editing ? remotes.map((r) => (r === editing ? next : r)) : [...remotes, next]
    await saveSection({ ...(stored ?? { enabled: true }), remotes: list })
    setEditing(undefined)
  }

  const remove = async (s: Site) => {
    try {
      // sites reached through the removed one go with it
      await saveSection({ ...(stored ?? { enabled: false }), remotes: remotes.filter((r) => r !== s && r.via !== s.name) })
    } catch (err) {
      setSaveError(errMessage(err))
    }
    setRemoving(null)
  }

  const refresh = () => {
    load()
    queryClient.invalidateQueries(['site-invitations'])
    queryClient.invalidateQueries(['sites-status'])
  }

  const opts = networkOptions(cfg ?? undefined)
  const serverMsg = (prefix: string) => server.find((v) => v.path === prefix || v.path.startsWith(prefix + '['))?.message
  const statusOf = (name: string) => status?.sites.find((s) => s.name === name)

  if (loadError) return <div className="p-6 text-red-600">{t('sites.loadFailed', { message: loadError })}</div>

  return (
    <div className="space-y-6 p-6">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h1 className="text-2xl font-bold text-gray-800 dark:text-white">{t('sites.title')}</h1>
          <p className="text-sm text-gray-500">
            {t('sites.intro')}{' '}
            <a className="text-blue-600 underline" href={sitesWikiURL} target="_blank" rel="noreferrer">
              {t('sites.wiki')}
            </a>
          </p>
        </div>
        {perms.mayAdmin && (
          <div className="flex gap-2">
            <button type="button" className={primary} onClick={() => setDialog('invite')}>
              {t('sites.invite')}
            </button>
            <button type="button" className={btn} onClick={() => setDialog('join')}>
              {t('sites.join')}
            </button>
          </div>
        )}
      </div>
      {perms.loaded && !perms.mayAdmin && <p className="text-sm text-gray-500">{t('sites.pairingAdminOnly')}</p>}
      {perms.loaded && !canWrite && <p className="text-sm text-gray-500">{t('sites.readOnly')}</p>}

      {/* this gate */}
      <form onSubmit={saveSettings} className={box + ' space-y-3'}>
        <h2 className="text-lg font-semibold text-gray-800 dark:text-white">{t('sites.thisGate')}</h2>
        {saved && (
          <div role="status" className="rounded bg-green-50 p-2 text-green-700 dark:bg-green-900/30 dark:text-green-200">
            {t('sites.saved')}
          </div>
        )}
        {saveError && (
          <div role="alert" className="rounded bg-red-50 p-2 text-red-700 dark:bg-red-900/30 dark:text-red-200">
            {saveError}
          </div>
        )}
        <label className="flex items-center gap-2">
          <input type="checkbox" className={check} checked={form.enabled} disabled={!canWrite} onChange={(e) => setForm({ ...form, enabled: e.target.checked })} />
          {t('sites.enabled')}
        </label>
        <div>
          <span className="text-gray-500">{t('sites.ownAddress')}</span>{' '}
          <span className="font-mono text-xs">{stored?.address || t('sites.addressLater')}</span>
        </div>
        <label className="block">
          {t('sites.ownEndpoint')}
          <input className={inputClass} value={form.endpoint} disabled={!canWrite} placeholder="home.example.net" onChange={(e) => setForm({ ...form, endpoint: e.target.value })} />
          <span className={sub}>{t('sites.ownEndpointHint')}</span>
          {(formErr.endpoint || serverMsg('vpn.sites.endpoint')) && (
            <p role="alert" className="mt-1 text-xs text-red-600">
              {formErr.endpoint ? t('sites.errHost') : serverMsg('vpn.sites.endpoint')}
            </p>
          )}
        </label>
        <fieldset>
          <legend>{t('sites.ownNetworks')}</legend>
          <p className={sub}>{t('sites.ownNetworksHint')}</p>
          <div className="mt-1 flex flex-wrap gap-3">
            {opts.map((o) => (
              <label key={o} className="flex items-center gap-1">
                <input
                  type="checkbox"
                  className={check}
                  disabled={!canWrite}
                  checked={form.networks.includes(o)}
                  onChange={(e) => setForm({ ...form, networks: e.target.checked ? [...form.networks, o] : form.networks.filter((n) => n !== o) })}
                />
                {o}
              </label>
            ))}
          </div>
          {serverMsg('vpn.sites.networks') && (
            <p role="alert" className="mt-1 text-xs text-red-600">
              {serverMsg('vpn.sites.networks')}
            </p>
          )}
        </fieldset>
        <fieldset>
          <legend>{t('sites.adminAccess')}</legend>
          <p className={sub}>{t('sites.adminAccessHint')}</p>
          <div className="mt-1 flex gap-4">
            <label className="flex items-center gap-1">
              <input type="checkbox" className={check} disabled={!canWrite} checked={form.https} onChange={(e) => setForm({ ...form, https: e.target.checked })} />
              HTTPS
            </label>
            <label className="flex items-center gap-1">
              <input type="checkbox" className={check} disabled={!canWrite} checked={form.ssh} onChange={(e) => setForm({ ...form, ssh: e.target.checked })} />
              SSH
            </label>
          </div>
        </fieldset>
        <label className="flex items-start gap-2">
          <input type="checkbox" className={check + ' mt-0.5'} disabled={!canWrite} checked={form.announce} onChange={(e) => setForm({ ...form, announce: e.target.checked })} />
          <span>
            {t('sites.announce')}
            <span className={'block ' + sub}>{t('sites.announceHint')}</span>
          </span>
        </label>
        {serverMsg('vpn.sites.address') && (
          <p role="alert" className="text-xs text-red-600">
            {serverMsg('vpn.sites.address')}
          </p>
        )}
        {canWrite && (
          <button type="submit" className={primary}>
            {t('sites.saveSettings')}
          </button>
        )}
      </form>

      {/* the sites */}
      <section className={box}>
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <h2 className="text-lg font-semibold text-gray-800 dark:text-white">{t('sites.sites')}</h2>
          {canWrite && hubs(remotes).length > 0 && (
            <button type="button" className={btn} onClick={() => setEditing(null)}>
              {t('sites.addVia')}
            </button>
          )}
        </div>
        {statusFailed && <p className="mb-2 text-xs text-gray-500">{t('sites.statusUnavailable')}</p>}
        {remotes.length === 0 ? (
          <p className="text-gray-500">{t('sites.none')}</p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-left text-sm" aria-label={t('sites.sitesTable')}>
              <thead className="text-xs uppercase text-gray-500">
                <tr>
                  <th className="py-1 pr-3">{t('sites.col.name')}</th>
                  <th className="py-1 pr-3">{t('sites.col.status')}</th>
                  <th className="py-1 pr-3">{t('sites.col.handshake')}</th>
                  <th className="py-1 pr-3">{t('sites.col.transfer')}</th>
                  <th className="py-1 pr-3">{t('sites.col.endpoint')}</th>
                  <th className="py-1 pr-3">{t('sites.col.networks')}</th>
                  <th className="py-1" />
                </tr>
              </thead>
              <tbody>
                {remotes.map((r) => {
                  const st = statusOf(r.name)
                  const badge = siteBadge(st, r.enabled && !!stored?.enabled)
                  const label = {
                    reachable: t('sites.badge.reachable'),
                    noAnswer: t('sites.badge.noAnswer'),
                    noHandshake: t('sites.badge.noHandshake'),
                    disabled: t('sites.badge.disabled'),
                    notRunning: t('sites.badge.notRunning'),
                  }[badge.key]
                  return (
                    <tr key={r.name} className="border-t border-gray-100 dark:border-gray-700">
                      <td className="py-2 pr-3">
                        <div className="font-medium">{r.name}</div>
                        {r.via && <div className="text-xs text-gray-500">{t('sites.throughSite', { site: r.via })}</div>}
                        {r.description && <div className="text-xs text-gray-500">{r.description}</div>}
                      </td>
                      <td className="py-2 pr-3">
                        <span className={badgeCls[badge.kind]} aria-label={t('sites.statusOf', { site: r.name })}>
                          {label}
                          {badge.key === 'reachable' && st?.rtt_ms !== undefined ? ` · ${st.rtt_ms} ms` : ''}
                        </span>
                      </td>
                      <td className="py-2 pr-3">{r.via ? '—' : st && st.handshake_age >= 0 ? t('sites.ago', { age: formatAge(st.handshake_age) }) : t('sites.never')}</td>
                      <td className="py-2 pr-3 whitespace-nowrap">{r.via || !st ? '—' : `↓ ${formatBytes(st.rx_bytes)} ↑ ${formatBytes(st.tx_bytes)}`}</td>
                      <td className="py-2 pr-3 font-mono text-xs">{r.via ? '—' : st?.endpoint || r.endpoint || t('sites.theyDial')}</td>
                      <td className="py-2 pr-3 font-mono text-xs">{(r.networks ?? []).join(', ') || '—'}</td>
                      <td className="py-2 text-right whitespace-nowrap">
                        <button type="button" className="px-2 text-blue-600" aria-label={t('sites.editSite', { site: r.name })} onClick={() => setEditing(r)}>
                          {canWrite ? t('sites.edit') : t('sites.view')}
                        </button>
                        {canWrite && (
                          <button type="button" className="px-2 text-red-600" aria-label={t('sites.removeSite', { site: r.name })} onClick={() => setRemoving(r)}>
                            {t('sites.delete')}
                          </button>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
        {server.length > 0 && editing === undefined && (
          <ul role="alert" className="mt-3 rounded bg-yellow-50 p-2 text-xs text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-200">
            {server.map((v) => (
              <li key={v.path + v.message}>
                <code>{v.path}</code>: {v.message}
              </li>
            ))}
          </ul>
        )}
        <p className={'mt-3 ' + sub}>{t('sites.policiesHint')}</p>
      </section>

      {/* open invitations */}
      {perms.mayAdmin && (invitations?.invitations.length ?? 0) > 0 && (
        <section className={box}>
          <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
            <h2 className="text-lg font-semibold text-gray-800 dark:text-white">{t('sites.openInvitations')}</h2>
            <button type="button" className={btn} onClick={() => setDialog('accept')}>
              {t('sites.acceptReply')}
            </button>
          </div>
          <ul className="space-y-1">
            {invitations!.invitations.map((i) => (
              <li key={i.id} className="flex flex-wrap items-center justify-between gap-2">
                <span>
                  {i.site} · {t('sites.expiresIn', { minutes: minutesLeft(i.expires) })}
                </span>
                <button
                  type="button"
                  className="px-2 text-red-600"
                  aria-label={t('sites.cancelInvitation', { site: i.site })}
                  onClick={async () => {
                    await cancelInvitation(i.id)
                    refresh()
                  }}
                >
                  {t('common.cancel')}
                </button>
              </li>
            ))}
          </ul>
        </section>
      )}

      <InviteDialog open={dialog === 'invite'} onClose={() => setDialog(null)} onDone={refresh} taken={taken} />
      <JoinDialog open={dialog === 'join'} onClose={() => setDialog(null)} onDone={refresh} />
      <AcceptDialog open={dialog === 'accept'} onClose={() => setDialog(null)} onDone={refresh} />

      {editing !== undefined && (
        <SiteDrawer
          site={editing}
          remotes={remotes}
          taken={taken}
          readOnly={!canWrite}
          server={server}
          onSave={saveSite}
          onClose={() => {
            setEditing(undefined)
            setServer([])
          }}
        />
      )}

      <Dialog open={removing !== null} onClose={() => setRemoving(null)} title={t('sites.removeTitle')} size="small">
        {removing && (
          <div className="space-y-3 text-sm">
            <p>{t('sites.removeBody', { site: removing.name })}</p>
            {remotes.some((r) => r.via === removing.name) && <p className="text-amber-700 dark:text-amber-400">{t('sites.removeAlsoVia')}</p>}
            <div className="flex justify-end gap-2">
              <button type="button" className={btn} onClick={() => setRemoving(null)}>
                {t('common.cancel')}
              </button>
              <button type="button" className="min-h-[40px] rounded bg-red-600 px-4 text-sm text-white" onClick={() => remove(removing)}>
                {t('sites.delete')}
              </button>
            </div>
          </div>
        )}
      </Dialog>
    </div>
  )
}

export default VpnSites
