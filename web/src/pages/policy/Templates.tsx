// Policy & Objects → Templates (task 111): pick a template, answer its
// questions, look at the change, apply it. A template writes into the
// candidate only — committing stays in the commit bar at the top, which is
// why applying refreshes its change count and points there.
// Docs: https://github.com/MostlyWithinSpec/Goblin-Gate/wiki/How-Changes-Work#templates
import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, Eye, Wand2 } from 'lucide-react'
import { Button } from '../../components/Button'
import { EditDrawer } from '../../components/EditDrawer'
import { DiffViewer } from '../../components/DiffViewer'
import { inputClass } from '../../components/ConfigList'
import { t } from '../../i18n'
import { APIRequestError } from '../../api/client'
import {
  applyTemplate,
  getTemplates,
  previewTemplate,
  type TemplateInfo,
  type TemplateInput,
  type TemplateResult
} from '../../api/templates'

const WIKI_URL = 'https://github.com/MostlyWithinSpec/Goblin-Gate/wiki/How-Changes-Work#templates'

const DAYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun']

// dayLabel puts a day code through the catalog. A constant whose value is a
// t() call at module load would freeze English, so the calls live in here.
function dayLabel(day: string): string {
  switch (day) {
    case 'mon':
      return t('templates.dayMon')
    case 'tue':
      return t('templates.dayTue')
    case 'wed':
      return t('templates.dayWed')
    case 'thu':
      return t('templates.dayThu')
    case 'fri':
      return t('templates.dayFri')
    case 'sat':
      return t('templates.daySat')
    default:
      return t('templates.daySun')
  }
}

// answersFrom prefills a form with each input's default.
export function answersFrom(inputs: TemplateInput[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const i of inputs) out[i.name] = i.default ?? ''
  return out
}

function csv(s: string): string[] {
  return s.split(',').map((x) => x.trim()).filter(Boolean)
}

// A "days" answer is a comma separated list of day codes. Keeping the list in
// the order mon…sun is what makes an edited answer comparable with the answer
// the server got with the preview.
const parseDays = (s: string): string[] => {
  const on = new Set(
    s
      .split(',')
      .map((x) => x.trim().toLowerCase())
      .filter(Boolean)
  )
  return DAYS.filter((d) => on.has(d))
}

const fmtDays = (list: string[]): string => list.join(',')

const isTrue = (v: string): boolean => v === 'true'

// Field renders one answer. Every control keeps its answer as the exact text
// the server expects, so what is sent is what the field shows.
function Field({
  input,
  value,
  error,
  onChange
}: {
  input: TemplateInput
  value: string
  error?: string
  onChange: (v: string) => void
}) {
  const id = `tpl-${input.name}`
  const numberLike = input.type === 'vlan_id' || input.type === 'port'
  const isSelect = input.type === 'interface' || input.type === 'tunnel' || input.type === 'profile'
  const isList = input.type === 'ips' || input.type === 'sources'

  // help and the problem of this answer are wired to the control with
  // aria-describedby, so the control's accessible name stays exactly the label —
  // and the error still sits in the field's own div. Nothing but the label's own
  // text ever goes inside the <label>: this testing-library matches a <label for>
  // by its full text content (only a bare asterisk or plus is trimmed), so a "*"
  // or a nested element in the label breaks every getByLabelText for the field.
  // A required answer is marked with a * next to the label instead.
  const describedBy =
    [input.help ? `${id}-help` : null, error ? `${id}-error` : null].filter(Boolean).join(' ') || undefined
  const controlProps = {
    id,
    className: inputClass,
    'aria-describedby': describedBy,
  }
  const label = (
    <label className="text-sm font-medium" htmlFor={id}>
      {input.label}
    </label>
  )
  const star = input.required && (
    <span className="text-red-600" title={t('templates.required')}>
      *
    </span>
  )
  const help = input.help && (
    <p id={`${id}-help`} className="mt-1 text-xs text-gray-500 dark:text-gray-400">
      {input.help}
    </p>
  )
  const errorLine = error && (
    <p id={`${id}-error`} role="alert" className="mt-1 text-xs text-red-600 dark:text-red-400">
      {error}
    </p>
  )

  if (input.type === 'bool') {
    return (
      <div>
        <input
          {...controlProps}
          type="checkbox"
          checked={isTrue(value)}
          onChange={(e) => onChange(e.target.checked ? 'true' : 'false')}
        />
        {/* the label is a sibling of the box: wrapped in the <label> its whole text
            would become the box's accessible name and the plain label query would
            not match */}
        {label}
        {star}
        {help}
        {errorLine}
      </div>
    )
  }

  if (input.type === 'days') {
    const on = parseDays(value)
    const toggle = (d: string) => onChange(fmtDays(on.includes(d) ? on.filter((x) => x !== d) : parseDays(on.concat(d).join(','))))
    return (
      <div>
        <span className="text-sm font-medium">{input.label}</span>
        {input.required && <span className="text-red-600">*</span>}
        <div className="mt-1 flex flex-wrap gap-1">
          {DAYS.map((d) => (
            <label
              key={d}
              className={`cursor-pointer rounded border px-2 py-0.5 text-xs ${
                on.includes(d)
                  ? 'border-blue-600 bg-blue-600 text-white'
                  : 'border-gray-300 text-gray-700 dark:border-gray-600 dark:text-gray-300'
              }`}
            >
              <input type="checkbox" className="sr-only" checked={on.includes(d)} onChange={() => toggle(d)} />
              {dayLabel(d)}
            </label>
          ))}
        </div>
        {help}
        {errorLine}
      </div>
    )
  }

  return (
    <div>
      <div className="flex items-center gap-1">
        {label}
        {star}
      </div>
      {isList && (
        <ul className="mt-1 flex flex-wrap gap-1">
          {csv(value).map((v, i) => (
            <li key={`${v}-${i}`} className="rounded bg-gray-100 px-2 py-0.5 font-mono text-xs dark:bg-gray-700">
              {v}
            </li>
          ))}
        </ul>
      )}
      {isSelect ? (
        <select
          {...controlProps}
          value={value}
          onChange={(e) => onChange(e.target.value)}
        >
          {input.type === 'profile' && <option value="">{t('templates.none')}</option>}
          {(input.options ?? []).map((o) => (
            <option key={o} value={o}>
              {o}
            </option>
          ))}
        </select>
      ) : (
        <input
          {...controlProps}
          type={numberLike ? 'number' : input.type === 'time' ? 'time' : 'text'}
          min={numberLike ? 1 : undefined}
          max={input.type === 'vlan_id' ? 4094 : input.type === 'port' ? 65535 : undefined}
          value={value}
          placeholder={isList ? (input.type === 'sources' ? 'US, 203.0.113.0/24' : '192.168.1.20, 192.168.1.31') : undefined}
          onChange={(e) => onChange(e.target.value)}
        />
      )}
      {help}
      {errorLine}
    </div>
  )
}

// Wizard is the drawer: the questions, a preview of the change, and Apply.
function Wizard({ template, onClose }: { template: TemplateInfo; onClose: () => void }) {
  const queryClient = useQueryClient()
  const [values, setValues] = useState<Record<string, string>>(() => answersFrom(template.inputs))
  // previewed holds the answers the preview below was made with, so an edit can
  // drop a diff that no longer describes the change.
  const [preview, setPreview] = useState<{ answers: Record<string, string>; result: TemplateResult } | null>(null)
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<string | null>(null)
  const [applied, setApplied] = useState<{ answers: Record<string, string>; changes: number } | null>(null)

  // A new answer invalidates a preview: what it showed is no longer the change.
  const setValue = (name: string, v: string) => {
    setValues((prev) => ({ ...prev, [name]: v }))
    setPreview(null)
    setApplied(null)
    setFailure(null)
  }

  // The answers of the last preview or apply. A field keeps its problem only
  // while its own answer is still the one that caused it.
  const answered = applied ? applied.answers : preview?.answers ?? null
  const result = applied ? null : preview?.result ?? null
  const badFields = new Map<string, string>()
  for (const e of result?.input_errors ?? []) {
    if (e.input && answered?.[e.input] !== undefined && answered[e.input] === values[e.input]) {
      badFields.set(e.input, e.message)
    }
  }
  // A problem with no answer of its own, and every validation problem, go above
  // the questions — they stay until an answer makes the preview stale.
  const general: string[] = [
    ...(result?.input_errors ?? []).filter((e) => !e.input).map((e) => e.message),
    ...(result?.validation_errors ?? []).map((e) => `${e.path}: ${e.message}`),
  ]

  const run = async (what: 'preview' | 'apply') => {
    const answers = { ...values }
    setBusy(true)
    setFailure(null)
    try {
      if (what === 'preview') {
        setPreview({ answers, result: await previewTemplate(template.id, answers) })
      } else {
        const res = await applyTemplate(template.id, answers)
        setPreview(null)
        setApplied({ answers, changes: res.changes?.length ?? 0 })
        // The candidate changed: the commit bar reads its count from configDiff.
        queryClient.invalidateQueries({ queryKey: ['configDiff'] })
        queryClient.invalidateQueries({ queryKey: ['candidate'] })
      }
    } catch (e) {
      setPreview(null)
      setApplied(null)
      const details = e instanceof APIRequestError ? e.details : undefined
      if (details && typeof details === 'object' && 'input_errors' in details) {
        const r = details as TemplateResult
        // Keep the problems, with no diff: the answers still need fixing.
        setPreview({ answers, result: { changes: [], input_errors: r.input_errors ?? [], validation_errors: r.validation_errors ?? [] } })
        setFailure(t('templates.answersNeedFixing'))
      } else {
        const detail = e instanceof Error ? e.message : what
        setFailure(t('templates.stepFailed', { what: detail }))
      }
    } finally {
      setBusy(false)
    }
  }

  const changeCount = result?.changes.length ?? 0

  return (
    <EditDrawer isOpen onClose={onClose} title={template.title} wide>
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault()
          void run('apply')
        }}
      >
        <p className="text-sm text-gray-600 dark:text-gray-300">{template.description}</p>

        {general.length > 0 && (
          <div role="alert" className="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-800 dark:border-red-900 dark:bg-red-900/20 dark:text-red-200">
            <ul className="list-disc space-y-0.5 pl-4">
              {general.map((m, i) => (
                <li key={i}>{m}</li>
              ))}
            </ul>
          </div>
        )}
        {failure && (
          <p role="alert" className="text-sm text-red-600 dark:text-red-400">
            {failure}
          </p>
        )}

        <div className="space-y-3">
          {template.inputs.map((input) => (
            <Field
              key={input.name}
              input={input}
              value={values[input.name] ?? ''}
              error={badFields.get(input.name)}
              onChange={(v) => setValue(input.name, v)}
            />
          ))}
        </div>

        <div className="flex flex-wrap items-center gap-2 border-t border-gray-200 pt-4 dark:border-gray-700">
          <Button variant="outline" onClick={() => void run('preview')} disabled={busy}>
            <Eye size={14} /> {t('templates.preview')}
          </Button>
          <Button variant="primary" type="submit" disabled={busy}>
            <Wand2 size={14} /> {t('templates.apply')}
          </Button>
          {busy && <span className="text-sm text-gray-500">{t('templates.working')}</span>}
        </div>

        {applied ? (
          <div className="rounded-lg border border-green-200 bg-green-50 p-3 text-sm text-green-800 dark:border-green-900 dark:bg-green-900/20 dark:text-green-200">
            <p>
              {t('templates.addedLead')} {t('templates.addedChanges', { count: applied.changes })} {t('templates.addedMid')}
              {t('templates.addedRest')}
            </p>
            <Button variant="secondary" size="sm" className="mt-2" onClick={onClose}>
              {t('common.close')}
            </Button>
          </div>
        ) : result ? (
          <div className="space-y-2">
            <h3 className="text-sm font-semibold">
              {changeCount === 0 ? t('templates.changesNothing') : t('templates.changesThings', { count: changeCount })}
            </h3>
            {changeCount === 0 ? (
              <p className="text-sm text-gray-500">{t('templates.nothingToApply')}</p>
            ) : (
              <DiffViewer changes={result.changes.map((c) => ({ path: c.path, old: c.old, new: c.new }))} />
            )}
          </div>
        ) : null}
      </form>
    </EditDrawer>
  )
}

// Card is one template: what it does and the button that opens the wizard, or
// the reason it cannot be used with this config yet.
function Card({ template, onOpen }: { template: TemplateInfo; onOpen: () => void }) {
  const out = !!template.unavailable
  return (
    <div className="flex flex-col rounded-lg border border-gray-200 p-4 dark:border-gray-700">
      <h2 className="text-sm font-semibold text-gray-900 dark:text-white">{template.title}</h2>
      <p className="mt-1 flex-1 text-sm text-gray-600 dark:text-gray-300">{template.description}</p>
      {out ? (
        <p className="mt-3 flex items-start gap-1 text-xs text-amber-700 dark:text-amber-400">
          <AlertTriangle size={14} className="mt-0.5 shrink-0" />
          {template.unavailable}
        </p>
      ) : (
        <Button variant="primary" size="sm" className="mt-3 self-start" onClick={onOpen}>
          {t('templates.setUp')}
        </Button>
      )}
    </div>
  )
}

export default function Templates() {
  // TError is named because react-query defaults it to `unknown`, which cannot be
  // rendered without a cast; getTemplates rejects with the API's Error.
  const q = useQuery<TemplateInfo[], Error>({ queryKey: ['templates'], queryFn: getTemplates })
  const [open, setOpen] = useState<string | null>(null)
  const [search, setSearch] = useState('')

  const needle = search.trim().toLowerCase()
  const list = (q.data ?? []).filter(
    (tpl) => !needle || tpl.title.toLowerCase().includes(needle) || tpl.description.toLowerCase().includes(needle)
  )
  // The catalog can change under us (a commit removing a WAN); close a drawer
  // whose template is gone or no longer usable.
  const current = (q.data ?? []).find((tpl) => tpl.id === open && !tpl.unavailable) ?? null

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-gray-800 dark:text-white">{t('templates.title')}</h1>
        <p className="text-sm text-gray-500 dark:text-gray-400">
          {t('templates.introLead')}{' '}
          {t('templates.introTail')}{' '}
          <a className="text-blue-600 hover:underline" href={WIKI_URL} target="_blank" rel="noreferrer">
            {t('templates.docs')}
          </a>
        </p>
      </div>

      <div className="flex items-center gap-3">
        <input
          className={`${inputClass} max-w-xs`}
          placeholder={t('templates.search')}
          aria-label={t('templates.search')}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <span className="text-sm text-gray-500">{t('templates.count', { count: list.length })}</span>
      </div>

      {q.isError && (
        <p role="alert" className="text-sm text-red-600 dark:text-red-400">
          {t('templates.couldNotLoad', { why: q.error.message })}
        </p>
      )}
      {!q.isLoading && list.length === 0 && !q.isError && (
        <p className="text-sm text-gray-500">{t('templates.noMatch', { search })}</p>
      )}

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
        {list.map((tpl) => (
          <Card key={tpl.id} template={tpl} onOpen={() => setOpen(tpl.id)} />
        ))}
      </div>

      {current && <Wizard key={current.id} template={current} onClose={() => setOpen(null)} />}
    </div>
  )
}
