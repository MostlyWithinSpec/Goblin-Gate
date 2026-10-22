// Templates page (task 111): the form is built from the template's inputs, a
// preview shows the diff, the answer problems land on their field, and Apply
// saves to the candidate and refreshes the commit bar's change count.
import { beforeEach, describe, expect, it, vi } from 'vitest'
import React from 'react'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('../api/templates', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/templates')>()
  return { ...real, getTemplates: vi.fn(), previewTemplate: vi.fn(), applyTemplate: vi.fn() }
})
vi.mock('../api/config', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/config')>()
  return { ...real, getConfigDiff: vi.fn(), getSystemStatus: vi.fn() }
})

import * as tpl from '../api/templates'
import * as cfgapi from '../api/config'
import { APIRequestError } from '../api/client'
import { CommitBar } from '../components/CommitBar'
import Templates, { answersFrom } from '../pages/policy/Templates'
import type { TemplateInfo } from '../api/templates'

const guest: TemplateInfo = {
  id: 'guest-network',
  version: 1,
  title: 'Guest network',
  description: 'A separate network for visitors.',
  inputs: [
    { name: 'name', label: 'Network name', type: 'name', default: 'guest', required: true },
    {
      name: 'parent',
      label: 'On interface',
      type: 'interface',
      options: ['wan1', 'lan1'],
      default: 'lan1',
      required: true,
      help: 'The physical port the VLAN runs on.',
    },
    { name: 'vlan_id', label: 'VLAN ID', type: 'vlan_id', default: '50', required: true },
    { name: 'address', label: 'Box address', type: 'cidr', default: '192.168.50.1/24', required: true },
    { name: 'dns_filter', label: 'DNS filter profile', type: 'profile', options: ['family'] },
  ],
}

const bedtime: TemplateInfo = {
  id: 'bedtime',
  version: 1,
  title: 'Bedtime for some devices',
  description: 'Turn off the internet at night.',
  inputs: [
    { name: 'devices', label: 'Device addresses', type: 'ips', required: true },
    { name: 'days', label: 'Days', type: 'days', default: 'mon,tue,wed', required: true },
    { name: 'start', label: 'From', type: 'time', default: '21:00', required: true },
    { name: 'kill_switch', label: 'Kill switch', type: 'bool', default: 'true', required: true },
  ],
}

const limited: TemplateInfo = {
  id: 'limited-forward',
  version: 1,
  title: 'Port forward for some places only',
  description: 'Reach a server from the countries you list.',
  inputs: [{ name: 'sources', label: 'Allowed from', type: 'sources', required: true }],
}

const noWan: TemplateInfo = {
  id: 'vpn-network',
  version: 1,
  title: 'Network through a VPN provider',
  description: 'Send a network through the tunnel.',
  inputs: [],
  unavailable: 'add a VPN provider first',
}

const emptyResult: tpl.TemplateResult = { changes: [], input_errors: [], validation_errors: [] }

function wrap(ui: React.ReactElement, container?: HTMLElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const tree = (
    <QueryClientProvider client={qc}>
      <MemoryRouter>{ui}</MemoryRouter>
    </QueryClientProvider>
  )
  // render's second argument is its options object; the container goes inside it
  return container ? render(tree, { container }) : render(tree)
}

// openTemplate renders the page and opens one template's drawer, returning a
// `form` scoped to it — the cards behind the drawer show the same titles and
// descriptions, so an unscoped query would be ambiguous. Cards keep catalog
// order and an unavailable template has no button, so the usable ones are
// addressable by index.
//
// A sync fireEvent.click does not flush the setState that opens the drawer, so
// the drawer is awaited through findByRole: when it resolves the drawer is
// committed and every plain query below can see it.
//
// testing-library removes the previous test's DOM only when the next render
// happens, and the page test at the bottom of this file leaves a drawer open, so
// each render goes into its own fresh container — otherwise the drawer another
// test left behind would satisfy findByRole before this test's own click landed.
function inFreshPage(): HTMLElement {
  const el = document.createElement('div')
  document.body.appendChild(el)
  return el
}

async function openTemplate(index: number, first: string) {
  wrap(<Templates />, inFreshPage())
  await screen.findByText('Guest network')
  fireEvent.click(screen.getAllByRole('button', { name: 'Set up' })[index])
  const drawer = await screen.findByRole('dialog')
  // proves this test's own drawer is the one that was found
  await within(drawer).findByText(first)
  return within(drawer)
}

beforeEach(() => {
  vi.mocked(tpl.getTemplates).mockReset().mockResolvedValue([guest, bedtime, limited, noWan])
  vi.mocked(tpl.previewTemplate).mockReset().mockResolvedValue(emptyResult)
  vi.mocked(tpl.applyTemplate).mockReset().mockResolvedValue({ status: 'ok', changes: [] })
  vi.mocked(cfgapi.getConfigDiff).mockReset().mockResolvedValue({ count: 0, changes: [] })
  vi.mocked(cfgapi.getSystemStatus).mockReset().mockResolvedValue({
    version: '0.1.0',
    pending_confirm: null,
    reboot_required: false,
  })
})

describe('answersFrom', () => {
  it('prefills every input with its default', () => {
    expect(answersFrom(guest.inputs)).toEqual({
      name: 'guest',
      parent: 'lan1',
      vlan_id: '50',
      address: '192.168.50.1/24',
      dns_filter: '',
    })
  })
})

describe('Templates page', () => {
  it('lists a card per template and no form for an unavailable one', async () => {
    wrap(<Templates />)
    expect(await screen.findByText('Guest network')).toBeInTheDocument()
    expect(screen.getByText('Bedtime for some devices')).toBeInTheDocument()
    expect(screen.getByText('add a VPN provider first')).toBeInTheDocument()
    // three usable templates, so three buttons â€” not four
    expect(screen.getAllByRole('button', { name: 'Set up' })).toHaveLength(3)
  })

  it('search filters the cards', async () => {
    wrap(<Templates />)
    await screen.findByText('Guest network')
    fireEvent.change(screen.getByLabelText('Search templates'), { target: { value: 'bedtime' } })
    expect(screen.queryByText('Guest network')).not.toBeInTheDocument()
    expect(screen.getByText('Bedtime for some devices')).toBeInTheDocument()
  })

  it('builds a field per input type, prefilled with the defaults', async () => {
    const form = await openTemplate(0, 'Network name')

    expect((form.getByLabelText('Network name') as HTMLInputElement).value).toBe('guest')
    const parent = form.getByLabelText('On interface') as HTMLSelectElement
    expect(parent.tagName).toBe('SELECT')
    expect([...parent.options].map((o) => o.value)).toEqual(['wan1', 'lan1'])
    expect(parent.value).toBe('lan1')
    const vlan = form.getByLabelText('VLAN ID') as HTMLInputElement
    expect(vlan.type).toBe('number')
    expect(vlan.value).toBe('50')
    expect((form.getByLabelText('Box address') as HTMLInputElement).value).toBe('192.168.50.1/24')
    // an optional profile select also offers "none"
    const profile = form.getByLabelText('DNS filter profile') as HTMLSelectElement
    expect([...profile.options].map((o) => o.value)).toEqual(['', 'family'])
    expect(form.getByText('The physical port the VLAN runs on.')).toBeInTheDocument()
  })

  it('renders days, time and bool inputs', async () => {
    const form = await openTemplate(1, 'Device addresses')

    // days: seven toggles, monâ€“wed on by default
    const boxes = () =>
      form.getAllByText(/^(Mon|Tue|Wed|Thu|Fri|Sat|Sun)$/).map((el) => el.querySelector('input') as HTMLInputElement)
    const checked = () => boxes().filter((b) => b.checked)
    expect(boxes()).toHaveLength(7)
    expect(checked()).toHaveLength(3)
    fireEvent.click(form.getByText('Sun'))
    expect(checked()).toHaveLength(4)

    expect((form.getByLabelText('From') as HTMLInputElement).type).toBe('time')
    const kill = form.getByLabelText('Kill switch') as HTMLInputElement
    expect(kill.checked).toBe(true)
    fireEvent.click(kill)
    expect(kill.checked).toBe(false)
  })

  it('sends the answers the server expects', async () => {
    const form = await openTemplate(1, 'Device addresses')
    fireEvent.change(form.getByLabelText('Device addresses'), { target: { value: '192.168.1.20, 192.168.1.31' } })
    fireEvent.click(form.getByText('Sun'))
    fireEvent.click(form.getByLabelText('Kill switch'))
    fireEvent.click(form.getByRole('button', { name: 'Apply' }))

    await waitFor(() =>
      expect(tpl.applyTemplate).toHaveBeenCalledWith('bedtime', {
        devices: '192.168.1.20, 192.168.1.31',
        days: 'mon,tue,wed,sun',
        start: '21:00',
        kill_switch: 'false',
      })
    )
  })

  it('shows a country/CIDR list as chips while typing', async () => {
    const form = await openTemplate(2, 'Allowed from')
    fireEvent.change(form.getByLabelText('Allowed from'), { target: { value: 'US, 203.0.113.0/24' } })
    await waitFor(() => expect(form.getByText('203.0.113.0/24')).toBeInTheDocument())
    expect(form.getByText('US')).toBeInTheDocument()
  })

  it('preview shows the diff the template would make', async () => {
    vi.mocked(tpl.previewTemplate).mockResolvedValue({
      changes: [
        { path: 'vlans[0]', op: 'add', new: { name: 'guest', vlan_id: 50 } },
        { path: 'policies[0]', op: 'add', new: { name: 'guest-internet' } },
      ],
      input_errors: [],
      validation_errors: [],
    })
    const form = await openTemplate(0, 'Network name')
    fireEvent.click(form.getByRole('button', { name: 'Preview' }))

    await waitFor(() => expect(form.getByText('This changes 2 things')).toBeInTheDocument())
    expect(form.getByText('vlans[0]')).toBeInTheDocument()
    expect(form.getByText('policies[0]')).toBeInTheDocument()
    expect(tpl.previewTemplate).toHaveBeenCalledWith('guest-network', answersFrom(guest.inputs))
    expect(tpl.applyTemplate).not.toHaveBeenCalled()
  })

  it('a preview with no changes says so instead of applying nothing', async () => {
    vi.mocked(tpl.previewTemplate).mockResolvedValue({
      changes: [],
      input_errors: [{ input: '', message: 'no WAN interface is enabled' }],
      validation_errors: [],
    })
    const form = await openTemplate(0, 'Network name')
    fireEvent.click(form.getByRole('button', { name: 'Preview' }))

    await waitFor(() => expect(form.getByText('This changes nothing')).toBeInTheDocument())
    expect(form.getByText('no WAN interface is enabled')).toBeInTheDocument()
  })

  it('maps apply errors onto the field they belong to', async () => {
    vi.mocked(tpl.applyTemplate).mockRejectedValue(
      new APIRequestError("the template's answers need fixing", 400, 'template_invalid', {
        changes: [],
        input_errors: [
          { input: 'name', message: 'a policy named "guest" already exists' },
          { input: 'vlan_id', message: 'VLAN 50 is already "work" on lan1' },
        ],
        validation_errors: [{ path: 'vlans[2].vlan_id', message: 'must be 1-4094' }],
      })
    )
    const form = await openTemplate(0, 'Network name')
    fireEvent.click(form.getByRole('button', { name: 'Apply' }))

    // each answer problem sits under the field it belongs to
    const nameField = form.getByLabelText('Network name').closest('div') as HTMLElement
    const vlanField = form.getByLabelText('VLAN ID').closest('div') as HTMLElement
    await waitFor(() => expect(within(nameField).getByText('a policy named "guest" already exists')).toBeInTheDocument())
    expect(within(vlanField).getByText('VLAN 50 is already "work" on lan1')).toBeInTheDocument()
    // a problem with no input of its own and every validation problem go above the questions
    expect(form.getByText('vlans[2].vlan_id: must be 1-4094')).toBeInTheDocument()
    expect(form.getByText('The answers need fixing before this can be applied.')).toBeInTheDocument()
  })

  it('an answer edited after a preview drops the stale diff', async () => {
    vi.mocked(tpl.previewTemplate).mockResolvedValue({
      changes: [{ path: 'vlans[0]', op: 'add', new: { name: 'guest' } }],
      input_errors: [],
      validation_errors: [],
    })
    const form = await openTemplate(0, 'Network name')
    fireEvent.click(form.getByRole('button', { name: 'Preview' }))
    await waitFor(() => expect(form.getByText('This changes 1 thing')).toBeInTheDocument())
    fireEvent.change(form.getByLabelText('Network name'), { target: { value: 'visitors' } })
    await waitFor(() => expect(form.queryByText('This changes 1 thing')).not.toBeInTheDocument())
  })

  it('apply saves to the candidate and refreshes the commit bar count', async () => {
    vi.mocked(tpl.applyTemplate).mockResolvedValue({
      status: 'ok',
      changes: [{ path: 'vlans[0]', op: 'add', new: { name: 'guest' } }],
    })
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const spy = vi.spyOn(qc, 'invalidateQueries')
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <CommitBar />
          <Templates />
        </MemoryRouter>
      </QueryClientProvider>
    )
    await screen.findByText('Guest network')
    fireEvent.click(screen.getAllByRole('button', { name: 'Set up' })[0])
    const drawer = await screen.findByRole('dialog')
    fireEvent.click(within(drawer).getByRole('button', { name: 'Apply' }))

    // applied: the page tells the admin the change is only in the candidate and
    // points at the commit bar; the bar's query is invalidated so its count moves
    const form = within(drawer)
    await waitFor(() => expect(form.getByText(/Added to the candidate/)).toBeInTheDocument())
    expect(form.getByText(/commit it in the bar at the bottom/i)).toBeInTheDocument()
    await waitFor(() =>
      expect(spy).toHaveBeenCalledWith(expect.objectContaining({ queryKey: ['configDiff'] }))
    )
    expect(spy).toHaveBeenCalledWith(expect.objectContaining({ queryKey: ['candidate'] }))
  })
})
