import { safeStorage } from './safeStorage'
// Compatibility is browser-local: one device's network failure must not disable
// direct playback for every user. Platforms may return several different CDNs.
const KEY = 'tbmusic_direct_compat_v1'
const DAY = 86400_000
export interface DirectRecord {
  source: string
  origin: string
  mode: 'direct' | 'proxy'
  expires: number
  checkedAt: number
}

export function directOrigin(raw: string): string {
  try {
    const u = new URL(raw)
    if (!['https:', 'http:'].includes(u.protocol) || u.username || u.password) return ''
    return u.origin
  } catch { return '' }
}

export function directRecords(now = Date.now()): DirectRecord[] {
  try {
    const list = JSON.parse(safeStorage.getItem(KEY) || '[]')
    if (!Array.isArray(list)) return []
    return list.filter((x) => x && typeof x.source === 'string' && x.source.length <= 16 &&
      typeof x.origin === 'string' && directOrigin(x.origin) === x.origin &&
      ['direct', 'proxy'].includes(x.mode) && Number.isFinite(x.checkedAt) &&
      Number.isFinite(x.expires) && x.expires > now && x.expires <= now + 7 * DAY).slice(0, 256)
  } catch { return [] }
}

export function preferDirect(source: string, raw: string): boolean {
  const origin = directOrigin(raw)
  if (!origin || (window.location.protocol === 'https:' && origin.startsWith('http:'))) return false
  return !directRecords().some((r) => r.source === source && r.origin === origin && r.mode === 'proxy')
}

export function clearDirectRecords() {
  safeStorage.removeItem(KEY)
}

export function rememberDirect(source: string, raw: string, mode: DirectRecord['mode']) {
  const origin = directOrigin(raw)
  if (!origin) return
  const now = Date.now()
  const records = directRecords(now).filter((r) => !(r.source === source && r.origin === origin))
  records.unshift({ source, origin, mode, checkedAt: now, expires: now + (mode === 'proxy' ? DAY : 7 * DAY) })
  try { safeStorage.setItem(KEY, JSON.stringify(records.slice(0, 256))) } catch { /* private mode/storage full */ }
}
