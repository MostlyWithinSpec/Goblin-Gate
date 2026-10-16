import { beforeEach, describe, expect, test, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { Candidate } from '../api/objects'

// Mock the API module: no network. ConfigList calls getCandidate + patchSection.
vi.mock('../api/objects', async (importOriginal) => {
  const real = await importOriginal<typeof import('../api/objects')>()
  return { ...real, getCandidate: vi.fn(), patchSection: vi.fn() }
})
import * as api from '../api/objects'
import Schedules from '../pages/policy/Schedules'

const cfg: Candidate = {
  schedules: [{ name: 'business-hours', comment: 'Weekday office hours', ranges: [{ days: ['mon', 'tue', 'wed', 'thu', 'fri'], start: '09:00', end: '17:00' }] }],
}

const lastPatch = () => {
  const calls = vi.mocked(api.patchSection).mock.calls
  return calls[calls.length - 1]
}

beforeEach(() => {
  vi.mocked(api.getCandidate).mockResolvedValue(structuredClone(cfg))
  vi.mocked(api.patchSection).mockReset().mockResolvedValue({ status: 'ok', validation_errors: null })
})

describe('Schedules page', () => {
  test('renders the list with a week strip and range summary', async () => {
    render(<Schedules />)
    expect(await screen.findByRole('button', { name: 'business-hours' })).toBeInTheDocument()
    expect(screen.getByText(/Mon Tue Wed Thu Fri 09:00–17:00/)).toBeInTheDocument()
  })

  test('rejects an invalid start time (HH:MM)', async () => {
    render(<Schedules />)
    await screen.findByRole('button', { name: 'business-hours' })
    fireEvent.click(screen.getByRole('button', { name: /New schedule/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'bad-start' } })
    fireEvent.change(screen.getByLabelText('Start time'), { target: { value: '9am' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Use HH:MM (00:00–23:59)')).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('rejects an invalid end time (HH:MM, 24:00 allowed)', async () => {
    render(<Schedules />)
    await screen.findByRole('button', { name: 'business-hours' })
    fireEvent.click(screen.getByRole('button', { name: /New schedule/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'bad-end' } })
    fireEvent.change(screen.getByLabelText('End time'), { target: { value: '25:00' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Use HH:MM (00:00–24:00)')).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('rejects a range whose start equals its end', async () => {
    render(<Schedules />)
    await screen.findByRole('button', { name: 'business-hours' })
    fireEvent.click(screen.getByRole('button', { name: /New schedule/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'same' } })
    // default range is 09:00–17:00; make end equal to start
    fireEvent.change(screen.getByLabelText('End time'), { target: { value: '09:00' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('End must differ from start (use 24:00 for midnight)')).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })

  test('allows an overnight range (end before start)', async () => {
    render(<Schedules />)
    await screen.findByRole('button', { name: 'business-hours' })
    fireEvent.click(screen.getByRole('button', { name: /New schedule/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'night-shift' } })
    // 22:00 -> 06:00 is overnight and valid
    fireEvent.change(screen.getByLabelText('Start time'), { target: { value: '22:00' } })
    fireEvent.change(screen.getByLabelText('End time'), { target: { value: '06:00' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, list] = lastPatch() as [string, api.Schedule[]]
    expect(path).toBe('schedules')
    expect(list).toContainEqual({ name: 'night-shift', ranges: [{ days: ['mon', 'tue', 'wed', 'thu', 'fri'], start: '22:00', end: '06:00' }] })
  })

  test('PATCHes the whole schedules list with a valid new schedule', async () => {
    render(<Schedules />)
    await screen.findByRole('button', { name: 'business-hours' })
    fireEvent.click(screen.getByRole('button', { name: /New schedule/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'late-evening' } })
    fireEvent.change(screen.getByLabelText('Comment'), { target: { value: 'after hours' } })
    // default range is weekdays 09:00–17:00; keep it, add a second range to 23:59
    fireEvent.click(screen.getByRole('button', { name: '+ Add range' }))
    const endInputs = screen.getAllByLabelText('End time')
    fireEvent.change(endInputs[1], { target: { value: '23:59' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [path, list] = lastPatch() as [string, api.Schedule[]]
    expect(path).toBe('schedules')
    // the original schedule is preserved and the new one appended with both ranges
    expect(list).toHaveLength(2)
    expect(list[0]).toEqual(cfg.schedules![0])
    expect(list[1].name).toBe('late-evening')
    expect(list[1].comment).toBe('after hours')
    expect(list[1].ranges).toHaveLength(2)
  })

  test('midnight checkbox stores end as 24:00', async () => {
    render(<Schedules />)
    await screen.findByRole('button', { name: 'business-hours' })
    fireEvent.click(screen.getByRole('button', { name: /New schedule/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'to-midnight' } })
    fireEvent.click(screen.getByRole('checkbox', { name: /until midnight/ }))
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(api.patchSection).toHaveBeenCalled())
    const [, list] = lastPatch() as [string, api.Schedule[]]
    expect(list[1].ranges[0].end).toBe('24:00')
  })

  test('requires at least one day per range', async () => {
    render(<Schedules />)
    await screen.findByRole('button', { name: 'business-hours' })
    fireEvent.click(screen.getByRole('button', { name: /New schedule/ }))
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'no-days' } })
    // uncheck every day (default range has mon-fri checked)
    for (const d of ['Mon', 'Tue', 'Wed', 'Thu', 'Fri']) {
      const cb = screen.getAllByRole('checkbox').find((c) => c.closest('label')?.textContent === d)!
      fireEvent.click(cb)
    }
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Pick at least one day')).toBeInTheDocument()
    expect(api.patchSection).not.toHaveBeenCalled()
  })
})
