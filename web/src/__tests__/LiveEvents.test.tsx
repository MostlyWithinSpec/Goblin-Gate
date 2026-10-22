import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { LiveEvent } from '../api/events'

vi.mock('../api/events', async (orig) => {
  const real = await orig<typeof import('../api/events')>()
  return { ...real, openEventStream: vi.fn() }
})
import * as eventsApi from '../api/events'
import LiveEventsCard from '../components/LiveEventsCard'

// jsdom has no EventSource. The card subscribes by hand through
// openEventStream, so the fake is a plain object holding the three handlers
// and nothing else.
class FakeStream {
  static instances: FakeStream[] = []
  closed = false
  handlers: eventsApi.StreamHandlers
  constructor(handlers: eventsApi.StreamHandlers) {
    this.handlers = handlers
    FakeStream.instances.push(this)
  }
  close() {
    this.closed = true
  }
  emit(name: string, data: unknown) {
    this.handlers.onData(typeof data === 'string' ? (JSON.parse(data) as LiveEvent) : (data as LiveEvent))
  }
  // first event: the card can only tell the stream is up from what it delivers
  deliver(ev: LiveEvent) {
    this.handlers.onUp()
    this.handlers.onData(ev)
  }
  drop() {
    this.handlers.onDown()
  }
}

const eventAt = (n: number, kind = 'commit', title = 'Commit applied'): LiveEvent => ({
  event: kind,
  title,
  message: `${kind} number ${n}`,
  time: new Date(Date.UTC(2026, 9, 21, 10, 0, n)).toISOString(),
})

beforeEach(() => {
  FakeStream.instances = []
  vi.stubGlobal('EventSource', class {
    constructor(public url: string) {}
    addEventListener() {}
    removeEventListener() {}
    close() {}
  })
  vi.mocked(eventsApi.openEventStream).mockImplementation((handlers) => new FakeStream(handlers))
})

afterEach(() => {
  vi.unstubAllGlobals()
})

const wait = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))
const state = () => screen.getByTestId('live-events-state')
const titles = () =>
  [...document.querySelectorAll('[aria-label="Live events"] li span.font-medium')].map((n) => n.textContent)

// The card reopens the stream after a timeout. Sleep through the whole
// backoff step in one call (waiting in small slices would let the armed timer
// fire early) and report how many streams existed before and after the sleep.
async function reopenedIn(ms: number) {
  const before = FakeStream.instances.length
  await wait(ms)
  return { before, after: FakeStream.instances.length }
}

// React applies the state a stream handler set once that handler has returned,
// so a macrotask later the DOM shows what the event changed.
async function flush() {
  await act(async () => {
    await wait(0)
  })
}

async function shown(want: string) {
  await waitFor(() => expect(state()).toHaveTextContent(want))
}

describe('Live events card', () => {
  test('opens the event stream and shows an event as it arrives', async () => {
    render(<LiveEventsCard />)
    expect(FakeStream.instances).toHaveLength(1)
    expect(state()).toHaveTextContent('reconnecting')

    const stream = FakeStream.instances[0]
    stream.deliver(eventAt(0, 'test', 'Notification test'))
    await shown('live')
    expect(screen.getByText(/Notification test/)).toBeInTheDocument()
    expect(screen.getByText(/test number 0/)).toBeInTheDocument()

    stream.emit('wan_down', eventAt(1, 'wan_down', 'WAN down'))
    await flush()
    expect(titles()).toEqual(['WAN down', 'Notification test'])
  })

  test('ignores an event without a kind', () => {
    render(<LiveEventsCard />)
    FakeStream.instances[0].emit('message', { title: 'no kind', message: 'x', time: new Date().toISOString() } as unknown as LiveEvent)
    expect(screen.getByText(/Nothing yet/)).toBeInTheDocument()
  })

  test('reconnects after the stream drops, with growing backoff', async () => {
    render(<LiveEventsCard />)
    const first = FakeStream.instances[0]
    first.deliver(eventAt(1, 'login', 'admin signed in'))
    await shown('live')

    first.drop()
    await shown('reconnecting')
    // the dropped stream is closed so its own retries cannot deliver twice
    expect(first.closed).toBe(true)
    // the first backoff step is one second: not before it, reopened by the end
    const slow = await reopenedIn(900)
    expect(slow.before).toBe(1)
    expect(slow.after).toBe(1)
    const opened = await reopenedIn(700)
    expect(opened.after).toBe(2)

    const second = FakeStream.instances[1]
    second.deliver(eventAt(2, 'commit', 'Commit applied'))
    await shown('live')
    // the list survives the reconnect and new events land on top
    expect(titles()).toEqual(['Commit applied', 'admin signed in'])

    second.drop()
    await shown('reconnecting')
    // The second step is not a clean 2 s to measure: the wait that let the
    // first step fire already ate part of it. All that is exact is that the
    // step doubled — so by now (well over one second, the previous step) the
    // stream is back and no third one opened meanwhile.
    const grew = await reopenedIn(1600)
    expect(grew.after).toBe(3)
  })

  test('keeps only the last 50 events, newest first', async () => {
    render(<LiveEventsCard />)
    const stream = FakeStream.instances[0]
    for (let i = 1; i <= 55; i++) stream.emit('commit', eventAt(i, 'commit', `Commit ${i}`))
    await flush()
    expect(titles()).toHaveLength(50)
    expect(titles()[0]).toBe('Commit 55')
    expect(titles()[49]).toBe('Commit 6')
    expect(screen.queryByText('Commit 5')).not.toBeInTheDocument()
  })

  test('pause holds the list and resume shows what arrived meanwhile', async () => {
    render(<LiveEventsCard />)
    const stream = FakeStream.instances[0]
    stream.deliver(eventAt(1, 'commit', 'Commit before pause'))
    await shown('live')

    fireEvent.click(screen.getByLabelText('Pause live events'))
    await shown('paused')
    stream.emit('ips_block', eventAt(2, 'ips_block', 'IPS blocked traffic'))
    await flush()
    expect(titles()).toEqual(['Commit before pause'])

    fireEvent.click(screen.getByLabelText('Resume live events'))
    await shown('live')
    expect(titles()).toEqual(['IPS blocked traffic', 'Commit before pause'])
  })

  test('closing the card stops reconnecting', async () => {
    const { unmount } = render(<LiveEventsCard />)
    FakeStream.instances[0].drop()
    unmount()
    await wait(1200)
    expect(FakeStream.instances).toHaveLength(1)
  })
})
