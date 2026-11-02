export function coverURL(raw?: string): string {
  if (typeof raw !== 'string' || !raw) return ''
  if (raw.startsWith('/api/cover?')) return raw
  if (!/^https?:\/\//i.test(raw)) return ''
  return '/api/cover?url=' + encodeURIComponent(raw)
}
