// Named time schedules (config "schedules"): one or more ranges of days plus
// start/end times; policies can attach one to match traffic only in those hours.
import { ConfigList, inputClass, omitEmpty } from '../../components/ConfigList'
import type { Schedule, ScheduleRange } from '../../api/objects'
import { DAYS, DAY_LABELS, newRange, validateSchedule, weekCoverage } from './scheduleLogic'

// DayPicker: seven Mon–Sun toggles plus quick presets.
function DayPicker({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
  const toggle = (d: string) => onChange(value.includes(d) ? value.filter((x) => x !== d) : [...value, d])
  return (
    <div>
      <div className="flex flex-wrap gap-1">
        {DAYS.map((d) => (
          <label
            key={d}
            className={`cursor-pointer rounded border px-2 py-0.5 text-xs ${
              value.includes(d)
                ? 'border-blue-600 bg-blue-600 text-white'
                : 'border-gray-300 text-gray-700 dark:border-gray-600 dark:text-gray-300'
            }`}
          >
            <input type="checkbox" className="sr-only" checked={value.includes(d)} onChange={() => toggle(d)} />
            {DAY_LABELS[d]}
          </label>
        ))}
      </div>
      <div className="mt-1 flex gap-2 text-xs text-gray-500">
        <button type="button" onClick={() => onChange([...DAYS])}>every day</button>
        <button type="button" onClick={() => onChange(['mon', 'tue', 'wed', 'thu', 'fri'])}>weekdays</button>
        <button type="button" onClick={() => onChange(['sat', 'sun'])}>weekend</button>
      </div>
    </div>
  )
}

// WeekStrip: a compact Mon–Sun strip showing which days the schedule covers.
function WeekStrip({ s }: { s: Schedule }) {
  const covered = weekCoverage(s)
  return (
    <span className="inline-flex gap-0.5" title={covered.map((c, i) => (c ? DAY_LABELS[DAYS[i]] : null)).filter(Boolean).join(', ')}>
      {DAYS.map((d, i) => (
        <span
          key={d}
          className={`h-4 w-2 rounded-sm ${covered[i] ? 'bg-blue-500' : 'bg-gray-200 dark:bg-gray-600'}`}
        />
      ))}
    </span>
  )
}

// RangeRow edits one range: day picker plus start/end time inputs. End may be
// "24:00" (midnight) via a checkbox, since <input type="time"> can't show it.
function RangeRow({ r, onChange, onRemove }: { r: ScheduleRange; onChange: (r: ScheduleRange) => void; onRemove?: () => void }) {
  const midnight = r.end === '24:00'
  return (
    <div className="rounded border border-gray-200 p-2 dark:border-gray-600">
      <DayPicker value={r.days} onChange={(days) => onChange({ ...r, days })} />
      <div className="mt-2 flex items-center gap-2">
        <label className="text-xs text-gray-700 dark:text-gray-300">
          From
          <input
            aria-label="Start time"
            type="time"
            className={inputClass + ' w-28'}
            value={r.start}
            onChange={(e) => onChange({ ...r, start: e.target.value })}
          />
        </label>
        <label className="text-xs text-gray-700 dark:text-gray-300">
          To
          <input
            aria-label="End time"
            type="time"
            disabled={midnight}
            className={inputClass + ' w-28'}
            value={midnight ? '' : r.end}
            onChange={(e) => onChange({ ...r, end: e.target.value })}
          />
        </label>
        <label className="flex items-center gap-1 text-xs text-gray-700 dark:text-gray-300">
          <input type="checkbox" checked={midnight} onChange={(e) => onChange({ ...r, end: e.target.checked ? '24:00' : '23:59' })} />
          until midnight
        </label>
        {onRemove && (
          <button type="button" aria-label="Remove range" onClick={onRemove} className="ml-auto text-xs text-red-600">
            remove
          </button>
        )}
      </div>
    </div>
  )
}

export default function Schedules() {
  return (
    <ConfigList<Schedule>
      title="Schedules"
      path="schedules"
      newLabel="New schedule"
      select={(c) => c.schedules}
      empty={() => ({ name: '', ranges: [newRange()] })}
      clean={(s) => omitEmpty({ ...s, comment: s.comment?.trim(), ranges: s.ranges.map((r) => ({ ...r, days: [...r.days].sort((a, b) => DAYS.indexOf(a as (typeof DAYS)[number]) - DAYS.indexOf(b as (typeof DAYS)[number])) })) })}
      validate={(s) => validateSchedule(s)}
      columns={[
        { label: 'Week', render: (s) => <WeekStrip s={s} /> },
        { label: 'Ranges', render: (s) => s.ranges.map((r, i) => `${[...r.days].sort((a, b) => DAYS.indexOf(a as (typeof DAYS)[number]) - DAYS.indexOf(b as (typeof DAYS)[number])).map((d) => DAY_LABELS[d as keyof typeof DAY_LABELS]).join(' ')} ${r.start}–${r.end}`).join('; ') },
        { label: 'Comment', render: (s) => s.comment },
      ]}
      form={(s, set, { err }) => (
        <>
          <label className="block">
            Comment
            <input aria-label="Comment" className={inputClass} value={s.comment ?? ''} onChange={(e) => set({ comment: e.target.value })} />
          </label>
          <div className="space-y-2">
            {s.ranges.map((r, i) => (
              <div key={i}>
                <RangeRow r={r} onChange={(nr) => set({ ranges: s.ranges.map((x, j) => (j === i ? nr : x)) })} onRemove={s.ranges.length > 1 ? () => set({ ranges: s.ranges.filter((_, j) => j !== i) }) : undefined} />
                {err(`ranges[${i}].days`)}
                {err(`ranges[${i}].start`)}
                {err(`ranges[${i}].end`)}
              </div>
            ))}
          </div>
          <button type="button" className="text-sm text-blue-600 hover:underline" onClick={() => set({ ranges: [...s.ranges, newRange()] })}>
            + Add range
          </button>
          {err('ranges')}
        </>
      )}
    />
  )
}
