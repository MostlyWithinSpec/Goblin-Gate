// Network → Routing: BGP and OSPF (FRR). One card per protocol with its
// settings, and below them what the running FRR reports now: neighbour state,
// uptime, prefix counts and the routes learned. The whole `routing` section is
// saved as one object, so each card PATCHes the section it belongs to and keeps
// the other card's stored fields untouched.
import React, { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getCandidate, patchSection, type Candidate } from '../../api/objects'
import { getRoutingStatus, type BGPNeighborStatus, type LearnedRoute, type OSPFNeighborStatus, type RoutingStatus } from '../../api/routing'
import type { ValidationError } from '../../api/policy'
import { usePermissions } from '../../api/permissions'
import { inputClass } from '../../components/ConfigList'
import {
  announceOptions,
  areaID,
  bgpBadge,
  buildRouting,
  emptyArea,
  emptyNeighbor,
  emptyRule,
  formatUptime,
  frrRunning,
  interfaceOptions,
  moveRule,
  ospfBadge,
  routingWikiURL,
  sortRoutes,
  validateRouting,
  formOf,
  type AreaForm,
  type NeighborForm,
  type RoutingForm,
  type RuleForm,
} from './routingLogic'
import { t } from '../../i18n'

const cls = (kind: 'ok' | 'warn' | 'bad') =>
  kind === 'ok'
    ? 'rounded bg-green-100 px-1.5 py-0.5 text-xs text-green-800 dark:bg-green-900/40 dark:text-green-200'
    : kind === 'warn'
      ? 'rounded bg-yellow-100 px-1.5 py-0.5 text-xs text-yellow-800 dark:bg-yellow-900/40 dark:text-yellow-200'
      : 'rounded bg-red-100 px-1.5 py-0.5 text-xs text-red-700 dark:bg-red-900/40 dark:text-red-200'

// Section bundles the three messages one card owes its reader: a banner for a
// save that failed, the fields' own messages, and the server's complaints about
// the saved section that no field of this card owns.
function Section({ errors, rest }: { errors: Record<string, string | undefined>; rest: ValidationError[] }) {
  return (
    <>
      {errors.save && (
        <div role="alert" className="mb-3 rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900/30 dark:text-red-200">
          {errors.save}
        </div>
      )}
      {rest.length > 0 && (
        <ul role="alert" className="mb-3 rounded bg-yellow-50 p-2 text-sm text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-200">
          {rest.map((v) => (
            <li key={v.path + v.message}>
              <code>{v.path}</code>: {v.message}
            </li>
          ))}
        </ul>
      )}
    </>
  )
}

// Err shows the message for one field, whether it came from the page's own check
// or from the server. Both use the same path under "routing.".
function Err({ errors, server, field }: { errors: Record<string, string | undefined>; server: ValidationError[]; field: string }) {
  const fromServer = server.find((v) => v.path === `routing.${field}` || v.path.startsWith(`routing.${field}[`))
  const message = errors[field] ?? fromServer?.message
  if (!message) return null
  return (
    <p role="alert" className="mt-1 text-xs text-red-600">
      {message}
    </p>
  )
}

const box = 'rounded bg-white p-4 text-sm text-gray-700 shadow-sm dark:bg-gray-800 dark:text-gray-300'
const check = 'h-4 w-4 rounded border-gray-300 dark:border-gray-600'
const sub = 'mt-1 text-xs text-gray-500'
const innerBox = 'rounded border border-gray-200 p-3 dark:border-gray-700'

// Rules edits one neighbour's import or export list: one row per rule with the
// order it is matched in, and up/down buttons to move it. The name of a repeated
// control is the translated words, then which rule, then which filter. Rules also
// holds the same field key validateRouting writes its errors under
// ('bgp.neighbors[0].import') so a message lands below the field it belongs to.
function Rules({
  title,
  group,
  field,
  hint,
  list,
  errors,
  server,
  onChange,
}: {
  title: string;
  group: string;
  field: string;
  hint: string;
  list: RuleForm[];
  errors: Record<string, string | undefined>;
  server: ValidationError[];
  onChange: (next: RuleForm[]) => void
}) {
  const set = (i: number, patch: Partial<RuleForm>) => onChange(list.map((r, j) => (j === i ? { ...r, ...patch } : r)))
  // The words a name is built from. A label helper cannot take the key as a
  // parameter — the i18n test only accepts a literal inside t() — so the
  // appends are written out at each control.
  const where = t('routing.inFilter', { filter: group })
  return (
    <fieldset className={innerBox}>
      <legend className="font-medium">{title}</legend>
      <p className="mb-2 text-xs text-gray-500">{hint}</p>
      {list.map((r, i) => (
        <div key={i} className="mb-2 flex flex-wrap items-start gap-2">
          <select
            aria-label={`${t('routing.ruleAction')} ${i + 1} ${where}`}
            className={inputClass + ' w-24'}
            value={r.action}
            onChange={(e) => set(i, { action: e.target.value })}
          >
            <option value="permit">{t('routing.permit')}</option>
            <option value="deny">{t('routing.deny')}</option>
          </select>
          <label className="block min-w-40 flex-1">
            {t('routing.rulePrefix')}
            <input
              aria-label={`${t('routing.rulePrefix')} ${i + 1} ${where}`}
              className={inputClass + ' font-mono text-xs'}
              placeholder="10.0.0.0/8"
              value={r.prefix}
              onChange={(e) => set(i, { prefix: e.target.value })}
            />
            <Err errors={errors} server={server} field={`${field}[${i}].prefix`} />
          </label>
          <label className="block w-28">
            {t('routing.ruleMaxLength')}
            <input
              aria-label={`${t('routing.ruleMaxLength')} ${i + 1} ${where}`}
              className={inputClass}
              inputMode="numeric"
              value={r.max_length}
              onChange={(e) => set(i, { max_length: e.target.value })}
            />
            <Err errors={errors} server={server} field={`${field}[${i}].max_length`} />
          </label>
          <span className="flex gap-1 pt-5">
            <button type="button" aria-label={`${t('routing.moveUp')} ${i + 1} ${where}`} className="h-10 w-10 text-gray-500" onClick={() => onChange(moveRule(list, i, -1))}>
              ↑
            </button>
            <button type="button" aria-label={`${t('routing.moveDown')} ${i + 1} ${where}`} className="h-10 w-10 text-gray-500" onClick={() => onChange(moveRule(list, i, 1))}>
              ↓
            </button>
            <button type="button" aria-label={`${t('routing.removeRule')} ${i + 1} ${where}`} className="h-10 w-10 text-red-600" onClick={() => onChange(list.filter((_, j) => j !== i))}>
              ✕
            </button>
          </span>
        </div>
      ))}
      {list.length === 0 && <p className="mb-2 text-xs text-gray-500">{t('routing.noRules')}</p>}
      <button type="button" aria-label={t('routing.addRule') + ' ' + where} className="min-h-[40px] rounded border border-gray-300 px-3 text-xs dark:border-gray-600" onClick={() => onChange([...list, emptyRule()])}>
        {t('routing.addRule')}
      </button>
    </fieldset>
  )
}

// Neighbor edits one BGP neighbour. The name of each control names the field and
// which neighbour it belongs to, so two sessions never share a label.
function Neighbor({
  n,
  index,
  errors,
  server,
  known,
  onChange,
}: {
  n: NeighborForm;
  index: number;
  errors: Record<string, string | undefined>;
  server: ValidationError[];
  known: boolean;
  onChange: (next: NeighborForm) => void
}) {
  const set = (patch: Partial<NeighborForm>) => onChange({ ...n, ...patch })
  const base = `bgp.neighbors[${index}]`
  const fe = (f: string) => `${base}.${f}`
  const whose = t('routing.inNeighbor', { neighbor: t('routing.neighborNumber', { index: index + 1 }) })
  return (
    <div className={innerBox}>
      <div className="mb-2 flex items-center gap-2">
        <input
          type="checkbox"
          aria-label={`${t('routing.neighborEnabled')} ${whose}`}
          className={check}
          checked={n.enabled}
          onChange={(e) => set({ enabled: e.target.checked })}
        />
        <span>{t('routing.neighborEnabled')}</span>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="block">
          {t('routing.neighborAddress')}
          <input
            aria-label={`${t('routing.neighborAddress')} ${whose}`}
            className={inputClass + ' font-mono'}
            placeholder="192.0.2.1"
            value={n.address}
            onChange={(e) => set({ address: e.target.value })}
          />
          <Err errors={errors} server={server} field={fe('address')} />
        </label>
        <label className="block">
          {t('routing.neighborRemoteAS')}
          <input
            aria-label={`${t('routing.neighborRemoteAS')} ${whose}`}
            className={inputClass}
            inputMode="numeric"
            placeholder="65001"
            value={n.remote_as}
            onChange={(e) => set({ remote_as: e.target.value })}
          />
          <Err errors={errors} server={server} field={fe('remote_as')} />
        </label>
        <label className="block">
          {t('routing.neighborDescription')}
          <input
            aria-label={`${t('routing.neighborDescription')} ${whose}`}
            className={inputClass}
            placeholder={t('routing.neighborDescriptionPlaceholder')}
            value={n.description}
            onChange={(e) => set({ description: e.target.value })}
          />
          <Err errors={errors} server={server} field={fe('description')} />
        </label>
        <label className="block">
          {t('routing.neighborPassword')}
          <input
            type="password"
            aria-label={`${t('routing.neighborPassword')} ${whose}`}
            className={inputClass}
            placeholder={known ? t('routing.unchanged') : ''}
            value={n.password}
            onChange={(e) => set({ password: e.target.value })}
          />
          <Err errors={errors} server={server} field={fe('password')} />
          <p className={sub}>{t('routing.passwordNote')}</p>
        </label>
        <label className="block">
          {t('routing.neighborMultihop')}
          <input
            aria-label={`${t('routing.neighborMultihop')} ${whose}`}
            className={inputClass}
            inputMode="numeric"
            placeholder="0"
            value={n.multihop}
            onChange={(e) => set({ multihop: e.target.value })}
          />
          <Err errors={errors} server={server} field={fe('multihop')} />
          <p className={sub}>{t('routing.multihopNote')}</p>
        </label>
      </div>
      <div className="mt-3 grid gap-3">
        <Rules
          title={t('routing.importFilter')}
          group={t('routing.importFilter') + ' ' + (index + 1)}
          field={`${base}.import`}
          hint={t('routing.filterHint')}
          list={n.import}
          errors={errors}
          server={server}
          onChange={(import_) => set({ import: import_ })}
        />
        <Rules
          title={t('routing.exportFilter')}
          group={t('routing.exportFilter') + ' ' + (index + 1)}
          field={`${base}.export`}
          hint={t('routing.filterHint')}
          list={n.export}
          errors={errors}
          server={server}
          onChange={(export_) => set({ export: export_ })}
        />
      </div>
    </div>
  )
}

// Area edits one OSPF area and the interfaces inside it. The name of a repeated
// control is the translated words, then which interface, then which area.
function Area({
  area,
  index,
  options,
  errors,
  server,
  onChange,
}: {
  area: AreaForm;
  index: number;
  options: string[];
  errors: Record<string, string | undefined>;
  server: ValidationError[];
  onChange: (next: AreaForm) => void
}) {
  const set = (patch: Partial<AreaForm>) => onChange({ ...area, ...patch })
  const base = `ospf.areas[${index}]`
  const fe = (f: string) => `${base}.${f}`
  const setIface = (i: number, patch: Partial<AreaForm['interfaces'][number]>) =>
    set({ interfaces: area.interfaces.map((x, j) => (j === i ? { ...x, ...patch } : x)) })
  const unused = (name: string) => !area.interfaces.some((x) => x.name === name)
  const where = t('routing.inArea', { area: t('routing.areaNumber', { index: index + 1 }) })
  return (
    <div className={innerBox}>
      <label className="block w-48 max-w-full">
        {t('routing.areaId')}
        <input
          aria-label={t('routing.areaId') + ' ' + where}
          className={inputClass + ' font-mono'}
          placeholder="0"
          value={area.id}
          onChange={(e) => set({ id: e.target.value })}
        />
        <Err errors={errors} server={server} field={fe('id')} />
        {area.id.trim() && areaID(area.id.trim()) && areaID(area.id.trim()) !== area.id.trim() && (
          <p className={sub}>{t('routing.areaIdDotted', { id: areaID(area.id.trim())! })}</p>
        )}
      </label>
      <div className="mt-3">
        <p className="mb-1 font-medium">{t('routing.areaInterfaces')}</p>
        {area.interfaces.map((x, i) => (
          <div key={i} className="mb-2 flex flex-wrap items-start gap-2">
            <label className="block min-w-40 flex-1">
              {t('routing.areaInterface')}
              <select
                aria-label={`${t('routing.areaInterface')} ${i + 1} ${where}`}
                className={inputClass}
                value={x.name}
                onChange={(e) => setIface(i, { name: e.target.value })}
              >
                <option value="">{t('routing.chooseInterface')}</option>
                {options.filter((o) => o === x.name || !area.interfaces.some((y, j) => j !== i && y.name === o)).map((o) => (
                  <option key={o} value={o}>
                    {o}
                  </option>
                ))}
              </select>
              <Err errors={errors} server={server} field={`${fe('interfaces')}[${i}].name`} />
            </label>
            <label className="block w-28">
              {t('routing.areaCost')}
              <input
                aria-label={`${t('routing.areaCost')} ${i + 1} ${where}`}
                className={inputClass}
                inputMode="numeric"
                placeholder="0"
                value={x.cost}
                onChange={(e) => setIface(i, { cost: e.target.value })}
              />
              <Err errors={errors} server={server} field={`${fe('interfaces')}[${i}].cost`} />
            </label>
            <label className="flex items-center gap-2 pt-6">
              <input
                type="checkbox"
                aria-label={`${t('routing.areaPassive')} ${i + 1} ${where}`}
                className={check}
                checked={x.passive}
                onChange={(e) => setIface(i, { passive: e.target.checked })}
              />
              <span>{t('routing.areaPassive')}</span>
            </label>
            <button
              type="button"
              aria-label={`${t('routing.removeInterface')} ${i + 1} ${where}`}
              className="h-10 w-10 pt-6 text-red-600"
              onClick={() => set({ interfaces: area.interfaces.filter((_, j) => j !== i) })}
            >
              ✕
            </button>
          </div>
        ))}
        {area.interfaces.length === 0 && <p className="mb-2 text-xs text-gray-500">{t('routing.noInterfaces')}</p>}
        <button
          type="button"
          aria-label={t('routing.addInterface') + ' ' + where}
          className="min-h-[40px] rounded border border-gray-300 px-3 text-xs dark:border-gray-600"
          disabled={options.length === 0}
          onClick={() => set({ interfaces: [...area.interfaces, { name: options.find(unused) ?? '', cost: '', passive: false }] })}
        >
          {t('routing.addInterface')}
        </button>
      </div>
    </div>
  )
}

// BGPSection is the BGP card: its settings, and the sessions FRR reports while it
// is on.
function BGPSection({
  form,
  setForm,
  cfg,
  status,
  canEdit,
  saved,
  onSave,
}: {
  form: RoutingForm;
  setForm: (patch: Partial<RoutingForm>) => void;
  cfg: Candidate | null;
  status: RoutingStatus | undefined;
  canEdit: boolean;
  saved: boolean;
  onSave: (next: RoutingForm) => void
}) {
  const [errors, setErrors] = useState<Record<string, string | undefined>>({})
  const [serverErrors, setServerErrors] = useState<ValidationError[]>([])
  const announce = announceOptions(cfg ?? {})

  const save = async (ev: React.FormEvent) => {
    ev.preventDefault()
    const errs = validateRouting(form)
    setErrors(errs)
    if (Object.keys(errs).length > 0) return
    try {
      const res = await patchSection('routing', buildRouting(form))
      setServerErrors((res.validation_errors ?? []).filter((v) => v.path.startsWith('routing.')))
      setErrors({})
      onSave(form)
    } catch (e) {
      setErrors({ save: (e as Error).message })
    }
  }

  return (
    <section className="mb-6">
      <h2 className="mb-1 text-lg font-semibold text-gray-800 dark:text-white">{t('routing.bgp')}</h2>
      <form onSubmit={save} noValidate aria-label={t('routing.bgp')} className={box}>
        <Section errors={errors} rest={serverErrors} />
        {saved && (
          <div role="status" className="mb-3 rounded bg-green-50 p-2 text-sm text-green-700 dark:bg-green-900/20 dark:text-green-300">
            {t('routing.saved')}
          </div>
        )}
        <label className="flex items-center gap-2">
          <input
            type="checkbox"
            aria-label={t('routing.enableBGP')}
            className={check}
            checked={form.bgp_enabled}
            disabled={!canEdit}
            onChange={(e) => setForm({ bgp_enabled: e.target.checked })}
          />
          <span>{t('routing.enableBGP')}</span>
        </label>
        {form.bgp_enabled && (
          <div className="mt-3 grid gap-3">
            <div className="grid gap-3 sm:grid-cols-2">
              <label className="block">
                {t('routing.asNumber')}
                <input
                  aria-label={t('routing.asNumber')}
                  className={inputClass}
                  inputMode="numeric"
                  placeholder="64512"
                  value={form.bgp_as}
                  disabled={!canEdit}
                  onChange={(e) => setForm({ bgp_as: e.target.value })}
                />
                <Err errors={errors} server={serverErrors} field="bgp.as" />
              </label>
              <label className="block">
                {t('routing.routerId')}
                <input
                  aria-label={t('routing.routerId')}
                  className={inputClass + ' font-mono'}
                  placeholder={t('routing.routerIdPlaceholder')}
                  value={form.router_id}
                  disabled={!canEdit}
                  onChange={(e) => setForm({ router_id: e.target.value })}
                />
                <Err errors={errors} server={serverErrors} field="router_id" />
                <p className={sub}>{t('routing.routerIdNote')}</p>
              </label>
            </div>
            <fieldset>
              <legend>{t('routing.announce')}</legend>
              <p className="mb-1 text-xs text-gray-500">{t('routing.announceNote')}</p>
              {announce.length === 0 && <p className="text-xs text-gray-500">{t('routing.noAnnounceOptions')}</p>}
              <div className="max-h-48 overflow-y-auto rounded border border-gray-200 p-2 dark:border-gray-600">
                {announce.map((o) => (
                  <label key={o.value} className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      aria-label={t('routing.announceItem', { name: o.value })}
                      className={check}
                      checked={form.bgp_announce.includes(o.value)}
                      disabled={!canEdit}
                      onChange={(e) =>
                        setForm({ bgp_announce: e.target.checked ? [...form.bgp_announce, o.value] : form.bgp_announce.filter((x) => x !== o.value) })
                      }
                    />
                    <span className="font-mono text-xs">{o.label}</span>
                  </label>
                ))}
              </div>
              <Err errors={errors} server={serverErrors} field="bgp.announce" />
            </fieldset>
            <div className="flex flex-wrap gap-4">
              <label className="flex items-center gap-2">
                <input
                  type="checkbox"
                  aria-label={t('routing.redistributeConnected')}
                  className={check}
                  checked={form.bgp_redistribute_connected}
                  disabled={!canEdit}
                  onChange={(e) => setForm({ bgp_redistribute_connected: e.target.checked })}
                />
                <span>{t('routing.redistributeConnected')}</span>
              </label>
              <label className="flex items-center gap-2">
                <input
                  type="checkbox"
                  aria-label={t('routing.redistributeStatic')}
                  className={check}
                  checked={form.bgp_redistribute_static}
                  disabled={!canEdit}
                  onChange={(e) => setForm({ bgp_redistribute_static: e.target.checked })}
                />
                <span>{t('routing.redistributeStatic')}</span>
              </label>
            </div>
            <div className="grid gap-3">
              <p className="font-medium">{t('routing.neighbors')}</p>
              {form.bgp_neighbors.map((n, i) => (
                <Neighbor
                  key={i}
                  n={n}
                  index={i}
                  errors={errors}
                  server={serverErrors}
                  known={n.password === ''}
                  onChange={(next) => setForm({ bgp_neighbors: form.bgp_neighbors.map((x, j) => (j === i ? next : x)) })}
                />
              ))}
              {form.bgp_neighbors.length === 0 && <p className="text-xs text-gray-500">{t('routing.noNeighbors')}</p>}
              <Err errors={errors} server={serverErrors} field="bgp.neighbors" />
              {canEdit && (
                <button type="button" className="min-h-[40px] w-fit rounded border border-gray-300 px-3 text-xs dark:border-gray-600" onClick={() => setForm({ bgp_neighbors: [...form.bgp_neighbors, emptyNeighbor()] })}>
                  {t('routing.addNeighbor')}
                </button>
              )}
            </div>
            {canEdit && (
              <div className="flex justify-end">
                <button type="submit" className="min-h-[40px] rounded bg-blue-600 px-4 py-1 text-white">
                  {t('routing.saveBGP')}
                </button>
              </div>
            )}
          </div>
        )}
      </form>
      {status?.bgp_enabled && <BGPStatus status={status} />}
    </section>
  )
}

function BGPStatus({ status }: { status: RoutingStatus }) {
  return (
    <div className="mt-3 rounded bg-white p-4 text-sm shadow-sm dark:bg-gray-800 dark:text-gray-300">
      <h3 className="mb-2 font-semibold">{t('routing.bgpStatus')}</h3>
      {!frrRunning(status) && (
        <p role="status" className="mb-2 rounded bg-yellow-50 p-2 text-xs text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-200">
          {t('routing.frrStarting')}
        </p>
      )}
      {status.bgp.length === 0 && <p className="text-xs text-gray-500">{t('routing.noBGPNeighbors')}</p>}
      {status.bgp.length > 0 && (
        <table className="min-w-full text-sm" aria-label={t('routing.bgpStatusTable')}>
          <thead className="text-left text-xs uppercase text-gray-500">
            <tr>
              <th className="px-2 py-1">{t('routing.col.address')}</th>
              <th className="px-2 py-1">{t('routing.col.remoteAS')}</th>
              <th className="px-2 py-1">{t('routing.col.state')}</th>
              <th className="px-2 py-1">{t('routing.col.uptime')}</th>
              <th className="px-2 py-1">{t('routing.col.prefixesIn')}</th>
              <th className="px-2 py-1">{t('routing.col.prefixesOut')}</th>
            </tr>
          </thead>
          <tbody>
            {status.bgp.map((n: BGPNeighborStatus) => (
              <tr key={n.address} className="border-t border-gray-100 dark:border-gray-700">
                <td className="px-2 py-1 font-mono">
                  {n.address}
                  {n.description && <span className="ml-2 text-xs text-gray-500">{n.description}</span>}
                </td>
                <td className="px-2 py-1">{n.remote_as}</td>
                <td className="px-2 py-1">
                  <span className={cls(bgpBadge(n))}>{n.up ? t('routing.established') : n.state}</span>
                </td>
                <td className="px-2 py-1">{formatUptime(n.uptime_seconds) || t('routing.notUp')}</td>
                <td className="px-2 py-1">{n.prefixes_received}</td>
                <td className="px-2 py-1">{n.prefixes_sent}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}

// OSPFSection is the OSPF card. OSPFv2 (IPv4) only.
function OSPFSection({
  form,
  setForm,
  cfg,
  status,
  canEdit,
  saved,
  onSave,
}: {
  form: RoutingForm;
  setForm: (patch: Partial<RoutingForm>) => void;
  cfg: Candidate | null;
  status: RoutingStatus | undefined;
  canEdit: boolean;
  saved: boolean;
  onSave: (next: RoutingForm) => void
}) {
  const [errors, setErrors] = useState<Record<string, string | undefined>>({})
  const [serverErrors, setServerErrors] = useState<ValidationError[]>([])
  const ifaces = interfaceOptions(cfg ?? {})

  const save = async (ev: React.FormEvent) => {
    ev.preventDefault()
    const errs = validateRouting(form)
    setErrors(errs)
    if (Object.keys(errs).length > 0) return
    try {
      const res = await patchSection('routing', buildRouting(form))
      setServerErrors((res.validation_errors ?? []).filter((v) => v.path.startsWith('routing.')))
      setErrors({})
      onSave(form)
    } catch (e) {
      setErrors({ save: (e as Error).message })
    }
  }

  return (
    <section className="mb-6">
      <h2 className="mb-1 text-lg font-semibold text-gray-800 dark:text-white">{t('routing.ospf')}</h2>
      <form onSubmit={save} noValidate aria-label={t('routing.ospf')} className={box}>
        <Section errors={errors} rest={serverErrors} />
        {saved && (
          <div role="status" className="mb-3 rounded bg-green-50 p-2 text-sm text-green-700 dark:bg-green-900/20 dark:text-green-300">
            {t('routing.saved')}
          </div>
        )}
        <label className="flex items-center gap-2">
          <input
            type="checkbox"
            aria-label={t('routing.enableOSPF')}
            className={check}
            checked={form.ospf_enabled}
            disabled={!canEdit}
            onChange={(e) => setForm({ ospf_enabled: e.target.checked })}
          />
          <span>{t('routing.enableOSPF')}</span>
        </label>
        <p className="mt-1 text-xs text-gray-500">{t('routing.ospfNote')}</p>
        {form.ospf_enabled && (
          <div className="mt-3 grid gap-3">
            <div className="grid gap-3">
              {form.ospf_areas.map((a, i) => (
                <Area
                  key={i}
                  area={a}
                  index={i}
                  options={ifaces}
                  errors={errors}
                  server={serverErrors}
                  onChange={(next) => setForm({ ospf_areas: form.ospf_areas.map((x, j) => (j === i ? next : x)) })}
                />
              ))}
              <Err errors={errors} server={serverErrors} field="ospf.areas" />
              {canEdit && (
                <button type="button" className="min-h-[40px] w-fit rounded border border-gray-300 px-3 text-xs dark:border-gray-600" onClick={() => setForm({ ospf_areas: [...form.ospf_areas, emptyArea()] })}>
                  {t('routing.addArea')}
                </button>
              )}
            </div>
            <div className="flex flex-wrap gap-4">
              <label className="flex items-center gap-2">
                <input
                  type="checkbox"
                  aria-label={t('routing.redistributeConnected')}
                  className={check}
                  checked={form.ospf_redistribute_connected}
                  disabled={!canEdit}
                  onChange={(e) => setForm({ ospf_redistribute_connected: e.target.checked })}
                />
                <span>{t('routing.redistributeConnected')}</span>
              </label>
              <label className="flex items-center gap-2">
                <input
                  type="checkbox"
                  aria-label={t('routing.redistributeStatic')}
                  className={check}
                  checked={form.ospf_redistribute_static}
                  disabled={!canEdit}
                  onChange={(e) => setForm({ ospf_redistribute_static: e.target.checked })}
                />
                <span>{t('routing.redistributeStatic')}</span>
              </label>
            </div>
            {canEdit && (
              <div className="flex justify-end">
                <button type="submit" className="min-h-[40px] rounded bg-blue-600 px-4 py-1 text-white">
                  {t('routing.saveOSPF')}
                </button>
              </div>
            )}
          </div>
        )}
      </form>
      {status?.ospf_enabled && <OSPFStatus status={status} />}
    </section>
  )
}

function OSPFStatus({ status }: { status: RoutingStatus }) {
  return (
    <div className="mt-3 rounded bg-white p-4 text-sm shadow-sm dark:bg-gray-800 dark:text-gray-300">
      <h3 className="mb-2 font-semibold">{t('routing.ospfStatus')}</h3>
      {!frrRunning(status) && (
        <p role="status" className="mb-2 rounded bg-yellow-50 p-2 text-xs text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-200">
          {t('routing.frrStarting')}
        </p>
      )}
      {status.ospf.length === 0 && <p className="text-xs text-gray-500">{t('routing.noOSPFNeighbors')}</p>}
      {status.ospf.length > 0 && (
        <table className="min-w-full text-sm" aria-label={t('routing.ospfStatusTable')}>
          <thead className="text-left text-xs uppercase text-gray-500">
            <tr>
              <th className="px-2 py-1">{t('routing.col.routerID')}</th>
              <th className="px-2 py-1">{t('routing.col.address')}</th>
              <th className="px-2 py-1">{t('routing.col.interface')}</th>
              <th className="px-2 py-1">{t('routing.col.state')}</th>
            </tr>
          </thead>
          <tbody>
            {status.ospf.map((n: OSPFNeighborStatus, i: number) => (
              <tr key={`${n.router_id}-${i}`} className="border-t border-gray-100 dark:border-gray-700">
                <td className="px-2 py-1 font-mono">{n.router_id}</td>
                <td className="px-2 py-1 font-mono">{n.address}</td>
                <td className="px-2 py-1 font-mono">{n.interface}</td>
                <td className="px-2 py-1">
                  <span className={cls(ospfBadge(n))}>{n.state}</span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}

// Routes is the table of what FRR learned, with a search box over prefix,
// protocol and next hop.
function Routes({ routes }: { routes: LearnedRoute[] }) {
  const [term, setTerm] = useState('')
  const q = term.trim().toLowerCase()
  const shown = sortRoutes(routes).filter(
    (r) =>
      q === '' ||
      r.prefix.toLowerCase().includes(q) ||
      r.protocol.toLowerCase().includes(q) ||
      (r.interface ?? '').toLowerCase().includes(q) ||
      r.next_hops.some((h) => h.toLowerCase().includes(q)),
  )
  return (
    <section className="mb-6">
      <div className="mb-2 flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-lg font-semibold text-gray-800 dark:text-white">{t('routing.routes')}</h2>
        <input
          type="search"
          aria-label={t('routing.searchRoutes')}
          placeholder={t('routing.searchRoutes')}
          className={inputClass + ' w-64'}
          value={term}
          onChange={(e) => setTerm(e.target.value)}
        />
      </div>
      {shown.length === 0 ? (
        <p className="text-sm text-gray-500">{routes.length === 0 ? t('routing.noRoutes') : t('routing.noRoutesMatch')}</p>
      ) : (
        <table className="min-w-full text-sm dark:text-gray-200" aria-label={t('routing.routes')}>
          <thead className="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-gray-700">
            <tr>
              <th className="px-2 py-1">{t('routing.col.prefix')}</th>
              <th className="px-2 py-1">{t('routing.col.protocol')}</th>
              <th className="px-2 py-1">{t('routing.col.via')}</th>
              <th className="px-2 py-1">{t('routing.col.interface')}</th>
              <th className="px-2 py-1">{t('routing.col.distance')}</th>
              <th className="px-2 py-1">{t('routing.col.metric')}</th>
              <th className="px-2 py-1">{t('routing.col.ROUTE')}</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((r) => (
              <tr key={`${r.protocol}-${r.prefix}-${r.next_hops.join(',')}`} className="border-t border-gray-100 dark:border-gray-700">
                <td className="px-2 py-1 font-mono">{r.prefix}</td>
                <td className="px-2 py-1">{r.protocol.toUpperCase()}</td>
                <td className="px-2 py-1 font-mono">{r.next_hops.join(', ')}</td>
                <td className="px-2 py-1 font-mono">{r.interface ?? ''}</td>
                <td className="px-2 py-1">{r.distance}</td>
                <td className="px-2 py-1">{r.metric}</td>
                <td className="px-2 py-1">
                  <span aria-label={t('routing.routeFor', { prefix: r.prefix })}>
                    {r.selected ? <span className={cls('ok')}>{t('routing.selected')}</span> : <span className={cls('warn')}>{t('routing.notSelected')}</span>}{' '}
                    {r.installed && <span className={cls('ok')}>{t('routing.installed')}</span>}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  )
}

export default function Routing() {
  const perms = usePermissions()
  const canEdit = perms.can('write:network')
  const [cfg, setCfg] = useState<Candidate | null>(null)
  const [form, setFormState] = useState<RoutingForm>(formOf(undefined))
  const [loadError, setLoadError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    getCandidate()
      .then((c) => {
        setCfg(c)
        setFormState(formOf(c.routing))
      })
      .catch((e: Error) => setLoadError(e.message))
  }, [])

  const setForm = (patch: Partial<RoutingForm>) => {
    setSaved(false)
    setFormState((f) => ({ ...f, ...patch }))
  }
  // onSave pushes the saved form back into the page state so the other card
  // keeps the fields this card does not edit when it saves later.
  const onSave = (next: RoutingForm) => {
    setSaved(true)
    setFormState(next)
  }

  const status = useQuery({ queryKey: ['routing-status'], queryFn: getRoutingStatus, refetchInterval: 10_000, retry: false })

  return (
    <div>
      <h2 className="mb-1 text-xl font-semibold text-gray-800 dark:text-white">{t('routing.title')}</h2>
      <p className="mb-4 text-sm text-gray-500">
        {t('routing.intro')}{' '}
        <a className="text-blue-600 underline dark:text-blue-400" href={routingWikiURL} target="_blank" rel="noreferrer">
          {t('routing.wikiLink')}
        </a>
        .
      </p>
      {loadError && (
        <div role="alert" className="mb-3 rounded bg-red-50 p-2 text-sm text-red-700 dark:bg-red-900/30 dark:text-red-200">
          {loadError}
        </div>
      )}
      {!canEdit && <p className="mb-3 text-sm text-gray-500 dark:text-gray-400">{t('permissions.readOnlyList')}</p>}
      {status.isError && (
        <p className="mb-3 text-sm text-gray-500" role="status">
          {t('routing.statusUnavailable')}
        </p>
      )}
      <BGPSection form={form} setForm={setForm} cfg={cfg} status={status.data} canEdit={canEdit} saved={saved} onSave={onSave} />
      <OSPFSection form={form} setForm={setForm} cfg={cfg} status={status.data} canEdit={canEdit} saved={saved} onSave={onSave} />
      {status.data && <Routes routes={status.data.routes} />}
    </div>
  )
}
