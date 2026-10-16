// Client-side validation for schedule ranges, mirroring the server rules:
// HH:MM times, end before start means overnight (into the next day), end may
// be "24:00" (= midnight) but start may not, and start == end is an error.
import type { Errors } from '../../components/ConfigList'
import type { Schedule, ScheduleRange } from '../../api/objects'

export const DAYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'] as const
export type Day = (typeof DAYS)[number]
export const DAY_LABELS: Record<Day, string> = {
  mon: 'Mon', tue: 'Tue', wed: 'Wed', thu: 'Thu', fri: 'Fri', sat: 'Sat', sun: 'Sun',
}

const TIME_RE = /^([01]\d|2[0-3]):[0-5]\d$/
const MIDNIGHT_RE = /^24:00$/

// parseClock returns minutes since midnight, or null when invalid. allow24
// accepts "24:00" (1440) for end times only.
export function parseClock(s: string, allow24: boolean): number | null {
  if (allow24 && MIDNIGHT_RE.test(s)) return 1440
  if (!TIME_RE.test(s)) return null
  const [h, m] = s.split(':').map(Number)
  return h * 60 + m
}

// newRange returns a default range (weekdays 09:00-17:00).
export function newRange(): ScheduleRange {
  return { days: ['mon', 'tue', 'wed', 'thu', 'fri'], start: '09:00', end: '17:00' }
}

// validateSchedule checks one schedule and returns errors keyed by field name
// (ranges[<i>].start, ranges[<i>].end, ranges[<i>].days).
export function validateSchedule(s: Schedule): Errors {
  const errs: Errors = {}
  if (!s.ranges || s.ranges.length === 0) {
    errs.ranges = 'At least one range is required'
    return errs
  }
  for (let i = 0; i < s.ranges.length; i++) {
    const r = s.ranges[i]
    const key = `ranges[${i}]`
    if (!r.days || r.days.length === 0) {
      errs[`${key}.days`] = 'Pick at least one day'
      continue
    }
    for (const d of r.days) {
      if (!(DAYS as readonly string[]).includes(d)) {
        errs[`${key}.days`] = `Unknown day "${d}"`
        break
      }
    }
    const start = parseClock(r.start, false)
    if (start === null) errs[`${key}.start`] = 'Use HH:MM (00:00–23:59)'
    const end = parseClock(r.end, true)
    if (end === null) errs[`${key}.end`] = 'Use HH:MM (00:00–24:00)'
    if (start !== null && end !== null && start === end) {
      errs[`${key}.end`] = 'End must differ from start (use 24:00 for midnight)'
    }
  }
  return errs
}

// weekCoverage marks which of the 7 days a schedule covers, for the table strip.
export function weekCoverage(s: Schedule): boolean[] {
  const covered = DAYS.map(() => false)
  for (const r of s.ranges ?? []) {
    for (let i = 0; i < DAYS.length; i++) {
      if (r.days?.includes(DAYS[i])) covered[i] = true
    }
  }
  return covered
}
