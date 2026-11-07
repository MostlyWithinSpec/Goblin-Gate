// Application control profiles (config "profiles.appctrl", task 035/044):
// allow, monitor or block apps of the catalog per group with per-app
// overrides. Apps are recognized by their DNS names.
import { useEffect, useState } from 'react'
import { ConfigList, inputClass, omitEmpty } from '../../components/ConfigList'
import { Badge } from '../../components/Badge'
import { getApps, profileInterfaces, type Action3, type App, type AppCtrlProfile } from '../../api/objects'
import { listLogs, type LogEvent } from '../../api/stats'
import { UsedBy } from './DNSFilterProfiles'
import { appAction, groupApps, setGroup, setOverride, summarize } from './profileLogic'
import { t } from '../../i18n'

const ACTIONS: Action3[] = ['allow', 'monitor', 'block']
const variant = { allow: 'success', monitor: 'primary', block: 'error' } as const

export function AppEvents({ apps }: { apps: App[] }) {
  const [events, setEvents] = useState<LogEvent[] | null>(null)
  useEffect(() => {
    listLogs({ kind: 'appctrl', limit: 50 })
      .then((p) => setEvents(p.events ?? []))
      .catch(() => setEvents([]))
  }, [])
  const name = (id?: string) => apps.find((a) => a.id === id)?.name ?? id
  return (
    <div className="mt-6 rounded bg-white p-4 text-sm shadow-sm dark:bg-gray-800 dark:text-gray-200">
      <h3 className="mb-2 font-semibold">{t('appControl.recentEvents')}</h3>
      {events === null ? (
        <p className="text-gray-500">{t('common.loading')}</p>
      ) : events.length === 0 ? (
        <p className="text-gray-500">{t('appControl.noEvents')}</p>
      ) : (
        <table className="min-w-full text-xs" data-testid="app-events">
          <thead className="text-left uppercase text-gray-500">
            <tr><th className="py-1">{t('appControl.colTime')}</th><th>{t('appControl.colClient')}</th><th>{t('appControl.colApp')}</th><th>{t('appControl.colGroup')}</th><th>{t('appControl.colDomain')}</th><th>{t('appControl.colAction')}</th></tr>
          </thead>
          <tbody>
            {events.map((e) => (
              <tr key={e.id} className="border-t border-gray-100 dark:border-gray-700">
                <td className="py-1">{new Date(e.time * 1000).toLocaleString()}</td>
                <td>{e.src_ip}</td>
                <td>{name(e.app)}</td>
                <td>{e.category}</td>
                <td><code>{e.message}</code></td>
                <td><Badge variant={e.action === 'deny' ? 'error' : 'success'}>{e.action === 'deny' ? t('appControl.blocked') : e.action}</Badge></td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  )
}

export default function AppControl() {
  const [apps, setApps] = useState<App[]>([])
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    getApps().then(setApps).catch((e: Error) => setError(e.message))
  }, [])
  const grouped = groupApps(apps)

  return (
    <div className="p-6">
      {error && <div role="alert" className="mb-3 rounded bg-red-50 p-2 text-sm text-red-700">{t('appControl.catalogError', { error })}</div>}
      <ConfigList<AppCtrlProfile>
        title={t('appControl.title')}
        path="profiles.appctrl"
        newLabel={t('appControl.newProfile')}
        intro={t('appControl.intro')}
        select={(c) => c.profiles?.appctrl}
        empty={() => ({ name: '', groups: {}, apps: {} })}
        clean={(p) => omitEmpty({ ...p, comment: p.comment?.trim() })}
        columns={[
          { label: t('appControl.colGroups'), render: (p) => summarize(p.groups) || t('appControl.allowAll') },
          { label: t('appControl.colOverrides'), render: (p) => summarize(p.apps) || Object.keys(p.apps ?? {}).length || '' },
          { label: t('appControl.colComment'), render: (p) => p.comment },
        ]}
        after={(cfg, items) => (
          <>
            <UsedBy used={profileInterfaces(cfg, 'appctrl_profile', items.map((p) => p.name))} names={items.map((p) => p.name)} />
            <AppEvents apps={apps} />
          </>
        )}
        form={(p, set) => (
          <>
            <label className="block">
              {t('appControl.comment')}
              <input aria-label="Comment" className={inputClass} value={p.comment ?? ''} onChange={(e) => set({ comment: e.target.value })} />
            </label>
            {grouped.map(([group, list]) => (
              <fieldset key={group} className="rounded border border-gray-200 p-2 dark:border-gray-600">
                <legend className="flex w-full items-center justify-between px-1 font-medium">
                  {group}
                  <select
                    aria-label={`Action for group ${group}`}
                    className="ml-2 rounded border border-gray-300 px-1 text-xs dark:bg-gray-700"
                    value={p.groups?.[group] ?? 'allow'}
                    onChange={(e) => set({ groups: setGroup(p, group, e.target.value as Action3).groups })}
                  >
                    {ACTIONS.map((a) => <option key={a} value={a}>{a}</option>)}
                  </select>
                </legend>
                <table className="w-full text-xs">
                  <tbody>
                    {list.map((app) => (
                      <tr key={app.id}>
                        <td className="py-0.5" title={app.domains.join(', ')}>{app.name}</td>
                        <td className="text-right">
                          <Badge variant={variant[appAction(p, app)]}>{appAction(p, app)}</Badge>
                          <select
                            aria-label={`Override for ${app.name}`}
                            className="ml-2 rounded border border-gray-300 px-1 dark:bg-gray-700"
                            value={p.apps?.[app.id] ?? ''}
                            onChange={(e) => set({ apps: setOverride(p, app.id, e.target.value as Action3 | '').apps })}
                          >
                            <option value="">{t('appControl.asGroup')}</option>
                            {ACTIONS.map((a) => <option key={a} value={a}>{a}</option>)}
                          </select>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </fieldset>
            ))}
          </>
        )}
      />
    </div>
  )
}
