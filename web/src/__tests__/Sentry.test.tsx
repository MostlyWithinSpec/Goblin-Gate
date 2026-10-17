import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

// Mock the Sentry + Blocks API: no network. Keep pure helpers (isPluginNotRunning) real.
vi.mock('../api/sentry', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/sentry')>()
  return {
    ...real,
    getSentryStatus: vi.fn(),
    getIncidents: vi.fn(),
    getIncident: vi.fn(),
    answerIncident: vi.fn(),
    getDecisions: vi.fn(),
    getRules: vi.fn(),
    putRules: vi.fn(),
    setSentryMode: vi.fn(),
    getBlocks: vi.fn(),
    createBlock: vi.fn(),
    deleteBlock: vi.fn(),
    getModels: vi.fn(),
    trainModel: vi.fn(),
    getModel: vi.fn(),
    acceptModel: vi.fn(),
    rollbackModel: vi.fn(),
    putModelPolicy: vi.fn(),
    getExamples: vi.fn(),
    deleteExample: vi.fn()
  }
})

import * as sentry from '../api/sentry'
import Queue from '../pages/plugins/sentry/Queue'
import IncidentDetail from '../pages/plugins/sentry/IncidentDetail'
import ModeSelector from '../pages/plugins/sentry/ModeSelector'
import Learning from '../pages/plugins/sentry/Learning'

// --- Fixtures ---------------------------------------------------------------

const status = (mode = 'approve'): sentry.SentryStatus => ({
  mode,
  counts: { pending: 1 },
  rules: 2,
  watches: ['inbound'],
  cursor: 42,
  incidents: 7
})

const incident = (over: Partial<sentry.Incident> = {}): sentry.Incident => ({
  id: 11,
  key: 'k-11',
  status: 'pending',
  first: '2026-10-16T10:00:00Z',
  last: '2026-10-16T10:05:00Z',
  count: 3,
  alert: {
    id: 101,
    time: 1760604000,
    action: 'drop',
    src_ip: '203.0.113.5',
    src_port: 4444,
    dst_ip: '192.0.2.10',
    dst_port: 80,
    proto: 'tcp',
    signature: 'ET SCAN Nmap SYN scan',
    severity: 1
  },
  enrichment: {
    direction: 'inbound',
    remote: 'tor-node.example',
    local_name: 'web-01',
    seen: 3,
    remote_seen: 9,
    watched: true,
    blocked: false
  },
  recommendation: { tool: 'block', target: '203.0.113.5', minutes: 60 },
  summary: 'SYN scan from 203.0.113.5 against web-01',
  ...over
})

const decision = (over: Partial<sentry.Decision> = {}): sentry.Decision => ({
  id: 99,
  incident_id: 11,
  time: '2026-10-16T10:05:30Z',
  mode: 'approve',
  by: 'rule:2',
  rule_name: 'block-scan-sources',
  inputs: {
    direction: 'inbound',
    remote: 'tor-node.example',
    local_name: 'web-01',
    seen: 3,
    remote_seen: 9,
    watched: true,
    blocked: false
  },
  action: { tool: 'block', target: '203.0.113.5', minutes: 60 },
  executed: true,
  result: 'blocked for 60m',
  ...over
})

const answered = (answer: string) => ({ incident: incident({ status: 'acted' as const, answer }), decision: decision() })

const modelReport = (over: Partial<sentry.ModelReport> = {}): sentry.ModelReport => ({
  kind: 'knn',
  created: '2026-10-16T09:00:00Z',
  examples: 45,
  train: 36,
  test: 9,
  accuracy: 0.89,
  per_label: {
    block: { n: 20, predicted: 18, correct: 17, precision: 0.94, recall: 0.85 },
    ignore: { n: 25, predicted: 22, correct: 21, precision: 0.95, recall: 0.84 }
  },
  confusion: {},
  blocked_ignored: 1,
  ignored_blocked: 0,
  minutes_right: 30,
  thresholds: [{ threshold: 0.7, acted: 12, wrong: 1, hidden_attacks: 0 }],
  novel: 4,
  novel_correct: 3,
  novel_acted: 2,
  invalid: 0,
  latency_ms: 12,
  ...over
})

const modelVersion = (over: Partial<sentry.ModelVersion> = {}): sentry.ModelVersion => ({
  version: 2,
  kind: 'knn',
  created: '2026-10-16T09:00:00Z',
  examples: 45,
  report: modelReport(),
  ...over
})

const modelsState = (over: Partial<sentry.ModelsState> = {}): sentry.ModelsState => ({
  current: 2,
  previous: 1,
  policy: {
    use: 'suggest',
    thresholds: {
      mark_false_positive: 0.8,
      add_watch: 0.8,
      notify_admin: 0.9,
      query_reputation: 0.75
    }
  },
  versions: [modelVersion(), modelVersion({ version: 1, created: '2026-10-10T09:00:00Z' })],
  ...over
})

const learnedExample = (over: Partial<sentry.LearnedExample> = {}): sentry.LearnedExample => ({
  time: '2026-10-16T10:05:30Z',
  alert: incident().alert,
  enrichment: incident().enrichment,
  recommendation: { tool: 'block', target: '203.0.113.5', minutes: 60 },
  answer: 'block',
  ...over
})

// --- Render helpers ---------------------------------------------------------

const renderPage = (ui: React.ReactElement) =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>
  )

const renderRoute = (path: string, ui: React.ReactElement) =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter initialEntries={[path]}>
        <Routes>
          <Route path="/plugins/sentry/incidents/:id" element={ui} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>
  )

beforeEach(() => {
  vi.mocked(sentry.getSentryStatus).mockReset().mockResolvedValue(status())
  vi.mocked(sentry.getIncidents).mockReset().mockResolvedValue({ incidents: [incident()] })
  vi.mocked(sentry.getIncident).mockReset().mockResolvedValue({ incident: incident(), decisions: [decision()] })
  vi.mocked(sentry.answerIncident).mockReset().mockResolvedValue(answered('approve'))
  vi.mocked(sentry.setSentryMode).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
  vi.mocked(sentry.getModels).mockReset().mockResolvedValue(modelsState())
  vi.mocked(sentry.getExamples).mockReset().mockResolvedValue({ examples: [learnedExample()] })
  vi.mocked(sentry.acceptModel).mockReset().mockResolvedValue({ current: 1 })
  vi.mocked(sentry.rollbackModel).mockReset().mockResolvedValue({ current: 1 })
  vi.mocked(sentry.putModelPolicy).mockReset().mockImplementation(async (p) => p)
  vi.mocked(sentry.deleteExample).mockReset().mockResolvedValue(undefined)
  vi.mocked(sentry.trainModel).mockReset().mockResolvedValue({ job: { kind: 'knn', started: '2026-10-16T09:00:00Z' } })
})

// --- Queue ------------------------------------------------------------------

describe('Queue page', () => {
  test('approve sends the right body (no minutes)', async () => {
    renderPage(<Queue />)
    fireEvent.click(await screen.findByRole('button', { name: /Approve/ }))
    await waitFor(() => expect(sentry.answerIncident).toHaveBeenCalled())
    expect(sentry.answerIncident).toHaveBeenCalledWith(11, 'approve')
  })

  test('"always do this" asks for confirmation then sends the right body', async () => {
    renderPage(<Queue />)
    fireEvent.click(await screen.findByRole('button', { name: /Always do this/ }))

    // The confirm dialog opens; nothing is sent until it is confirmed.
    expect(await screen.findByRole('dialog')).toBeTruthy()
    expect(sentry.answerIncident).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Create rule' }))
    await waitFor(() => expect(sentry.answerIncident).toHaveBeenCalled())
    expect(sentry.answerIncident).toHaveBeenCalledWith(11, 'always')
  })

  test('block asks for confirmation before sending', async () => {
    renderPage(<Queue />)
    fireEvent.click(await screen.findByRole('button', { name: /^Block$/ }))

    // The confirm dialog opens; nothing is sent until it is confirmed.
    const dialog = await screen.findByRole('dialog')
    expect(dialog).toBeTruthy()
    expect(sentry.answerIncident).not.toHaveBeenCalled()

    // The minutes input is seeded from the recommendation (60).
    const minutesInput = within(dialog as HTMLElement).getByDisplayValue('60') as HTMLInputElement
    fireEvent.change(minutesInput, { target: { value: '30' } })

    fireEvent.click(within(dialog as HTMLElement).getByRole('button', { name: /^Block$/ }))
    await waitFor(() => expect(sentry.answerIncident).toHaveBeenCalled())
    expect(sentry.answerIncident).toHaveBeenCalledWith(11, 'block', 30)
  })
})

// --- Incident detail ("why did it do that?") --------------------------------

describe('Incident detail page', () => {
  test('renders the why record (inputs, action, result)', async () => {
    renderRoute('/plugins/sentry/incidents/11', <IncidentDetail />)

    expect(await screen.findByText(/SYN scan from 203\.0\.113\.5/)).toBeTruthy()
    // The decision's "why": inputs (enrichment), action, and result.
    expect(screen.getByText('tor-node.example')).toBeTruthy()
    expect(screen.getByText('block 203.0.113.5 for 60 min')).toBeTruthy()
    expect(screen.getByText('blocked for 60m')).toBeTruthy()
  })
})

// --- Mode selector ----------------------------------------------------------

describe('ModeSelector', () => {
  test('changing the mode PATCHes the setting', async () => {
    renderPage(<ModeSelector status={status('approve')} />)
    fireEvent.click(await screen.findByRole('button', { name: /Auto/ }))
    await waitFor(() => expect(sentry.setSentryMode).toHaveBeenCalled())
    expect(sentry.setSentryMode).toHaveBeenCalledWith('auto')
  })

  test('clicking the current mode does nothing', async () => {
    renderPage(<ModeSelector status={status('approve')} />)
    fireEvent.click(await screen.findByRole('button', { name: /Approve/ }))
    expect(sentry.setSentryMode).not.toHaveBeenCalled()
  })
})

// --- Learning page (G.O.B.L.I.N. models) ------------------------------------

describe('Learning page', () => {
  test('accepting a version calls acceptModel with that version', async () => {
    renderPage(<Learning />)
    // v2 is current; v1 has the Accept button.
    fireEvent.click(await screen.findByRole('button', { name: /Accept/ }))
    await waitFor(() => expect(sentry.acceptModel).toHaveBeenCalledWith(1))
  })

  test('rolling back calls rollbackModel', async () => {
    renderPage(<Learning />)
    fireEvent.click(await screen.findByRole('button', { name: /Roll back/ }))
    await waitFor(() => expect(sentry.rollbackModel).toHaveBeenCalled())
  })

  test('saving thresholds PUTs the policy with the edited value', async () => {
    renderPage(<Learning />)
    // Open the draft form.
    fireEvent.click(await screen.findByRole('button', { name: /Edit/ }))
    // Change the first range input (mark_false_positive, default 0.8).
    const ranges = screen.getAllByDisplayValue('0.8') as HTMLInputElement[]
    fireEvent.change(ranges[0], { target: { value: '0.9' } })
    // Save.
    fireEvent.click(screen.getByRole('button', { name: /Save thresholds/ }))
    await waitFor(() => expect(sentry.putModelPolicy).toHaveBeenCalled())
    const call = vi.mocked(sentry.putModelPolicy).mock.calls[0][0]
    expect(call.use).toBe('suggest')
    expect(call.thresholds.mark_false_positive).toBe(0.9)
  })

  test('removing a learned decision calls deleteExample with the alert id', async () => {
    renderPage(<Learning />)
    // The single example (answer 'block') appears only in "Learned behaviour".
    fireEvent.click(await screen.findByTitle('Remove this learned decision'))
    await waitFor(() => expect(sentry.deleteExample).toHaveBeenCalledWith(101))
  })
})
