import { api, sessionVersion } from '../api'
import type { SongEntry } from '../types'

export async function loadLibrarySongs(url: string): Promise<SongEntry[]> {
  const generation = sessionVersion()
  const entries: SongEntry[] = []
  let cursor = 0
  do {
    const path = cursor ? `${url}${url.includes('?') ? '&' : '?'}before=${cursor}` : url
    const data = await api.get(path)
    if (generation !== sessionVersion()) throw new DOMException('账户已变更', 'AbortError')
    entries.push(...(data.list || []))
    if (!data.hasMore) return entries
    if (!Number.isSafeInteger(data.nextCursor) || data.nextCursor <= 0 || (cursor && data.nextCursor >= cursor)) {
      throw new Error('列表分页数据异常，请重试')
    }
    cursor = data.nextCursor
  } while (cursor)
  return entries
}
