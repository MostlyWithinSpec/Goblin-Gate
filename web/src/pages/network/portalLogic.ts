// Pure logic of Network → Captive portal: the settings form (numbers stay
// strings while typed), the checks the server makes too, the logo, and the
// printable voucher list.
import type { Candidate } from '../../api/objects'
import type { CaptivePortal, PortalMode, VoucherBatch } from '../../api/portal'

export const portalWikiURL = 'https://github.com/MostlyWithinSpec/Goblin-Gate/wiki/Captive-Portal'
export const maxLogoBytes = 128 * 1024

export interface PortalForm {
  interface: string
  enabled: boolean
  mode: PortalMode
  password: string
  session: string
  idle: string
  rate: string
  title: string
  text: string
  logo: string
}

export function portalFormOf(p?: CaptivePortal, iface = ''): PortalForm {
  return {
    interface: p?.interface ?? iface,
    enabled: p?.enabled ?? true,
    mode: p?.mode ?? 'click',
    password: p?.password ?? '',
    session: p?.session_minutes ? String(p.session_minutes) : '',
    idle: p?.idle_minutes ? String(p.idle_minutes) : '',
    rate: p?.rate_limit_mbps ? String(p.rate_limit_mbps) : '',
    title: p?.title ?? '',
    text: p?.text ?? '',
    logo: p?.logo ?? '',
  }
}

const num = (s: string) => (s.trim() ? Number(s.trim()) : 0)

export function buildPortal(f: PortalForm): CaptivePortal {
  const out: CaptivePortal = { interface: f.interface, enabled: f.enabled, mode: f.mode }
  // an empty password field keeps the stored one (secrets come back empty)
  if (f.mode === 'password' && f.password) out.password = f.password
  if (num(f.session)) out.session_minutes = num(f.session)
  if (num(f.idle)) out.idle_minutes = num(f.idle)
  if (num(f.rate)) out.rate_limit_mbps = num(f.rate)
  if (f.title.trim()) out.title = f.title.trim()
  if (f.text.trim()) out.text = f.text.trim()
  if (f.logo) out.logo = f.logo
  return out
}

export type PortalErrors = Partial<Record<'password' | 'session' | 'idle' | 'rate' | 'title' | 'text', string>>

const intIn = (s: string, lo: number, hi: number) => !s.trim() || (/^\d+$/.test(s.trim()) && num(s) >= lo && num(s) <= hi)

// validatePortal: hadPassword says the stored entry has one (redacted to "").
export function validatePortal(f: PortalForm, hadPassword: boolean): PortalErrors {
  const e: PortalErrors = {}
  if (f.mode === 'password' && !(f.password.length >= 4 || (hadPassword && f.password === ''))) e.password = 'password'
  if (!intIn(f.session, 5, 43200)) e.session = 'session'
  if (!intIn(f.idle, 5, 1440)) e.idle = 'idle'
  if (!intIn(f.rate, 1, 10000)) e.rate = 'rate'
  if ([...f.title].length > 80) e.title = 'title'
  if ([...f.text].length > 4000) e.text = 'text'
  return e
}

// portalInterfaces: the interfaces and VLANs a portal can guard (not WAN),
// with whether they have a static IPv4 address.
export function portalInterfaces(cfg?: Candidate): { name: string; static: boolean }[] {
  const wan = new Set((cfg?.interfaces ?? []).filter((i) => i.role === 'wan').map((i) => i.name))
  const isStatic = (a?: string) => !!a && /^\d+\.\d+\.\d+\.\d+\//.test(a)
  return [
    ...(cfg?.interfaces ?? []).filter((i) => !wan.has(i.name)).map((i) => ({ name: i.name, static: isStatic(i.address) })),
    ...(cfg?.vlans ?? []).filter((v) => !wan.has(v.parent)).map((v) => ({ name: v.name, static: isStatic(v.address) })),
  ]
}

const logoTypes = ['image/png', 'image/jpeg', 'image/webp', 'image/svg+xml']

// readLogo turns a picked file into a data: URI, or says why not.
export function readLogo(file: File): Promise<{ uri?: string; error?: 'type' | 'size' }> {
  if (!logoTypes.includes(file.type)) return Promise.resolve({ error: 'type' })
  if (file.size > maxLogoBytes) return Promise.resolve({ error: 'size' })
  return new Promise((resolve) => {
    const r = new FileReader()
    r.onload = () => resolve({ uri: String(r.result) })
    r.onerror = () => resolve({ error: 'type' })
    r.readAsDataURL(file)
  })
}

export function formatLeft(seconds: number): string {
  if (seconds <= 0) return '0 min'
  const m = Math.ceil(seconds / 60)
  if (m < 120) return `${m} min`
  const h = Math.floor(m / 60)
  if (h < 48) return `${h} h ${m % 60} min`
  return `${Math.floor(h / 24)} d ${h % 24} h`
}

export function formatMinutes(m: number): string {
  if (m % 1440 === 0) return `${m / 1440} d`
  if (m % 60 === 0) return `${m / 60} h`
  return `${m} min`
}

const esc = (s: string) => s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c] as string)

// printableBatch is a plain HTML page with the batch's usable codes, one
// card per code, for printing and cutting.
export function printableBatch(b: VoucherBatch, labels: { title: string; valid: string; uses: string }): string {
  const codes = b.codes.filter((c) => c.state === 'unused' || c.state === 'in_use')
  const cards = codes
    .map((c) => `<div class="c"><div class="t">${esc(labels.title)}</div><div class="k">${esc(c.code)}</div><div class="m">${esc(labels.valid)} · ${esc(labels.uses)}</div></div>`)
    .join('')
  return `<!doctype html><html><head><meta charset="utf-8"><title>${esc(labels.title)}</title><style>
body{font-family:system-ui,sans-serif;margin:1cm}.g{display:grid;grid-template-columns:repeat(3,1fr);gap:4mm}
.c{border:1px dashed #999;padding:4mm;text-align:center;break-inside:avoid}.t{font-size:10pt}.k{font:bold 16pt monospace;margin:2mm 0}.m{font-size:8pt;color:#555}
</style></head><body><div class="g">${cards}</div></body></html>`
}
