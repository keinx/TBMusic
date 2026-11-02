export function queryString(value: unknown): string {
  if (typeof value === 'string') return value
  if (Array.isArray(value)) return value.find((v): v is string => typeof v === 'string') || ''
  return ''
}
