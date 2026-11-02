import { safeStorage } from '../utils/safeStorage'
import { defineStore } from 'pinia'
import { ref, computed, watch } from 'vue'
import { api } from '../api'
import { songKey, Song, SourcesResp, SourceInfo } from '../types'
import { useLibrary } from './library'
import { preferDirect, rememberDirect } from '../utils/directPlayback'
import { coverURL } from '../utils/cover'

export type PlayMode = 'loop' | 'one' | 'random'

function lsGet(key: string, def: string): string {
  return safeStorage.getItem(key) ?? def
}

/* ---------- 持久化数据的形状校验 ---------- */

const strOrNull = (v: unknown, max: number): string | null =>
  typeof v === 'string' && v.length <= max ? v : null

// normalizeSong 把 localStorage 里的任意值收敛成合法 Song;不合法返回 null。
// 队列会被直接渲染(歌名/歌手/时长/歌单键),字段类型错一个就可能白屏。
function normalizeSong(x: unknown): Song | null {
  if (!x || typeof x !== 'object') return null
  const o = x as Record<string, unknown>
  const source = strOrNull(o.source, 16)
  const name = strOrNull(o.name, 200)
  if (!source || !name || source === '' || name === '') return null
  const pick = (k: string, max: number) => strOrNull(o[k], max) ?? ''
  const qualities = Array.isArray(o.qualities)
    ? o.qualities
        .filter((q): q is string => typeof q === 'string' && q.length > 0 && q.length <= 16)
        .slice(0, 8)
    : []
  return {
    source,
    name,
    singer: pick('singer', 200),
    songmid: pick('songmid', 128),
    songId: pick('songId', 128),
    albumId: pick('albumId', 128) || undefined,
    albumMid: pick('albumMid', 128) || undefined,
    albumName: pick('albumName', 200) || undefined,
    hash: pick('hash', 128) || undefined,
    copyrightId: pick('copyrightId', 128) || undefined,
    interval: pick('interval', 16),
    pic: pick('pic', 512) || undefined,
    lrcUrl: pick('lrcUrl', 512) || undefined,
    qualities: qualities.length ? qualities : undefined,
  }
}

export const usePlayer = defineStore('player', () => {
  const library = useLibrary()

  const proxyAudio = new Audio()
  const directAudio = new Audio()
  let audio = proxyAudio
  proxyAudio.preload = directAudio.preload = 'auto'

  // ---- 状态 ----
  const queue = ref<Song[]>([])
  const index = ref(-1)
  const playing = ref(false)
  const currentTime = ref(0)
  const duration = ref(0)
  const initialVolume = Number(lsGet('tbmusic_volume', '1'))
  const volume = ref(Number.isFinite(initialVolume) ? Math.min(1, Math.max(0, initialVolume)) : 1)
  const muted = ref(lsGet('tbmusic_muted', '0') === '1')
  const initialMode = lsGet('tbmusic_mode', 'loop')
  const mode = ref<PlayMode>(initialMode === 'one' || initialMode === 'random' ? initialMode : 'loop')
  const qualityPref = ref(lsGet('tbmusic_quality', '320k'))
  const urlLoading = ref(false)
  const urlError = ref('')
  const overlayOpen = ref(false)
  const currentQuality = ref('')
  const playbackRoute = ref<'direct' | 'proxy' | ''>('')
  const sources = ref<Record<string, SourceInfo>>({})


  const current = computed<Song | null>(() => queue.value[index.value] ?? null)

  // ---- 源能力 ----
  async function loadSources() {
    try {
      const d: SourcesResp = await api.get('/api/sources')
      sources.value = d.sources || {}
    } catch { /* ignore */ }
  }

  function supportedQualities(song: Song | null): string[] {
    if (!song) return []
    const q = sources.value[song.source]?.qualitys || []
    return q.length ? q : song.qualities || ['128k']
  }

  // Separate audio elements keep cross-origin playback out of Web Audio's
  // analysis graph; otherwise sources without CORS can become completely silent.
  for (const element of [proxyAudio, directAudio]) {
    element.addEventListener('timeupdate', () => { if (element === audio) currentTime.value = audio.currentTime })
    element.addEventListener('durationchange', () => { if (element === audio) duration.value = audio.duration || 0 })
    element.addEventListener('play', () => { if (element === audio) playing.value = true })
    element.addEventListener('playing', () => {
      if (element !== audio || !attempt || attempt.seq !== requestSeq) return
      clearDirectTimer()
      playing.value = true
      resumeListening()
      if (playbackRoute.value === 'direct') rememberDirect(attempt.song.source, attempt.directURL, 'direct')
      // Only a working proxy proves this was a direct-compatibility failure.
      // A dead link or an offline device must not blacklist the whole CDN.
      else if (attempt.directFailed) rememberDirect(attempt.song.source, attempt.directURL, 'proxy')
    })
    element.addEventListener('pause', () => { if (element === audio && audio.paused) { stopListening(); playing.value = false; clearDirectTimer() } })
    element.addEventListener('ended', () => { if (element === audio) { stopListening(); reportListening(); onEnded() } })
    for (const event of ['waiting', 'stalled']) element.addEventListener(event, () => {
      if (element === audio && !audio.paused) { stopListening(); armDirectTimer() }
    })
    element.addEventListener('seeking', () => { if (element === audio) stopListening() })
    element.addEventListener('seeked', () => { if (element === audio && !audio.paused && audio.readyState >= 3) resumeListening() })
    element.addEventListener('error', () => {
      if (element !== audio || !audio.src) return
      stopListening()
      if (playbackRoute.value === 'direct' && attempt) { fallbackToProxy(); return }
      if (!urlLoading.value) retryOnce()
    })
    element.volume = volume.value
    element.muted = muted.value
  }

  function retryOnce() {
    if (retryCount.value >= 1) {
      urlError.value = '播放失败'
      pauseAudio()
      autoSkip()
      return
    }
    retryCount.value++
    loadCurrent({ seekTo: audio.currentTime, preserveListening: true }) // 断流重试保持进度
  }
  // ---- Web Audio 频谱分析(懒初始化,供播放页可视化) ----
  let audioCtx: any = null
  let analyser: AnalyserNode | null = null
  let spectrumBuf: Uint8Array | null = null

  function ensureAnalyser() {
    if (audio !== proxyAudio) return
    if (audioCtx) {
      if (audioCtx.state === 'suspended') audioCtx.resume().catch(() => {})
      return
    }
    try {
      const AC = (window as any).AudioContext || (window as any).webkitAudioContext
      if (!AC) return
      audioCtx = new AC()
      const source = audioCtx.createMediaElementSource(proxyAudio)
      analyser = audioCtx.createAnalyser()
      analyser.fftSize = 128
      analyser.smoothingTimeConstant = 0.72
      source.connect(analyser)
      analyser.connect(audioCtx.destination)
      spectrumBuf = new Uint8Array(analyser.frequencyBinCount)
    } catch {
      audioCtx = null
      analyser = null
    }
  }

  // 归一化频谱能量(0~1),bins 为返回的频段数量
  function getSpectrum(bins = 40): number[] {
    if (audio !== proxyAudio || !analyser || !spectrumBuf) return []
    analyser.getByteFrequencyData(spectrumBuf as any)
    const n = Math.min(bins, spectrumBuf.length)
    const out: number[] = new Array(n)
    for (let i = 0; i < n; i++) out[i] = spectrumBuf[i] / 255
    return out
  }

  // ---- 播放状态持久化:刷新后恢复队列与播放位置 ----
  let accountId: number | null = null
  const stateKey = () => `tbmusic_player_state:${accountId}`
  const progressKey = () => `tbmusic_player_progress:${accountId}`
  const savedPosition = ref(0) // 待恢复的播放位置(秒)
  let lastPersist = 0

  function persistState() {
    if (accountId === null) return
    try {
      if (!queue.value.length) {
        safeStorage.removeItem(stateKey())
        safeStorage.removeItem(progressKey())
        return
      }
      // 未加载 src 时(如刷新后尚未点播放)保留待恢复的位置,避免被覆写为 0
      const t = audio.src ? Math.floor(audio.currentTime) : savedPosition.value
      safeStorage.setItem(stateKey(), JSON.stringify({
        q: queue.value,
        i: index.value,
        t: Math.floor(t),
      }))
      safeStorage.setItem(progressKey(), JSON.stringify({ k: current.value ? songKey(current.value) : '', t: Math.floor(t) }))
    } catch { /* 存储满等异常忽略 */ }
  }

  // 进度独立小 key,每 5s 写一次(几十字节,不再全量序列化队列)
  function persistProgressOnly() {
    if (accountId === null) return
    try {
      if (!audio.src) return
      if (!current.value) return
      safeStorage.setItem(progressKey(), JSON.stringify({ k: songKey(current.value), t: Math.floor(audio.currentTime) }))
    } catch { /* ignore */ }
  }

  function restoreState() {
    try {
      const raw = safeStorage.getItem(stateKey())
      if (!raw) return
      const st = JSON.parse(raw)
      let progress: { k?: string; t?: number } | null = null
      try { progress = JSON.parse(safeStorage.getItem(progressKey()) || 'null') } catch { /* 进度损坏仍恢复队列 */ }
      // 元素形状校验:被污染/旧版本的 localStorage 不得进入渲染(防白屏、防 NaN 进度)
      const validQ = Array.isArray(st.q)
        ? st.q.map(normalizeSong).filter((x: Song | null): x is Song => x !== null)
        : []
      const i = typeof st.i === 'number' && Number.isInteger(st.i) ? st.i : 0
      // 位置合法性:非有限值/负数/超过 24h 的一律丢弃,避免复位到异常位置
      const savedTime = validQ[i] && progress?.k === songKey(validQ[i]) ? progress.t : st.t
      const t = typeof savedTime === 'number' && Number.isFinite(savedTime) && savedTime >= 0 && savedTime < 86400 ? Math.floor(savedTime) : 0
      if (validQ.length && i >= 0 && i < validQ.length) {
        queue.value = validQ
        index.value = i
        savedPosition.value = t
      }
    } catch { /* ignore */ }
  }

  // deep watch 在千首队列下会频繁全量序列化,合并为 400ms 一次;关页前仍立即落盘
  let persistTimer = 0
  function persistSoon() {
    if (persistTimer) return
    persistTimer = window.setTimeout(() => {
      persistTimer = 0
      persistState()
    }, 400)
  }
  watch([queue, index], () => persistSoon(), { deep: true })
  for (const element of [proxyAudio, directAudio]) element.addEventListener('timeupdate', () => {
    if (element !== audio) return
    const now = Date.now()
    if (now - lastPersist > 5000) {
      lastPersist = now
      persistProgressOnly() // 进度小对象,不再每 2s 全量序列化队列
    }
  })
  window.addEventListener('beforeunload', () => {
    if (persistTimer) { window.clearTimeout(persistTimer); persistTimer = 0 }
    persistState()
  })

  // ---- 取链播放 ----
  let requestSeq = 0
  let playbackRequest: AbortController | null = null

  function cancelPlaybackRequest() {
    stopListening()
    requestSeq++
    attempt = null
    clearDirectTimer()
    playbackRoute.value = ''
    playbackRequest?.abort()
    playbackRequest = null
    urlLoading.value = false
  }
  let historyTimer = 0
  let listenSong: Song | null = null
  let listenReported = false
  let listenElapsed = 0
  let listenStarted: number | null = null
  function stopListening() {
    if (historyTimer) window.clearTimeout(historyTimer)
    historyTimer = 0
    if (listenStarted !== null) listenElapsed += Math.max(0, performance.now() - listenStarted)
    listenStarted = null
  }
  function resetListening(song: Song | null) {
    stopListening()
    listenSong = song
    listenElapsed = 0
    listenReported = false
  }
  function reportListening() {
    if (!listenReported && listenSong && listenElapsed >= 5000) {
      listenReported = true
      void library.reportPlay(listenSong)
    }
  }
  function resumeListening() {
    if (!listenSong || listenReported || listenStarted !== null || audio.paused) return
    listenStarted = performance.now()
    historyTimer = window.setTimeout(() => {
      stopListening()
      if (!audio.paused && !audio.seeking && audio.readyState >= 3) {
        reportListening()
        if (!listenReported) resumeListening() // Timers can fire slightly before the deadline.
      }
    }, Math.max(1, Math.ceil(5000 - listenElapsed)))
  }
  const retryCount = ref(0)

  interface PlaybackAttempt {
    seq: number
    song: Song
    directURL: string
    proxyURL: string
    directFailed: boolean
    wantPlay: boolean
    seekTo: number
  }
  let attempt: PlaybackAttempt | null = null
  let playWhenReady = true
  let pendingSeekTo = 0
  let directTimer = 0
  const DIRECT_TIMEOUT = 7000
  function clearDirectTimer() {
    if (directTimer) window.clearTimeout(directTimer)
    directTimer = 0
  }
  function armDirectTimer() {
    if (playbackRoute.value !== 'direct' || !attempt?.wantPlay || directTimer) return
    const active = attempt
    directTimer = window.setTimeout(() => {
      directTimer = 0
      if (attempt === active && active.seq === requestSeq) fallbackToProxy()
    }, DIRECT_TIMEOUT)
  }
  function playAudio() {
    const active = attempt
    const target = audio
    if (active) active.wantPlay = true
    armDirectTimer()
    target.play().catch((error) => {
      if (target !== audio || active !== attempt) return
      if (error?.name === 'NotAllowedError') {
        if (active) active.wantPlay = false
        clearDirectTimer()
        playing.value = false
        return // Autoplay policy is not an incompatible source.
      }
      if (error?.name !== 'AbortError' && playbackRoute.value === 'direct') fallbackToProxy()
    })
  }
  function pauseAudio() {
    if (attempt) attempt.wantPlay = false
    clearDirectTimer()
    audio.pause()
  }
  function startStream(active: PlaybackAttempt, route: 'direct' | 'proxy', seekTo: number) {
    clearDirectTimer()
    audio.pause()
    audio.removeAttribute('src')
    audio.load()
    audio = route === 'direct' ? directAudio : proxyAudio
    playbackRoute.value = route
    audio.volume = volume.value
    audio.muted = muted.value
    currentTime.value = seekTo
    duration.value = 0
    ensureAnalyser()
    const target = audio
    target.src = route === 'direct' ? active.directURL : active.proxyURL
    if (seekTo > 0) target.addEventListener('loadedmetadata', () => {
      if (attempt === active && active.seq === requestSeq && target === audio) {
        try { target.currentTime = seekTo } catch { /* metadata not seekable */ }
      }
    }, { once: true })
    if (active.wantPlay) playAudio()
  }
  function fallbackToProxy() {
    const active = attempt
    if (!active || active.seq !== requestSeq || playbackRoute.value !== 'direct') return
    active.directFailed = true
    const position = audio.currentTime > 0 ? audio.currentTime : active.seekTo
    startStream(active, 'proxy', position)
  }

  async function loadCurrent(opts?: { seekTo?: number; wantPlay?: boolean; preserveListening?: boolean }) {
    const song = current.value
    if (!song) return
    if (library.isPlaybackBlocked(song)) { purgeBlacklisted(library.blacklist.map(e => e.musicInfo)); return }
    cancelPlaybackRequest()
    if (!opts?.preserveListening || !listenSong) resetListening(song)
    playWhenReady = opts?.wantPlay ?? true
    const controller = new AbortController()
    playbackRequest = controller
    urlLoading.value = true
    urlError.value = ''
    // 先停掉旧音频:否则取链失败时上一首会继续播放,
    // 界面显示的是新歌、耳朵听到的是旧歌(典型的"点 A 播 B")
    audio.pause()
    audio.removeAttribute('src')
    audio.load() // 释放旧音频连接,让收藏等小请求不必等旧音频结束
    // 注意:这里不清 retryCount——retryOnce 依赖计数跨次累加,否则断流时无限重试
    const seq = ++requestSeq
    const seekTo = opts?.seekTo ?? savedPosition.value
    pendingSeekTo = seekTo
    savedPosition.value = 0
    try {
      const d = await api.post('/api/music-url', {
        source: song.source,
        quality: qualityPref.value,
        musicInfo: song,
      }, { signal: controller.signal })
      if (seq !== requestSeq) return // 已切到别的歌
      currentQuality.value = d.quality
      attempt = {
        seq, song, directURL: d.url || '', proxyURL: d.stream,
        directFailed: false, wantPlay: playWhenReady, seekTo,
      }
      startStream(attempt, preferDirect(song.source, d.url || '') ? 'direct' : 'proxy', seekTo)
      // 封面由 MiniPlayer 单一入口加载并写回歌曲,播放页直接复用。
    } catch (e: any) {
      if (seq !== requestSeq || controller.signal.aborted) return
      urlError.value = e?.message || '获取播放地址失败'
      // 限流 / 未登录 / 脚本未就绪:属于"暂时取不到",不是这首歌没资源,
      // 绝不能自动跳到别的歌(否则会连带播出不相干的曲子)。
      const status = e?.status
      if (status === 429 || status === 401 || status === 503) return
      // 所有音源都没有该歌曲的资源:停留并提示,由用户决定切歌
      if (e?.code === 'no_source' || e?.code === 'host_blocked') return
      autoSkip()
    } finally {
      if (seq === requestSeq) {
        urlLoading.value = false
        playbackRequest = null
      }
    }
  }

  let skipFailures = 0
  function autoSkip() {
    if (queue.value.length <= 1) return
    if (++skipFailures >= Math.min(queue.value.length, 5)) { // 连续失败过多,停止
      skipFailures = 0
      playing.value = false
      return
    }
    next(true)
  }

  function onEnded() {
    if (mode.value === 'one') {
      resetListening(current.value)
      audio.currentTime = 0
      ensureAnalyser()
      playAudio()
      return
    }
    next()
  }

  // ---- 队列控制 ----
  function filterBlack(list: Song[]): Song[] {
    try {
      return list.filter((s) => !library.isPlaybackBlocked(s))
    } catch {
      return list
    }
  }

  function playAll(list: Song[], start = 0) {
    retryCount.value = 0 // 主动切歌重置断流重试计数
    savedPosition.value = 0 // 主动切歌不再沿用刷新前的续播位置
    const filtered = filterBlack(list)
    if (!filtered.length) return
    // start 是调用方按原列表传入的行号:映射为过滤后列表的下标(前面被拉黑剔除的歌会让位)
    const anchor = list[Math.min(Math.max(start, 0), list.length - 1)]
    let idx = anchor ? filtered.findIndex((x) => songKey(x) === songKey(anchor)) : 0
    if (idx < 0) idx = 0
    queue.value = filtered
    index.value = idx
    skipFailures = 0
    loadCurrent()
  }

  // playSong 播放单曲:只把这一首追加到播放列表并开始播放,
  // 不清空、不替换已有的播放列表(旧实现会把所在列表整张塞进队列)。
  // 若这首歌已在列表里,直接跳过去播,避免列表里出现重复条目。
  // 只有显式点击「播放全部」才允许替换队列。
  function playSong(song: Song) {
    if (!song || library.isPlaybackBlocked(song)) return
    if (current.value && songKey(current.value) === songKey(song) && (playing.value || urlLoading.value)) return
    retryCount.value = 0 // 主动切歌重置断流重试计数
    savedPosition.value = 0
    skipFailures = 0
    const k = songKey(song)
    const i = queue.value.findIndex((x) => songKey(x) === k)
    if (i >= 0) {
      index.value = i
      loadCurrent()
      return
    }
    queue.value.push(song)
    index.value = queue.value.length - 1
    loadCurrent()
  }

  function nextPlayableIndex(from: number, step: 1 | -1): number {
    const n = queue.value.length
    const allowed = queue.value.map((s, i) => library.isPlaybackBlocked(s) ? -1 : i).filter(i => i >= 0)
    if (!allowed.length) return -1
    if (mode.value === 'random' && step === 1) {
      const others = allowed.filter(i => i !== from)
      const choices = others.length ? others : allowed
      return choices[Math.floor(Math.random() * choices.length)]
    }
    for (let offset = 1; offset <= n; offset++) {
      const i = (from + step * offset + n) % n
      if (!library.isPlaybackBlocked(queue.value[i])) return i
    }
    return -1
  }

  function next(auto = false) {
    if (!queue.value.length) return
    const nextIndex = nextPlayableIndex(index.value, 1)
    if (nextIndex < 0) { clearQueue(false); return }
    index.value = nextIndex
    retryCount.value = 0
    if (!auto) { skipFailures = 0; savedPosition.value = 0 }
    loadCurrent()
  }

  function prev() {
    if (audio.currentTime > 3) { // 播放超过 3 秒先回到开头
      audio.currentTime = 0
      return
    }
    if (!queue.value.length) return
    savedPosition.value = 0
    retryCount.value = 0
    const prevIndex = nextPlayableIndex(index.value, -1)
    if (prevIndex < 0) { clearQueue(false); return }
    index.value = prevIndex
    skipFailures = 0
    loadCurrent()
  }

  function removeAt(i: number) {
    const wasCurrent = i === index.value
    queue.value.splice(i, 1)
    if (i < index.value) index.value--
    else if (wasCurrent) {
      if (index.value >= queue.value.length) index.value = queue.value.length - 1
      if (index.value >= 0) { retryCount.value = 0; loadCurrent() }
      else {
        // 队列被清空:停掉音频,避免"孤儿播放"
        cancelPlaybackRequest()
        audio.pause()
        audio.removeAttribute('src')
        audio.load()
        playing.value = false
      }
    }
  }

  function jumpTo(i: number) {
    retryCount.value = 0 // 主动切歌重置断流重试计数
    if (i < 0 || i >= queue.value.length) return
    savedPosition.value = 0
    index.value = i
    skipFailures = 0
    loadCurrent()
  }

  // 拉黑联动:把已拉黑的歌从队列剔除(含当前曲,自动切下一首)
  function purgeBlacklisted(black: Song[]) {
    if (!queue.value.length || !black.length) return
    const resume = playing.value || urlLoading.value
    const keys = new Set(black.map(keyOf))
    let removedCurrent = false
    for (let i = queue.value.length - 1; i >= 0; i--) {
      if (keys.has(keyOf(queue.value[i]))) {
        queue.value.splice(i, 1)
        if (i === index.value) removedCurrent = true
        else if (i < index.value) index.value--
      }
    }
    if (removedCurrent) {
      if (index.value >= queue.value.length) index.value = queue.value.length - 1
      savedPosition.value = 0
      cancelPlaybackRequest()
      audio.pause(); audio.removeAttribute('src'); audio.load()
      currentTime.value = 0; duration.value = 0
      if (index.value >= 0 && resume) { skipFailures = 0; retryCount.value = 0; loadCurrent() }
      else { cancelPlaybackRequest(); audio.pause(); audio.removeAttribute('src'); audio.load(); playing.value = false }
    }
  }

  const keyOf = songKey // 统一口径(含 copyrightId 兜底)

  // ---- 队列辅助:加入/移除(不切歌) ----
  // 列表每行都会调用;预建索引避免一次按钮重绘触发「行数 × 队列长度」次扫描。
  const queueKeys = computed(() => new Set(queue.value.map(songKey)))
  function isQueued(s: Song): boolean { return queueKeys.value.has(songKey(s)) }

  function addToQueue(s: Song): boolean {
    if (!s || library.isPlaybackBlocked(s) || isQueued(s)) return false
    queue.value.push(s)
    if (index.value < 0) {
      index.value = 0
      loadCurrent()
    }
    return true
  }

  function removeFromQueue(s: Song): boolean {
    const k = songKey(s)
    const i = queue.value.findIndex((x) => songKey(x) === k)
    if (i < 0) return false
    if (i === index.value) return false // 当前播放曲不可移除
    removeAt(i)
    return true
  }

  function clearQueue(keepCurrent = true) {
    if (keepCurrent && current.value) {
      queue.value = [current.value]
      index.value = 0
    } else {
      cancelPlaybackRequest()
      queue.value = []
      index.value = -1
      audio.pause()
      audio.removeAttribute('src')
      audio.load()
    }
  }

  function setAccount(id: number | null) {
    if (id === accountId) return
    persistState()
    if (persistTimer) window.clearTimeout(persistTimer)
    persistTimer = 0
    resetListening(null)
    clearQueue(false)
    for (const element of [proxyAudio, directAudio]) {
      element.pause(); element.removeAttribute('src'); element.load()
    }
    playing.value = false
    currentTime.value = duration.value = savedPosition.value = 0
    currentQuality.value = urlError.value = ''
    overlayOpen.value = false
    skipFailures = retryCount.value = 0
    accountId = id
    // Legacy unscoped queues cannot safely be attributed to a signed-in account.
    safeStorage.removeItem('tbmusic_player_state')
    safeStorage.removeItem('tbmusic_player_progress')
    if (id !== null) restoreState()
    updateMediaSession(current.value, current.value?.pic)
  }

  watch(() => library.blacklist.map(e => e.musicInfo), purgeBlacklisted, { flush: 'sync' })

  // ---- 播放控制 ----
  function toggle() {
    if (!current.value) return
    if (urlLoading.value) { playWhenReady = !playWhenReady; return }
    if (audio.paused) {
      ensureAnalyser()
      if (!audio.src) {
        loadCurrent() // 恢复场景:取链并跳到上次位置
      } else {
        playAudio()
      }
    } else {
      pauseAudio()
    }
  }
  // seek 跳转:必须过滤 NaN/Infinity 并夹到 [0, duration]。
  // duration 在 metadata 未就绪时为 NaN,直接赋给 currentTime 会抛
  // InvalidStateError/TypeError(事件回调里抛错不会被上层捕获)。
  function seek(t: number) {
    if (!Number.isFinite(t)) return
    let v = Math.max(0, t)
    const dur = audio.duration
    if (Number.isFinite(dur) && dur > 0) v = Math.min(v, dur)
    try {
      audio.currentTime = v
    } catch { /* 元数据尚未就绪:忽略本次跳转 */ }
  }
  function setVolume(v: number) {
    if (!Number.isFinite(v)) return
    v = Math.min(1, Math.max(0, v))
    volume.value = v
    audio.volume = v
    safeStorage.setItem('tbmusic_volume', String(v))
  }
  function setMuted(m: boolean) {
    muted.value = m
    audio.muted = m
    safeStorage.setItem('tbmusic_muted', m ? '1' : '0')
  }
  function setMode(m: PlayMode) { mode.value = m; safeStorage.setItem('tbmusic_mode', m) }
  function setQualityPref(q: string) {
    qualityPref.value = q
    safeStorage.setItem('tbmusic_quality', q)
    retryCount.value = 0
    if (current.value) loadCurrent({ seekTo: urlLoading.value ? pendingSeekTo : audio.currentTime, wantPlay: urlLoading.value ? playWhenReady : !audio.paused, preserveListening: true })
  }

  // ---- MediaSession ----
  function updateMediaSession(song: Song | null, pic?: string) {
    if (!('mediaSession' in navigator)) return
    if (!song) { navigator.mediaSession.metadata = null; return }
    try {
      navigator.mediaSession.metadata = new MediaMetadata({
        title: song.name,
        artist: song.singer,
        album: song.albumName || '',
        artwork: pic ? [{ src: new URL(coverURL(pic), window.location.origin).href, sizes: '500x500' }] : [],
      })
    } catch { /* ignore */ }
  }

  if ('mediaSession' in navigator) {
    try {
      navigator.mediaSession.setActionHandler('play', () => {
        ensureAnalyser()
        if (!audio.src) loadCurrent()
        else playAudio()
      })
      navigator.mediaSession.setActionHandler('pause', () => pauseAudio())
      navigator.mediaSession.setActionHandler('previoustrack', () => prev())
      navigator.mediaSession.setActionHandler('nexttrack', () => next())
      navigator.mediaSession.setActionHandler('seekto', (d) => { if (d.seekTime != null) seek(d.seekTime) })
    } catch { /* ignore */ }
  }

  watch(current, (s) => updateMediaSession(s, s?.pic))

  return {
    queue, index, current, playing, currentTime, duration, volume, muted,
    mode, qualityPref, urlLoading, urlError, overlayOpen, currentQuality, playbackRoute,
    sources,
    setAccount, loadSources, supportedQualities, getSpectrum,
    playAll, playSong, next, prev, toggle, seek, setVolume, setMuted, setMode,
    setQualityPref, removeAt, jumpTo, clearQueue, updateMediaSession,
    isQueued, addToQueue, removeFromQueue, purgeBlacklisted,
  }
})
