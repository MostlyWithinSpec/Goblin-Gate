// Live events API: the Server-Sent Events stream of GET /api/v1/events (task 102).
// Types mirror internal/notify/notify.go Event by JSON name.

// LiveEvent is one published event. Kind is one of config.NotifyEvents plus
// "login" and "rollback".
export interface LiveEvent {
  event: string
  key?: string
  title: string
  message: string
  time: string
  fields?: Record<string, string>
}

// Event kinds the card knows (internal/config NotifyEvents plus login and
// rollback). The server sends each one as "event: <kind>", so the stream
// helper listens for every name, plus "message" for anything sent without one.
export const EVENT_TYPES = [
  'commit',
  'login',
  'login_failed',
  'rollback',
  'ips_block',
  'feed_error',
  'wan_down',
  'wan_up',
  'update_available',
  'plugin',
  'block_added',
  'new_device',
  'test',
] as const

export const eventsStreamURL = (types?: string[]) =>
  types && types.length > 0 ? `/api/v1/events?types=${types.join(',')}` : '/api/v1/events'

// Stream is the small handle the live events card keeps: enough to drop the
// stream and to reopen it, nothing more.
export interface Stream {
  close(): void
}

export interface StreamHandlers {
  // onData gets every well-formed event the stream delivers.
  onData: (ev: LiveEvent) => void
  // onUp fires the first time the stream delivers something, so the card can
  // say "live". It never fires twice on one stream.
  onUp: () => void
  // onDown fires when the stream ends. EventSource retries a failed
  // connection on its own and keeps the object alive; a stream that delivered
  // events and then ended is gone, and the caller reopens one.
  onDown: () => void
}

// openEventStream opens the event stream of task 102 and wires it by hand with
// addEventListener. EventSource is not an element, so a React on<Event> prop
// never reaches it; the browser's own "open" is not observable here either, so
// a stream counts as up when it delivers its first event. EventSource cannot
// send an Authorization header, so this relies on the session cookie (same
// origin).
export function openEventStream(handlers: StreamHandlers): Stream {
  const source = new EventSource(eventsStreamURL())
  let up = false
  const onMessage = (e: MessageEvent) => {
    let ev: LiveEvent
    try {
      ev = JSON.parse(String(e.data)) as LiveEvent
    } catch {
      return
    }
    if (!ev || typeof ev.event !== 'string') return
    if (!up) {
      up = true
      handlers.onUp()
    }
    handlers.onData(ev)
  }
  for (const type of ['message', ...EVENT_TYPES]) source.addEventListener(type, onMessage)
  source.addEventListener('error', () => handlers.onDown())
  return { close: () => source.close() }
}
