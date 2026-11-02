import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { api } from '../api'
import { loadLibrarySongs } from '../utils/libraryPages'
import { Song, Playlist, SongEntry, songKey } from '../types'

// 收藏歌单 / 拉黑 / 历史
export const useLibrary = defineStore('library', () => {
  const playlists = ref<Playlist[]>([])
  const blacklist = ref<SongEntry[]>([])
  const blackOverrides = ref<Map<string, boolean>>(new Map())
  const blacklistKeys = computed(() => new Set(blacklist.value.map((e) => songKey(e.musicInfo))))
  const favKeys = ref<Set<string>>(new Set())
  // 默认歌单里 key -> 行 id:取消收藏可直接 DELETE,不必先拉整张歌单
  const favRows = ref<Map<string, number>>(new Map())
  const history = ref<SongEntry[]>([])
  const listenCount = ref<number | null>(null)
  let favRevision = 0
  let blackRevision = 0
  let playlistsRequest: Promise<void> | null = null
  let favRefreshNeeded = false

  // 进行中的写操作("fav:key" / "ban:key")。
  // 连点按钮时第二次点击直接忽略:否则每个点击都会发一个请求,
  // 拥堵时多个响应会一起回来,刷出一串重复的"操作成功"。
  const pending = ref<Set<string>>(new Set())

  let generation = 0
  function reset() {
    generation++
    favRevision++
    blackRevision++
    playlists.value = []
    blacklist.value = []
    blackOverrides.value = new Map()
    history.value = []
    listenCount.value = null
    favKeys.value = new Set()
    favRows.value = new Map()
    pending.value = new Set()
    playlistsRequest = null
    favRefreshNeeded = false
  }
  async function scoped<T>(promise: Promise<T>): Promise<T> {
    const gen = generation
    const value = await promise
    if (gen !== generation) throw new DOMException('账户已变更', 'AbortError')
    return value
  }

  function isPending(action: 'fav' | 'ban', s: Song): boolean {
    return pending.value.has(`${action}:${songKey(s)}`)
  }

  function isBlacklisted(s: Song): boolean {
    const key = songKey(s)
    return blackOverrides.value.get(key) ?? blacklistKeys.value.has(key)
  }
  function isPlaybackBlocked(s: Song): boolean { return blacklistKeys.value.has(songKey(s)) }
  function isFavorited(s: Song): boolean {
    return favKeys.value.has(songKey(s))
  }

  const defaultPlaylistId = computed(() => playlists.value.find((p) => p.isDefault)?.id ?? playlists.value[0]?.id ?? 0)

  // 一键收藏到默认歌单(无弹窗)
  async function addToDefault(song: Song): Promise<'added' | 'duplicated' | 'error'> {
    const key = songKey(song)
    try {
      if (!defaultPlaylistId.value) await scoped(loadPlaylists())
      const id = defaultPlaylistId.value
      if (!id) return 'error'
      const d = await scoped(api.post(`/api/playlists/${id}/songs`, { musicInfo: song }))
      if (d.id) favRows.value.set(key, d.id) // 记下行 id,取消收藏时一次 DELETE 搞定
      if (d.added) {
        favKeys.value.add(key)
        const p = playlists.value.find((x) => x.id === id)
        if (p) p.count++
        return 'added'
      }
      // 重复添加说明本就在默认歌单里,同步红心态
      favKeys.value.add(key)
      return 'duplicated'
    } catch { return 'error' }
  }

  // 从默认歌单取消收藏(按歌曲匹配行 id 删除);不在默认歌单时返回 false
  async function removeFromDefault(song: Song): Promise<boolean> {
    const id = defaultPlaylistId.value
    if (!id) return false
    const key = songKey(song)
    try {
      let rowId = favRows.value.get(key)
      if (!rowId) {
        // 本地没有行 id(刚打开页面还没拉过收藏):查一次并顺手补全缓存
        const list = await scoped(loadLibrarySongs(`/api/playlists/${id}/songs`))
        for (const e of list) favRows.value.set(songKey(e.musicInfo), e.id)
        rowId = favRows.value.get(key)
      }
      if (!rowId) {
        // 服务端确实没有这条(本地状态偏了):纠正红心并算作已取消,不报错
        favKeys.value.delete(key)
        return true
      }
      await scoped(api.del(`/api/playlists/${id}/songs/${rowId}`))
      favRows.value.delete(key)
      const p = playlists.value.find((x) => x.id === id)
      if (p) p.count = Math.max(0, p.count - 1)
      return true
    } catch { return false }
  }

  // 收藏切换:已收藏则取消,未收藏则收藏。
  // 乐观更新:先翻转红心再发请求,点击即时可见;失败回滚。
  async function toggleFavorite(song: Song): Promise<'added' | 'removed' | 'busy' | 'error'> {
    const gen = generation
    if (isPending('fav', song) || isPending('ban', song)) return 'busy'
    favRevision++
    pending.value.add('fav:' + songKey(song))
    const key = songKey(song)
    const wasFav = favKeys.value.has(key)
    if (wasFav) favKeys.value.delete(key)
    else favKeys.value.add(key)
    try {
      if (wasFav) {
        const removed = await removeFromDefault(song)
        if (gen !== generation) return 'error'
        if (!removed) {
          favKeys.value.add(key) // 回滚
          return 'error'
        }
        return 'removed'
      }
      const r = await addToDefault(song)
      if (gen !== generation) return 'error'
      if (r === 'error') {
        favKeys.value.delete(key) // 回滚
        return 'error'
      }
      return 'added'
    } finally {
      if (gen === generation) {
        favRevision++
        pending.value.delete('fav:' + key)
        if (favRefreshNeeded) void refreshFavKeys()
      }
    }
  }

  function loadPlaylists(): Promise<void> {
    if (playlistsRequest) return playlistsRequest
    const gen = generation
    playlistsRequest = (async () => {
      const d = await scoped(api.get('/api/playlists'))
      playlists.value = d.list || []
      await refreshFavKeys()
    })().finally(() => { if (gen === generation) playlistsRequest = null })
    return playlistsRequest
  }

  async function refreshFavKeys() {
    // 初始化请求晚于点击返回时,不能把乐观红心覆盖回旧值。
    if (pending.value.size) { favRefreshNeeded = true; return }
    favRefreshNeeded = false
    const revision = favRevision
    const id = defaultPlaylistId.value
    if (!id) {
      favKeys.value = new Set()
      favRows.value = new Map()
      return
    }
    // 歌单为空(如新用户)时无需拉取全量歌曲
    const meta = playlists.value.find((p) => p.id === id)
    if (meta && meta.count === 0) {
      favKeys.value = new Set()
      favRows.value = new Map()
      return
    }
    try {
      const list = await scoped(loadLibrarySongs(`/api/playlists/${id}/songs`))
      if (revision !== favRevision) {
        favRefreshNeeded = true
        if (!pending.value.size) void refreshFavKeys()
        return
      }
      favKeys.value = new Set(list.map((e) => songKey(e.musicInfo)))
      favRows.value = new Map(list.map((e) => [songKey(e.musicInfo), e.id]))
    } catch { /* 忽略,保留现有状态 */ }
  }

  async function createPlaylist(name: string): Promise<Playlist | null> {
    const d = await scoped(api.post('/api/playlists', { name }))
    if (d.playlist) playlists.value.push(d.playlist)
    return d.playlist || null
  }

  async function renamePlaylist(id: number, name: string) {
    await scoped(api.patch(`/api/playlists/${id}`, { name }))
    const p = playlists.value.find((x) => x.id === id)
    if (p) p.name = name
  }

  async function deletePlaylist(id: number) {
    await scoped(api.del(`/api/playlists/${id}`))
    playlists.value = playlists.value.filter((p) => p.id !== id)
  }

  async function addToPlaylist(playlistId: number, song: Song): Promise<boolean> {
    const d = await scoped(api.post(`/api/playlists/${playlistId}/songs`, { musicInfo: song }))
    const p = playlists.value.find((x) => x.id === playlistId)
    if (p && d.added) p.count++
    // 红心态只表达"默认歌单收藏",其他歌单不点亮红心
    if (playlistId === defaultPlaylistId.value) {
      favKeys.value.add(songKey(song))
      if (d.id) favRows.value.set(songKey(song), d.id) // 供取消收藏一次删除
    }
    return !!d.added
  }

  async function removeFromPlaylist(playlistId: number, rowId: number) {
    await scoped(api.del(`/api/playlists/${playlistId}/songs/${rowId}`))
    const p = playlists.value.find((x) => x.id === playlistId)
    if (p) p.count = Math.max(0, p.count - 1)
    if (playlistId === defaultPlaylistId.value) {
      favRevision++
      for (const [key, id] of favRows.value) {
        if (id === rowId) { favRows.value.delete(key); favKeys.value.delete(key) }
      }
    }
  }

  async function loadBlacklist() {
    const revision = blackRevision
    const list = await scoped(loadLibrarySongs('/api/blacklist'))
    if (revision === blackRevision) blacklist.value = list
  }

  // 拉黑:服务端返回行 id/加入时间,直接本地插入条目,不再全量拉一次黑名单
  async function addBlacklist(song: Song): Promise<boolean> {
    blackRevision++
    favRevision++
    const wasFav = isFavorited(song)
    const d = await scoped(api.post('/api/blacklist', { musicInfo: song }))
    if (!Number.isSafeInteger(d.id) || d.id <= 0) throw new Error('黑名单响应无效，请刷新后重试')
    blackRevision++
    favRevision++
    // 服务端已联动把歌曲移出收藏歌单;本地增量维护红心与计数,避免全量重建
    if (wasFav) {
      favKeys.value.delete(songKey(song))
      favRows.value.delete(songKey(song))
      const def = playlists.value.find((x) => x.isDefault)
      if (def) def.count = Math.max(0, def.count - 1)
    }
    const key = songKey(song)
    // Only confirmed server rows belong to the list that drives playback.
    const idx = blacklist.value.findIndex((e) => songKey(e.musicInfo) === key)
    const entry: SongEntry = { id: d.id, musicInfo: song, addedAt: d.createdAt || 0 }
    if (idx >= 0) blacklist.value[idx] = entry
    else blacklist.value.unshift(entry)
    return true
  }

  async function removeBlacklist(rowId: number) {
    const gen = generation
    blackRevision++
    try {
      await scoped(api.del(`/api/blacklist/${rowId}`))
      blacklist.value = blacklist.value.filter((e) => e.id !== rowId)
    } finally { if (gen === generation) blackRevision++ }
  }

  // Icons update optimistically; the committed list drives destructive queue
  // changes only after the server confirms the operation.
  async function toggleBlacklist(song: Song): Promise<'added' | 'removed' | 'busy' | 'error'> {
    const gen = generation, key = songKey(song)
    if (isPending('ban', song) || isPending('fav', song)) return 'busy'
    const wasBlack = blacklistKeys.value.has(key)
    blackRevision++
    favRevision++
    pending.value.add('ban:' + key)
    blackOverrides.value.set(key, !wasBlack)
    try {
      if (!wasBlack) {
        await scoped(addBlacklist(song))
        return 'added'
      }
      const entry = blacklist.value.find(e => songKey(e.musicInfo) === key)
      if (entry) await removeBlacklist(entry.id)
      return 'removed'
    } catch { return 'error' }
    finally {
      if (gen === generation) {
        blackRevision++
        favRevision++
        blackOverrides.value.delete(key)
        pending.value.delete('ban:' + key)
        if (favRefreshNeeded) void refreshFavKeys()
      }
    }
  }


  async function reportPlay(song: Song) {
    try {
      const d = await scoped(api.post('/api/history', { musicInfo: song }))
      updateListenCount(d.listenCount)
    } catch { /* 忽略上报失败 */ }
  }

  function updateListenCount(value: unknown) {
    if (typeof value === 'number' && Number.isSafeInteger(value) && value >= 0) {
      listenCount.value = Math.max(listenCount.value ?? 0, value)
    }
  }

  async function loadListenCount() {
    const d = await scoped(api.get('/api/auth/me'))
    updateListenCount(d.user?.listenCount)
  }

  async function loadHistory() {
    const d = await scoped(api.get('/api/history?limit=500'))
    history.value = d.list || []
  }

  async function clearHistory() {
    await scoped(api.del('/api/history'))
    history.value = []
  }

  return {
    reset, loadHistory, playlists, blacklist, history,
    isBlacklisted, isPlaybackBlocked, isFavorited, isPending, toggleFavorite,
    loadPlaylists, createPlaylist, renamePlaylist, deletePlaylist,
    addToPlaylist, removeFromPlaylist,
    loadBlacklist, addBlacklist, removeBlacklist, toggleBlacklist,
    reportPlay, clearHistory, listenCount, loadListenCount, updateListenCount,
  }
})
