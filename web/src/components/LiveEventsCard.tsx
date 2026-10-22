import { useEffect, useRef, useState } from 'react'
import {
  AlertTriangle,
  BatteryCharging,
  BatteryWarning,
  CheckCircle2,
  GitCommitHorizontal,
  LogIn,
  Pause,
  Play,
  Plug,
  PlugZap,
  PlusCircle,
  Radio,
  RotateCcw,
  ServerCrash,
  Shield,
  Sparkles,
  UserPlus,
  type LucideIcon,
} from 'lucide-react'
import { openEventStream, type LiveEvent } from '../api/events'

const MAX_EVENTS = 50
// Reconnect backoff for a stream that ended: 1 s, doubling up to 30 s.
const MIN_DELAY = 1000
const MAX_DELAY = 30_000

// Styling per event kind (config.NotifyEvents plus login and rollback).
const KIND_STYLE: Record<string, { icon: LucideIcon; color: string }> = {
  commit: { icon: GitCommitHorizontal, color: 'text-blue-600 dark:text-blue-400' },
  login: { icon: LogIn, color: 'text-gray-500 dark:text-gray-400' },
  login_failed: { icon: AlertTriangle, color: 'text-orange-600 dark:text-orange-400' },
  rollback: { icon: RotateCcw, color: 'text-orange-600 dark:text-orange-400' },
  ips_block: { icon: Shield, color: 'text-red-600 dark:text-red-400' },
  feed_error: { icon: ServerCrash, color: 'text-red-600 dark:text-red-400' },
  wan_down: { icon: PlugZap, color: 'text-red-600 dark:text-red-400' },
  wan_up: { icon: CheckCircle2, color: 'text-green-600 dark:text-green-400' },
  update_available: { icon: Sparkles, color: 'text-blue-600 dark:text-blue-400' },
  plugin: { icon: Plug, color: 'text-purple-600 dark:text-purple-400' },
  block_added: { icon: PlusCircle, color: 'text-red-600 dark:text-red-400' },
  new_device: { icon: UserPlus, color: 'text-green-600 dark:text-green-400' },
  test: { icon: Radio, color: 'text-gray-500 dark:text-gray-400' },
  ups_on_battery: { icon: BatteryWarning, color: 'text-orange-600 dark:text-orange-400' },
  ups_back: { icon: BatteryCharging, color: 'text-green-600 dark:text-green-400' },
  ups_low: { icon: BatteryWarning, color: 'text-red-600 dark:text-red-400' },
}

const fallbackStyle = { icon: Radio, color: 'text-gray-500 dark:text-gray-400' }

const timeOf = (ev: LiveEvent) => {
  const t = new Date(ev.time).getTime()
  return isNaN(t) ? 0 : t
}

const sortTop = (list: LiveEvent[]) =>
  [...list].sort((a, b) => timeOf(b) - timeOf(a)).slice(0, MAX_EVENTS)

// LiveEventsCard shows what happens on the box as it happens, over
// GET /api/v1/events (task 103): the last 50 events, an icon and colour per
// type, a pause button, and a reconnect with backoff when the stream drops.
export default function LiveEventsCard() {
  const [events, setEvents] = useState<LiveEvent[]>([])
  const [paused, setPaused] = useState(false)
  const [up, setUp] = useState(false)

  // Subscription state lives in refs so the stream is opened once and the
  // handlers always see the current values.
  const pausedRef = useRef(false)
  const buffered = useRef<LiveEvent[]>([])

  useEffect(() => {
    let stream: ReturnType<typeof openEventStream> | null = null
    let timer: ReturnType<typeof setTimeout> | null = null
    let delay = MIN_DELAY
    let alive = true

    const deliver = (ev: LiveEvent) => {
      if (pausedRef.current) buffered.current.unshift(ev)
      else setEvents((cur) => sortTop([ev, ...cur]))
    }

    const connect = () => {
      if (!alive) return
      stream = openEventStream({
        onData: deliver,
        onUp: () => {
          setUp(true)
          delay = MIN_DELAY
        },
        onDown: () => {
          setUp(false)
          // EventSource retries on its own; close it so only the reopened
          // stream delivers (two live streams would show every event twice).
          stream?.close()
          stream = null
          if (!alive || timer) return
          timer = setTimeout(() => {
            timer = null
            connect()
          }, delay)
          delay = Math.min(delay * 2, MAX_DELAY)
        },
      })
    }

    connect()
    return () => {
      alive = false
      if (timer) clearTimeout(timer)
      stream?.close()
    }
  }, [])

  const togglePause = () =>
    setPaused((p) => {
      const next = !p
      pausedRef.current = next
      if (!next) {
        const batch = buffered.current
        buffered.current = []
        if (batch.length > 0) setEvents((cur) => sortTop([...batch, ...cur]))
      }
      return next
    })

  return (
    <section className="rounded-lg bg-white p-3 shadow dark:bg-gray-800" aria-label="Live events">
      <div className="mb-2 flex items-center gap-2">
        <h3 className="flex-1 text-sm font-semibold text-gray-800 dark:text-white">Live events</h3>
        <span className="text-xs text-gray-500" data-testid="live-events-state">
          {paused ? 'paused' : up ? 'live' : 'reconnecting…'}
        </span>
        <button
          type="button"
          onClick={togglePause}
          aria-label={paused ? 'Resume live events' : 'Pause live events'}
          className="text-gray-500 hover:text-gray-700 dark:hover:text-gray-300"
        >
          {paused ? <Play size={14} /> : <Pause size={14} />}
        </button>
      </div>
      {events.length === 0 ? (
        <p className="text-sm text-gray-500">Nothing yet — events show up here as they happen.</p>
      ) : (
        <ul className="space-y-1">
          {events.map((ev, i) => {
            const style = KIND_STYLE[ev.event] ?? fallbackStyle
            const Icon = style.icon
            const when = new Date(ev.time).getTime()
            return (
              <li key={`${ev.event}-${ev.key ?? ''}-${ev.time}-${i}`} className="flex items-start gap-2 text-sm">
                <Icon size={14} className={`mt-0.5 shrink-0 ${style.color}`} aria-hidden />
                <span className="flex-1 truncate text-gray-700 dark:text-gray-200" title={ev.message}>
                  <span className="font-medium text-gray-800 dark:text-white">{ev.title || ev.event}</span>
                  {ev.message ? ` — ${ev.message}` : ''}
                </span>
                <span className="shrink-0 text-xs text-gray-400">{isNaN(when) ? '' : new Date(ev.time).toLocaleTimeString()}</span>
              </li>
            )
          })}
        </ul>
      )}
    </section>
  )
}
