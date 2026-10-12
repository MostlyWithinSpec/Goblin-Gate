// Web filter profiles (config "profiles.webfilter", task 033/034): an action
// per category of the category database, plus own block/allow lists. Enforced
// at DNS by goblind's web filter proxy for the interfaces whose policies use
// the profile; blocked and warned names get the block page.
import React, { useEffect, useState } from 'react'
import { ConfigList, ListInput, inputClass, omitEmpty } from '../../components/ConfigList'
import { getWebCategories, lookupWebCategory, profileInterfaces, type WebAction, type WebFilterProfile, type WebLookup } from '../../api/objects'
import { Badge } from '../../components/Badge'
import { UsedBy } from './DNSFilterProfiles'
import { summarize, validateDomainLists } from './profileLogic'

// Shown before the category database has been downloaded (UT1 names).
export const DEFAULT_CATEGORIES = ['adult', 'gambling', 'malware', 'phishing', 'social_networks', 'games', 'audio-video', 'dating', 'drugs', 'vpn', 'warez']
const ACTIONS: WebAction[] = ['allow', 'monitor', 'warn', 'block']
const variant = { allow: 'success', monitor: 'primary', warn: 'warning', block: 'error' } as const

// setCategory sets one category's action; allow is the default and is not stored.
export function setCategory(p: WebFilterProfile, cat: string, action: WebAction): WebFilterProfile {
  const categories = { ...(p.categories ?? {}) }
  if (action === 'allow') delete categories[cat]
  else categories[cat] = action
  return { ...p, categories }
}

function Lookup() {
  const [q, setQ] = useState('')
  const [res, setRes] = useState<WebLookup | null>(null)
  const [err, setErr] = useState<string | null>(null)
  const go = (e: React.FormEvent) => {
    e.preventDefault()
    if (!q.trim()) return
    setErr(null)
    lookupWebCategory(q.trim().replace(/^https?:\/\//, '').split('/')[0]).then(setRes).catch((x: Error) => setErr(x.message))
  }
  return (
    <form onSubmit={go} className="mt-6 rounded bg-white p-4 text-sm shadow-sm dark:bg-gray-800 dark:text-gray-200">
      <h3 className="mb-2 font-semibold">Look up a domain</h3>
      <div className="flex gap-2">
        <input aria-label="Domain lookup" className={inputClass + ' mt-0'} placeholder="example.com" value={q} onChange={(e) => setQ(e.target.value)} />
        <button type="submit" className="rounded bg-blue-600 px-3 py-1 text-white">Look up</button>
      </div>
      {err && <p role="alert" className="mt-2 text-red-600">{err}</p>}
      {res && (
        <div className="mt-2" data-testid="lookup-result">
          <p>
            <code>{res.domain}</code>: {res.categories.length ? res.categories.join(', ') : 'no category'}
          </p>
          <ul className="mt-1">
            {Object.entries(res.profiles).map(([name, d]) => (
              <li key={name}>
                {name}: <Badge variant={variant[d.action]}>{d.action}</Badge>
                {d.category && <span className="ml-1 text-gray-500">({d.category}{d.match && d.match !== res.domain ? ` via ${d.match}` : ''})</span>}
              </li>
            ))}
          </ul>
          <p className="mt-1 text-xs text-gray-500">Profiles as committed (running config).</p>
        </div>
      )}
    </form>
  )
}

export default function WebFilterProfiles() {
  const [cats, setCats] = useState<{ categories: string[]; entries: number } | null>(null)
  useEffect(() => {
    getWebCategories().then(setCats).catch(() => setCats({ categories: [], entries: 0 }))
  }, [])
  const known = cats && cats.categories.length > 0 ? cats.categories : DEFAULT_CATEGORIES

  return (
    <div className="p-6">
      <ConfigList<WebFilterProfile>
        title="Web Filter Profiles"
        path="profiles.webfilter"
        newLabel="New web filter profile"
        intro={
          <>
            Categories come from the category database
            {cats && (cats.entries > 0 ? ` (${cats.entries.toLocaleString()} domains)` : ' (not downloaded yet: showing common categories)')}. Filtering works on DNS names, so a
            profile applies to the source interfaces of the first policy that selects it. <em>Warn</em> shows the block page with a "continue" button.
          </>
        }
        select={(c) => c.profiles?.webfilter}
        empty={() => ({ name: '', categories: { adult: 'block', malware: 'block', phishing: 'block' } })}
        clean={(p) => omitEmpty({ ...p, comment: p.comment?.trim() })}
        validate={(p) => validateDomainLists(p)}
        columns={[
          { label: 'Categories', render: (p) => summarize(p.categories) || 'allow all' },
          { label: 'Own lists', render: (p) => `${(p.block_domains ?? []).length} blocked, ${(p.allow_domains ?? []).length} allowed` },
          { label: 'Comment', render: (p) => p.comment },
        ]}
        after={(cfg, items) => (
          <>
            <UsedBy used={profileInterfaces(cfg, 'webfilter_profile', items.map((p) => p.name))} />
            <Lookup />
          </>
        )}
        form={(p, set, { err }) => {
          const all = [...new Set([...known, ...Object.keys(p.categories ?? {})])].sort()
          return (
            <>
              <label className="block">
                Comment
                <input aria-label="Comment" className={inputClass} value={p.comment ?? ''} onChange={(e) => set({ comment: e.target.value })} />
              </label>
              <fieldset>
                <legend>Categories</legend>
                <div className="mt-1 max-h-72 overflow-y-auto rounded border border-gray-200 dark:border-gray-600">
                  <table className="w-full text-xs">
                    <tbody>
                      {all.map((c) => (
                        <tr key={c} className="border-t border-gray-100 first:border-0 dark:border-gray-700">
                          <td className="px-2 py-1">{c}</td>
                          <td className="px-2 py-1 text-right">
                            <select
                              aria-label={`Action for ${c}`}
                              className="rounded border border-gray-300 px-1 dark:bg-gray-700"
                              value={p.categories?.[c] ?? 'allow'}
                              onChange={(e) => set({ categories: setCategory(p, c, e.target.value as WebAction).categories })}
                            >
                              {ACTIONS.map((a) => <option key={a} value={a}>{a}</option>)}
                            </select>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </fieldset>
              <ListInput label="Always block" placeholder="example.com" value={p.block_domains} onChange={(block_domains) => set({ block_domains })} />
              {err('block_domains')}
              <ListInput label="Always allow" placeholder="school.example.org" value={p.allow_domains} onChange={(allow_domains) => set({ allow_domains })} />
              {err('allow_domains')}
            </>
          )
        }}
      />
    </div>
  )
}
