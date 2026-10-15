// Notifications (task 053/054): channels and per-event routing. Channels are
// edited with ConfigList on "notifications.channels"; the rules table below it
// picks, for each event, which channels receive it (PATCH notifications.rules).
// A "Send test" button sends a test message to one channel of the running config.
import React, { useState } from 'react'
import { Send } from 'lucide-react'
import { ConfigList, inputClass, omitEmpty, type Errors } from '../../components/ConfigList'
import { Badge } from '../../components/Badge'
import {
  getCandidate,
  patchSection,
  testNotification,
  NOTIFY_EVENTS,
  type Candidate,
  type NotifyChannel,
  type NotifyRule,
} from '../../api/objects'

// validateChannel mirrors internal/config (notify.go checkNotifications) for
// the per-type fields; ConfigList checks the name itself.
export function validateChannel(c: NotifyChannel): Errors {
  const e: Errors = {}
  const url = (v?: string) => !v || (!v.startsWith('http://') && !v.startsWith('https://'))
  switch (c.type) {
    case 'webhook':
    case 'discord':
    case 'ntfy':
      if (url(c.url)) e.url = 'Enter an http:// or https:// URL'
      break
    case 'telegram':
      if (!c.bot_token || /[/ ?#]/.test(c.bot_token)) e.bot_token = 'A Telegram bot token is required'
      if (!c.chat_id) e.chat_id = 'A Telegram chat id is required'
      break
    case 'email':
      if (!c.smtp_host) e.smtp_host = 'An SMTP server is required'
      if (c.smtp_port === undefined || c.smtp_port < 0 || c.smtp_port > 65535) e.smtp_port = 'Port must be 0-65535'
      if (!c.from || !/^[^@\s]+@[^@\s]+$/.test(c.from)) e.from = 'Enter a valid sender address'
      if (!c.to || c.to.length === 0) e.to = 'At least one recipient is required'
      break
  }
  return e
}

const cleanChannel = (c: NotifyChannel): NotifyChannel =>
  omitEmpty({ ...c, smtp_port: c.smtp_port || undefined }) as NotifyChannel

// RulesTable: one row per event with a checkbox per channel. Saves the whole
// list of rules (only events that have at least one channel) to the candidate.
function RulesTable({ channels }: { channels: NotifyChannel[] }) {
  const [rules, setRules] = useState<NotifyRule[]>([])
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<string | null>(null)

  React.useEffect(() => {
    getCandidate()
      .then((c) => {
        setRules(c.notifications?.rules ?? [])
        setLoaded(true)
      })
      .catch((e: Error) => setError(e.message))
  }, [])

  const toggle = (event: string, channel: string, on: boolean) => {
    const rest = rules.filter((r) => r.event !== event)
    const current = rules.find((r) => r.event === event)?.channels ?? []
    const next = on ? [...current, channel] : current.filter((x) => x !== channel)
    save(next.length ? [...rest, { event, channels: next }] : rest)
  }

  const save = (next: NotifyRule[]) => {
    setRules(next)
    setError(null)
    patchSection('notifications.rules', next).catch((e: Error) => setError(e.message))
  }

  if (!loaded && !error) return <p className="text-sm text-gray-500">Loading…</p>
  if (error) return <div role="alert" className="rounded bg-red-50 p-2 text-sm text-red-700">{error}</div>

  return (
    <section className="mt-8">
      <h3 className="mb-1 text-lg font-semibold text-gray-800 dark:text-white">Per-event routing</h3>
      <p className="mb-3 text-sm text-gray-600 dark:text-gray-400">
        Pick which channels receive each event. Changes are saved to the candidate; commit to apply.
      </p>
      {channels.length === 0 ? (
        <p className="text-sm text-gray-500">Add a channel above first.</p>
      ) : (
        <div className="overflow-x-auto rounded bg-white shadow-sm dark:bg-gray-800">
          <table className="min-w-full text-sm">
            <thead className="bg-gray-50 text-left text-xs uppercase text-gray-500 dark:bg-gray-700">
              <tr>
                <th className="px-3 py-2">Event</th>
                {channels.map((c) => (
                  <th key={c.name} className="px-3 py-2 text-center">{c.name}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {NOTIFY_EVENTS.map((ev) => {
                const sel = rules.find((r) => r.event === ev)?.channels ?? []
                return (
                  <tr key={ev} className="border-t border-gray-100 dark:border-gray-700">
                    <td className="px-3 py-1 font-mono text-xs">{ev}</td>
                    {channels.map((c) => (
                      <td key={c.name} className="px-3 py-1 text-center">
                        <input
                          type="checkbox"
                          aria-label={`${ev} → ${c.name}`}
                          checked={sel.includes(c.name)}
                          onChange={(e) => toggle(ev, c.name, e.target.checked)}
                        />
                      </td>
                    ))}
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

// TestButton: sends a test message to one channel of the running config and
// shows the result inline (sent, or the server's reason).
function TestButton({ name }: { name: string }) {
  const [state, setState] = useState<{ busy: boolean; msg?: string; ok?: boolean }>({ busy: false })
  const send = () => {
    setState({ busy: true })
    testNotification(name)
      .then(() => setState({ busy: false, msg: 'sent', ok: true }))
      .catch((e: Error) => setState({ busy: false, msg: e.message, ok: false }))
  }
  return (
    <span className="inline-flex items-center gap-1">
      <button type="button" aria-label={`Send test to ${name}`} title="Send a test message" disabled={state.busy} onClick={send} className="text-blue-600 disabled:opacity-50">
        <Send size={16} />
      </button>
      {state.msg && (
        <span role={state.ok ? 'status' : 'alert'} data-testid={`test-${name}`} className={state.ok ? 'text-xs text-green-600' : 'text-xs text-red-600'}>
          {state.msg}
        </span>
      )}
    </span>
  )
}

export default function Notifications() {
  const [channels, setChannels] = useState<NotifyChannel[]>([])
  React.useEffect(() => {
    getCandidate().then((c) => setChannels(c.notifications?.channels ?? [])).catch(() => {})
  }, [])

  return (
    <div className="p-6">
      <ConfigList<NotifyChannel>
        title="Notification channels"
        path="notifications.channels"
        newLabel="New channel"
        intro="Channels are the destinations for alerts (email, webhook, Discord, ntfy or Telegram). Secrets are stored in the config and never logged. Status shows the running config (commit to apply changes)."
        select={(c: Candidate) => c.notifications?.channels}
        empty={() => ({ name: '', type: 'webhook', enabled: true })}
        clean={cleanChannel}
        validate={validateChannel}
        columns={[
          { label: 'Type', render: (c) => c.type },
          { label: 'Enabled', render: (c) => (c.enabled ? <Badge variant="success">yes</Badge> : <Badge variant="secondary">no</Badge>) },
        ]}
        rowActions={(c) => <TestButton name={c.name} />}
        form={(f, set, { err }) => (
          <>
            <label className="block">
              Type
              <select aria-label="Type" className={inputClass} value={f.type} onChange={(e) => set({ type: e.target.value as NotifyChannel['type'] })}>
                <option value="webhook">Webhook (JSON POST)</option>
                <option value="discord">Discord webhook</option>
                <option value="ntfy">ntfy</option>
                <option value="email">Email (SMTP)</option>
                <option value="telegram">Telegram bot</option>
              </select>
            </label>

            {(f.type === 'webhook' || f.type === 'discord' || f.type === 'ntfy') && (
              <>
                <label className="block">
                  URL
                  <input aria-label="URL" className={inputClass} placeholder="https://…" value={f.url ?? ''} onChange={(e) => set({ url: e.target.value })} />
                  {err('url')}
                </label>
                {(f.type === 'webhook' || f.type === 'ntfy') && (
                  <label className="block">
                    Token
                    <input aria-label="Token" type="password" className={inputClass} value={f.token ?? ''} onChange={(e) => set({ token: e.target.value })} />
                  </label>
                )}
              </>
            )}

            {f.type === 'email' && (
              <>
                <label className="block">
                  SMTP server
                  <input aria-label="SMTP server" className={inputClass} placeholder="smtp.example.com" value={f.smtp_host ?? ''} onChange={(e) => set({ smtp_host: e.target.value })} />
                  {err('smtp_host')}
                </label>
                <label className="block">
                  SMTP port
                  <input aria-label="SMTP port" type="number" className={inputClass} placeholder="587" value={f.smtp_port ?? ''} onChange={(e) => set({ smtp_port: e.target.value ? Number(e.target.value) : undefined })} />
                  {err('smtp_port')}
                </label>
                <label className="block">
                  Username
                  <input aria-label="Username" className={inputClass} value={f.username ?? ''} onChange={(e) => set({ username: e.target.value })} />
                </label>
                <label className="block">
                  Password
                  <input aria-label="Password" type="password" className={inputClass} value={f.password ?? ''} onChange={(e) => set({ password: e.target.value })} />
                </label>
                <label className="block">
                  From
                  <input aria-label="From" className={inputClass} placeholder="alerts@example.com" value={f.from ?? ''} onChange={(e) => set({ from: e.target.value })} />
                  {err('from')}
                </label>
                <label className="block">
                  To (one address per line)
                  <textarea aria-label="To" rows={3} className={inputClass + ' font-mono text-xs'} value={(f.to ?? []).join('\n')} onChange={(e) => set({ to: e.target.value.split(/[\s,]+/).map((x) => x.trim()).filter(Boolean) })} />
                  {err('to')}
                </label>
              </>
            )}

            {f.type === 'telegram' && (
              <>
                <label className="block">
                  Bot token
                  <input aria-label="Bot token" type="password" className={inputClass} value={f.bot_token ?? ''} onChange={(e) => set({ bot_token: e.target.value })} />
                  {err('bot_token')}
                </label>
                <label className="block">
                  Chat id
                  <input aria-label="Chat id" className={inputClass} value={f.chat_id ?? ''} onChange={(e) => set({ chat_id: e.target.value })} />
                  {err('chat_id')}
                </label>
              </>
            )}

            <label className="flex items-center gap-2">
              <input type="checkbox" checked={f.enabled} onChange={(e) => set({ enabled: e.target.checked })} /> Enabled
            </label>
          </>
        )}
        after={() => <RulesTable channels={channels} />}
      />
    </div>
  )
}
