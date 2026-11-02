import { test, beforeEach, afterEach, after, mock } from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'vite'
import vuePlugin from '@vitejs/plugin-vue'
import { createRouter, createMemoryHistory } from 'vue-router'
import { createPinia, setActivePinia, disposePinia } from 'pinia'
import { nextTick, createRenderer, ssrContextKey } from 'vue'

// Exercise the real stores and API without remote music services or a browser.
const vite = await createServer({ configFile: false, define: { __APP_RELEASE__: JSON.stringify({version:'test',buildId:'test',builtAt:'2026-10-31T00:00:00Z'}) }, plugins: [vuePlugin()], optimizeDeps: { noDiscovery: true, include: [] }, server: { middlewareMode: true, ws: false }, appType: 'custom' })
const { api, ApiError, invalidateSessionRequests } = await vite.ssrLoadModule('/src/api.ts')
const originalApi = { ...api }
const { useLibrary } = await vite.ssrLoadModule('/src/stores/library.ts')
const { usePlayer } = await vite.ssrLoadModule('/src/stores/player.ts')
const { useAuth } = await vite.ssrLoadModule('/src/stores/auth.ts')
const { usePrefs } = await vite.ssrLoadModule('/src/stores/prefs.ts')
const { queryString } = await vite.ssrLoadModule('/src/utils/query.ts')
const { coverURL } = await vite.ssrLoadModule('/src/utils/cover.ts')
const { downloadSong, isDownloading } = await vite.ssrLoadModule('/src/utils/download.ts')
const { toasts } = await vite.ssrLoadModule('/src/toast.ts')
const { directRecords, preferDirect, rememberDirect, clearDirectRecords } = await vite.ssrLoadModule('/src/utils/directPlayback.ts')

const song = (id) => ({ source: 'wy', songmid: id, songId: id, name: `歌曲${id}`, singer: '测试', interval: '03:00' })
const deferred = () => {
  let resolve, reject
  const promise = new Promise((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
let pinia, storage, audios, timers
class TestAudio extends EventTarget {
  src = ''; currentTime = 0; paused = true; duration = 180; readyState = 4; seeking = false
  constructor() { super(); audios.push(this) }
  pause() { this.paused = true; this.dispatchEvent(new Event('pause')) }
  play() { this.paused = false; this.dispatchEvent(new Event('play')); return Promise.resolve() }
  load() { this.currentTime = 0 }
  removeAttribute(name) { if (name === 'src') this.src = '' }
}
beforeEach(() => {
  Object.assign(api, originalApi)
  storage = new Map(); audios = []; timers = []
  globalThis.localStorage = { getItem: (k) => storage.get(k) ?? null, setItem: (k, v) => storage.set(k, v), removeItem: (k) => storage.delete(k) }
  globalThis.Audio = TestAudio
  Object.defineProperty(globalThis, 'navigator', { configurable: true, value: {} })
  globalThis.window = {
    location: { origin: 'http://localhost:8080', protocol: 'http:' }, addEventListener() {}, clearTimeout,
    setTimeout(fn, ms) { const id = setTimeout(fn, ms); timers.push(id); return id },
  }
  pinia = createPinia(); setActivePinia(pinia)
  toasts.splice(0)
})
afterEach(async () => {
  await nextTick()
  timers.forEach(clearTimeout)
  disposePinia(pinia)
  mock.restoreAll()
})
after(() => vite.close())

test('clicking songs appends only that song, deduplicates, and cancels obsolete resolution', async () => {
  const requests = []
  mock.method(api, 'post', (_, body, options) => {
    const pending = deferred(); requests.push({ ...pending, body, signal: options.signal }); return pending.promise
  })
  const player = usePlayer()
  player.queue = [song('old')]; player.index = 0
  player.playSong(song('new'))
  assert.deepEqual(player.queue.map(s => s.songmid), ['old', 'new'])
  player.playSong(song('next'))
  assert.equal(requests[0].signal.aborted, true)
  assert.deepEqual(player.queue.map(s => s.songmid), ['old', 'new', 'next'])
  player.playSong(song('new'))
  player.playSong(song('new'))
  assert.equal(player.queue.length, 3)
  assert.equal(requests.length, 3, 'repeated click while resolving must not issue another request')
  requests[0].resolve({ quality: '128k', stream: '/old' })
  await nextTick()
  assert.equal(audios[0].src, '', 'stale response must not start playback')
  player.clearQueue(false)
  assert.equal(requests[2].signal.aborted, true)
  requests.slice(1).forEach(r => r.resolve({ quality: '128k', stream: '/late' }))
  await nextTick()
  assert.equal(audios[0].src, '')
  assert.equal(player.current, null)
  assert.equal(player.urlLoading, false)
})

test('explicit play-all still replaces the queue and filters blacklisted songs', () => {
  mock.method(api, 'post', () => new Promise(() => {}))
  const library = useLibrary(), player = usePlayer()
  library.blacklist = [{ id: 1, musicInfo: song('blocked'), addedAt: 0 }]
  player.queue = [song('old')]
  player.playAll([song('blocked'), song('a'), song('b')], 2)
  assert.deepEqual(player.queue.map(s => s.songmid), ['a', 'b'])
  assert.equal(player.current.songmid, 'b')
  player.playSong(song('blocked'))
  assert.equal(player.queue.length, 2)
  player.clearQueue(false)
})

test('favorite changes immediately on slow requests, deduplicates, and rolls back on failure', async () => {
  const library = useLibrary(), pending = deferred(), s = song('a')
  library.playlists = [{ id: 1, isDefault: true, count: 0 }]
  const post = mock.method(api, 'post', () => pending.promise)
  const result = library.toggleFavorite(s)
  assert.equal(library.isFavorited(s), true)
  assert.equal(library.isPending('fav', s), true)
  assert.equal(await library.toggleFavorite(s), 'busy')
  assert.equal(await library.toggleBlacklist(s), 'busy')
  assert.equal(post.mock.callCount(), 1)
  pending.reject(new Error('offline'))
  assert.equal(await result, 'error')
  assert.equal(library.isFavorited(s), false)
  assert.equal(library.isPending('fav', s), false)
})

test('favorite removal uses cached row ID in one request and restores the heart on failure', async () => {
  const library = useLibrary(), s = song('a')
  library.playlists = [{ id: 1, isDefault: true, count: 0 }]
  mock.method(api, 'post', async () => ({ id: 42, added: true }))
  assert.equal(await library.toggleFavorite(s), 'added')
  const pending = deferred()
  const del = mock.method(api, 'del', () => pending.promise)
  const result = library.toggleFavorite(s)
  assert.equal(library.isFavorited(s), false)
  assert.deepEqual(del.mock.calls[0].arguments, ['/api/playlists/1/songs/42'])
  pending.reject(new Error('offline'))
  assert.equal(await result, 'error')
  assert.equal(library.isFavorited(s), true)
})

test('blacklist changes immediately and restores prior state on failed add/remove', async () => {
  const library = useLibrary(), s = song('b'), pending = deferred()
  mock.method(api, 'post', () => pending.promise)
  const result = library.toggleBlacklist(s)
  assert.equal(library.isBlacklisted(s), true)
  assert.equal(await library.toggleBlacklist(s), 'busy')
  pending.reject(new Error('offline'))
  assert.equal(await result, 'error')
  assert.equal(library.isBlacklisted(s), false)
  library.blacklist = [{ id: 8, musicInfo: s, addedAt: 100 }]
  const removal = deferred()
  mock.method(api, 'del', () => removal.promise)
  const removed = library.toggleBlacklist(s)
  assert.equal(library.isBlacklisted(s), false)
  removal.reject(new Error('offline'))
  assert.equal(await removed, 'error')
  assert.equal(library.blacklist[0].id, 8)
})

test('API timeout covers a stalled response body and external cancellation', async () => {
  mock.method(globalThis, 'fetch', async (_, { signal }) => ({
    status: 200, ok: true,
    json: () => new Promise((_, reject) => signal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))),
  }))
  await assert.rejects(api.post('/api/test', {}, { timeoutMs: 20 }), error => error instanceof ApiError && error.status === 408)
  const ctl = new AbortController()
  const request = api.post('/api/test', {}, { signal: ctl.signal })
  await Promise.resolve(); ctl.abort()
  await assert.rejects(request, { name: 'AbortError' })
})

test('download gives immediate feedback and hands the stream to the browser without buffering', async () => {
  const pending = deferred(), s = song('下载')
  mock.method(api, 'post', () => pending.promise)
  let clicked = false
  const anchor = { click() { clicked = true }, remove() {} }
  globalThis.document = { createElement: () => anchor, body: { appendChild() {} } }
  const fetchMock = mock.method(globalThis, 'fetch', () => { throw new Error('Audio must not be fetched into JS memory') })
  const downloading = downloadSong(s)
  assert.equal(isDownloading(s), true)
  assert.match(toasts[0].text, /正在准备下载/)
  pending.resolve({ stream: '/api/stream?source=wy&url=https%3A%2F%2Fexample.com%2Fsong.flac' })
  await downloading
  assert.equal(clicked, true)
  assert.equal(new URL(anchor.href).searchParams.get('download'), s.name)
  assert.equal(fetchMock.mock.callCount(), 0)
  assert.equal(isDownloading(s), false)
  assert.match(toasts[0].text, /已交给浏览器下载/)
})

test('progress updates write a small record instead of serializing a large queue', async () => {
  const player = usePlayer()
  player.setAccount(1)
  player.queue = Array.from({ length: 1000 }, (_, i) => song(String(i)))
  player.index = 999
  audios[0].src = '/stream'; audios[0].currentTime = 42
  audios[0].dispatchEvent(new Event('timeupdate'))
  const progress = storage.get('tbmusic_player_progress:1')
  assert.ok(progress.length < 100)
  assert.deepEqual(JSON.parse(progress), { k: 'wy:999', t: 42 })
  assert.equal(player.isQueued(song('999')), true)
  player.removeAt(0)
  assert.equal(player.isQueued(song('0')), false)
  player.clearQueue(false)
})

test('a damaged progress record does not discard the existing saved queue', () => {
  storage.set('tbmusic_player_state:1', JSON.stringify({ q: [song('saved')], i: 0, t: 12 }))
  storage.set('tbmusic_player_progress:1', '{broken')
  const player = usePlayer()
  assert.equal(player.current, null, 'must not restore before account verification')
  player.setAccount(1)
  assert.equal(player.current.songmid, 'saved')
  player.clearQueue(false)
})

test('initial favorite refresh arriving after a click cannot overwrite the optimistic state', async () => {
  const library = useLibrary(), initial = deferred(), saving = deferred(), s = song('new')
  let songReads = 0
  mock.method(api, 'get', async url => {
    if (url === '/api/playlists') return { list: [{ id: 1, isDefault: true, count: 1 }] }
    songReads++
    return songReads === 1 ? initial.promise : { list: [{ id: 1, musicInfo: song('old') }, { id: 2, musicInfo: s }] }
  })
  mock.method(api, 'post', () => saving.promise)
  const loading = library.loadPlaylists()
  await Promise.resolve(); await Promise.resolve()
  const adding = library.toggleFavorite(s)
  initial.resolve({ list: [{ id: 1, musicInfo: song('old') }] })
  await loading
  assert.equal(library.isFavorited(s), true)
  saving.resolve({ id: 2, added: true })
  await adding; await Promise.resolve(); await Promise.resolve()
  assert.equal(library.isFavorited(song('old')), true)
  assert.equal(library.isFavorited(s), true)
})

const resolvedDirect = { url: 'https://audio.example.com/a.mp3', stream: '/api/stream?url=a', quality: '320k' }

test('direct audio stays out of the analysis element and records only actual successful playback', async () => {
  mock.method(api, 'post', async () => resolvedDirect)
  const player = usePlayer()
  player.playSong(song('direct')); await nextTick()
  assert.equal(player.playbackRoute, 'direct')
  assert.equal(audios[1].src, resolvedDirect.url)
  assert.equal(audios[0].src, '')
  assert.deepEqual(directRecords(), [])
  audios[1].dispatchEvent(new Event('playing'))
  assert.equal(directRecords()[0].mode, 'direct')
  assert.deepEqual(player.getSpectrum(), [])
  player.clearQueue(false)
})

test('direct error falls back at the current position; proven incompatibility skips the next trial', async () => {
  mock.method(api, 'post', async () => resolvedDirect)
  const player = usePlayer()
  player.playSong(song('first')); await nextTick()
  audios[1].currentTime = 37
  audios[1].dispatchEvent(new Event('error'))
  assert.equal(player.playbackRoute, 'proxy')
  assert.equal(audios[0].src, resolvedDirect.stream)
  audios[0].dispatchEvent(new Event('loadedmetadata'))
  assert.equal(audios[0].currentTime, 37)
  assert.deepEqual(directRecords(), [], 'a proxy that also fails must not classify the CDN')
  audios[0].dispatchEvent(new Event('playing'))
  assert.equal(directRecords()[0].mode, 'proxy')
  player.playSong(song('second')); await nextTick()
  assert.equal(player.playbackRoute, 'proxy')
  assert.equal(audios[1].src, '')
  assert.equal(preferDirect('wy', 'https://other.example.com/b.mp3'), true)
  player.clearQueue(false)
})

test('a stalled direct connection falls back once and expired compatibility records retry direct', async () => {
  let timeout
  window.setTimeout = (fn, ms) => { if (ms === 7000) timeout = fn; return 123 }
  mock.method(api, 'post', async () => resolvedDirect)
  const player = usePlayer()
  player.playSong(song('slow')); await nextTick()
  assert.equal(typeof timeout, 'function')
  timeout()
  assert.equal(player.playbackRoute, 'proxy')
  audios[0].dispatchEvent(new Event('playing'))
  const saved = JSON.parse(storage.get('tbmusic_direct_compat_v1'))
  saved[0].expires = Date.now() - 1
  storage.set('tbmusic_direct_compat_v1', JSON.stringify(saved))
  assert.equal(preferDirect('wy', resolvedDirect.url), true)
  player.clearQueue(false)
})

test('autoplay denial, paused playback, and late events do not poison compatibility records', async () => {
  mock.method(api, 'post', async () => resolvedDirect)
  const player = usePlayer()
  mock.method(audios[1], 'play', () => Promise.reject(new DOMException('gesture needed', 'NotAllowedError')))
  player.playSong(song('denied')); await nextTick(); await Promise.resolve()
  assert.equal(player.playbackRoute, 'direct')
  assert.deepEqual(directRecords(), [])
  player.clearQueue(false)
  audios[1].dispatchEvent(new Event('playing'))
  assert.deepEqual(directRecords(), [])
})

test('direct compatibility is isolated by source and origin and HTTPS pages never try HTTP audio', () => {
  rememberDirect('wy', resolvedDirect.url, 'proxy')
  assert.equal(preferDirect('wy', resolvedDirect.url), false)
  assert.equal(preferDirect('tx', resolvedDirect.url), true)
  window.location.protocol = 'https:'
  assert.equal(preferDirect('wy', 'http://audio.example.com/a'), false)
  assert.equal(preferDirect('wy', 'javascript:alert(1)'), false)
  assert.equal(preferDirect('wy', 'https://user:pass@audio.example.com/a'), false)
})

test('clearing connection records retries direct without changing playback or other settings', async () => {
  rememberDirect('wy', resolvedDirect.url, 'proxy')
  storage.set('tbmusic_theme', 'dark')
  mock.method(api, 'post', async () => resolvedDirect)
  const player = usePlayer()
  player.playSong(song('keep-playing')); await nextTick()
  assert.equal(player.playbackRoute, 'proxy')
  clearDirectRecords()
  assert.deepEqual(directRecords(), [])
  assert.equal(preferDirect('wy', resolvedDirect.url), true)
  assert.equal(player.current.songmid, 'keep-playing')
  assert.equal(audios[0].src, resolvedDirect.stream)
  assert.equal(storage.get('tbmusic_theme'), 'dark')
  player.clearQueue(false)
})


test('account changes cancel playback and isolate queues, favorites, history and pending responses', async () => {
  const auth = useAuth(), library = useLibrary(), player = usePlayer()
  const pending = deferred()
  mock.method(api, 'post', async (url) => url === '/api/auth/login' ? { user: {id: 1, username: 'one'} } : {id: 42, added: true})
  await auth.login('one', 'secret')
  player.queue = [song('one')]; player.index = 0
  library.history = [{id:1,musicInfo:song('one')}]
  library.playlists = [{id:1,isDefault:true,count:0}]
  await library.toggleFavorite(song('one'))
  mock.method(api, 'get', () => pending.promise)
  const loading = library.loadHistory()
  mock.method(api, 'post', async () => ({ user: {id: 2, username: 'two'} }))
  await auth.login('two', 'secret')
  assert.equal(player.current, null)
  assert.equal(library.history.length, 0)
  assert.equal(library.playlists.length, 0)
  assert.equal(library.isFavorited(song('one')), false)
  pending.resolve({list:[{id:1,musicInfo:song('one')}]})
  await assert.rejects(loading, {name:'AbortError'})
  assert.equal(library.history.length, 0)
  player.queue = [song('two')]; player.index = 0
  player.setAccount(1)
  assert.equal(player.current.songmid, 'one')
  assert.equal(JSON.parse(storage.get('tbmusic_player_state:2')).q[0].songmid, 'two')
  player.clearQueue(false)
})

test('failed logout retains the account and successful logout stops audio', async () => {
  const auth=useAuth(), player=usePlayer()
  mock.method(api, 'post', async () => ({user:{id:1,username:'one'}}))
  mock.method(api, 'get', async () => ({}))
  await auth.login('one','secret')
  player.queue=[song('one')]; player.index=0
  audios[0].src='/audio'; await audios[0].play()
  mock.method(api,'post',async () => {throw new ApiError('database unavailable',503)})
  await assert.rejects(auth.logout(), /database unavailable/)
  assert.equal(auth.user.id,1)
  assert.equal(player.current.songmid,'one')
  mock.method(api,'post',async () => ({ok:true}))
  await auth.logout()
  assert.equal(auth.user,null)
  assert.equal(player.current,null)
  assert.ok(audios.every(a=>a.paused && !a.src))
})

test('revoked API generations cannot dispatch a late 401 against a new account', async () => {
  const pending=deferred(), auth=useAuth()
  auth.user={id:2,username:'two'}
  mock.method(globalThis,'fetch',()=>pending.promise)
  const request=api.get('/api/history')
  invalidateSessionRequests()
  pending.resolve({status:401,ok:false,json:async()=>({error:'old request'})})
  await assert.rejects(request,{name:'AbortError'})
  assert.equal(auth.user.id,2)
})

test('library follows all pages and direct default-playlist removal clears the favorite row', async () => {
  const library=useLibrary()
  mock.method(api,'get',async url=>{
    if(url==='/api/playlists') return {list:[{id:1,isDefault:true,count:1200}]}
    if(url.includes('before=')) return {list:[{id:1,musicInfo:song('oldest')}],hasMore:false}
    return {list:Array.from({length:1199},(_,i)=>({id:1200-i,musicInfo:song(String(i))})),hasMore:true,nextCursor:2}
  })
  await library.loadPlaylists()
  assert.equal(library.isFavorited(song('oldest')),true)
  const del=mock.method(api,'del',async()=>({ok:true}))
  await library.removeFromPlaylist(1,1)
  assert.equal(library.isFavorited(song('oldest')),false)
  mock.method(api,'post',async()=>({id:1300,added:true}))
  await library.toggleFavorite(song('oldest'))
  await library.toggleFavorite(song('oldest'))
  assert.equal(del.mock.calls[1].arguments[0],'/api/playlists/1/songs/1300')
})

test('queue additions and persisted playback obey the blacklist without autoplaying after restore', () => {
  const library=useLibrary(), player=usePlayer()
  const post=mock.method(api,'post',()=>new Promise(()=>{}))
  storage.set('tbmusic_player_state:1',JSON.stringify({q:[song('blocked'),song('ok')],i:0,t:20}))
  player.setAccount(1)
  library.blacklist=[{id:1,musicInfo:song('blocked'),addedAt:0}]
  assert.equal(player.addToQueue(song('blocked')),false)
  assert.deepEqual(player.queue.map(s=>s.songmid),['ok'])
  assert.equal(post.mock.callCount(),0,'loading blacklist must not start paused playback')
  player.clearQueue(false)
})

test('unavailable browser storage and malformed query parameters do not crash initialization', () => {
  globalThis.localStorage={ getItem(){throw new Error('blocked')}, setItem(){throw new Error('blocked')}, removeItem(){throw new Error('blocked')} }
  window.matchMedia=()=>({matches:false,addEventListener(){}})
  globalThis.document={documentElement:{setAttribute(){}},querySelector:()=>({})}
  globalThis.getComputedStyle=()=>({getPropertyValue:()=>''})
  const player=usePlayer(), prefs=usePrefs()
  player.setAccount(1); player.setVolume(999); player.setVolume(NaN); player.setMode('random')
  prefs.setTheme('dark')
  assert.equal(player.volume,1)
  assert.equal(queryString(['value',null]),'value')
  assert.equal(queryString([null,'fallback']),'fallback')
  assert.equal(queryString(null),'')
  assert.equal(coverURL(['https://8.8.8.8/a','bad']),'')
})


// Mount the actual setup/lifecycle code with a renderer that needs no browser DOM.
const renderer=createRenderer({
  createElement:()=>({}),createText:()=>({}),createComment:()=>({}),
  insert(){},remove(){},setElementText(){},setText(){},patchProp(){},
  parentNode:()=>null,nextSibling:()=>null,
})
const flush=()=>new Promise(resolve=>setImmediate(resolve))
async function mountLogic(path,url) {
  const component=(await vite.ssrLoadModule(path)).default
  const router=createRouter({history:createMemoryHistory(),routes:['/playlists/:id','/detail/:type/:source/:id','/:pathMatch(.*)*'].map(path=>({path,component:{render:()=>null}}))})
  await router.push(url); await router.isReady()
  const app=renderer.createApp({...component,render:()=>null})
  app.provide(ssrContextKey, {})
  app.use(pinia); app.use(router)
  const vm=app.mount({})
  await flush()
  return {state:vm.$.setupState,app,router}
}

test('detail pagination and play-all include songs beyond the first 100',async()=>{
  const seen=[]
  mock.method(api,'get',async url=>{
    const p=Number(new URL(url,'http://localhost').searchParams.get('page'))
    seen.push(p)
    return {list:Array.from({length:p===3?51:100},(_,i)=>song(String((p-1)*100+i))),total:251,hasMore:p<3}
  })
  mock.method(api,'post',()=>new Promise(()=>{}))
  const {state,app}=await mountLogic('/src/views/DetailView.vue','/detail/songlist/wy/list?name=first&name=second&pic=a&pic=b')
  try {
    assert.equal(state.title,'first'); assert.equal(state.pic,'a')
    assert.equal(state.songs.length,100)
    await state.loadMore()
    assert.equal(state.songs.length,200)
    await state.playAll()
    assert.deepEqual(seen,[1,2,3])
    assert.equal(usePlayer().queue.length,251)
    usePlayer().clearQueue(false)
    state.page=100; state.hasMore=true
    await state.playAll()
    assert.deepEqual(seen,[1,2,3], 'server page cap must not trigger an infinite fetch loop')
    assert.equal(usePlayer().queue.length,0)
  } finally {app.unmount()}
})

test('settings retries have independent timers and refreshes preserve unsaved drafts',async()=>{
  const scheduled=new Map(); let timerId=0
  window.setTimeout=(fn,ms)=>{const id=++timerId; scheduled.set(id,{fn,ms});return id}
  window.clearTimeout=id=>scheduled.delete(id)
  const settings={scripts:[{name:'one',url:'https://example.com/one.js'},{name:'two',url:'https://example.com/two.js'}],allowRegister:true,inviteRequired:false,inviteCode:'saved',streamHosts:['audio.example.com']}
  mock.method(api,'get',async url=>{
    if(url==='/api/admin/settings') return structuredClone(settings)
    if(url==='/api/admin/cache') return {limit:1024**2}
    return {sources:{}}
  })
  mock.method(api,'post',async()=>({ok:true}))
  mock.method(api,'put',async()=>({ok:true}))
  const {state,app}=await mountLogic('/src/views/SystemSettingsView.vue','/system')
  try {
    await state.retryScript(state.scripts[0]); await state.retryScript(state.scripts[1])
    assert.equal([...scheduled.values()].filter(t=>t.ms===8000).length,2)
    await state.save()
    assert.equal([...scheduled.values()].filter(t=>t.ms===8000).length,2,'save must not cancel retries')
    state.inviteCode='unsaved'; state.allowRegister=false; state.scripts[0].name='draft'
    settings.scripts[0].ready=true; settings.scripts[1].ready=true
    for(const [id,timer] of [...scheduled]) {scheduled.delete(id); await timer.fn()}
    assert.equal(state.retrying[settings.scripts[0].url],false)
    assert.equal(state.retrying[settings.scripts[1].url],false)
    assert.equal(state.inviteCode,'unsaved'); assert.equal(state.allowRegister,false)
    assert.equal(state.scripts[0].name,'draft'); assert.equal(state.scripts[0].ready,true)
  } finally {app.unmount()}
})

test('login credentials are cleared after sign-in and stay cleared after logout',async()=>{
  mock.method(api,'post',async url=>url.endsWith('/login')?{user:{id:51,username:'listener',isAdmin:false}}:{ok:true})
  mock.method(api,'get',async()=>({allowRegister:true}))
  const auth=useAuth();auth.loaded=true
  const {state,app}=await mountLogic('/src/components/LoginOverlay.vue','/')
  try {
    state.username='listener';state.password='private-password';state.inviteCode='private-invite'
    await state.submit();await nextTick()
    assert.equal(state.password,'');assert.equal(state.inviteCode,'')
    await auth.logout();await nextTick()
    assert.equal(state.password,'');assert.equal(auth.loggedIn,false)
  } finally {app.unmount()}
})

test('cross-tab session notification clears old state and reloads current account',async()=>{
  const listeners=new Map()
  window.addEventListener=(name,fn)=>listeners.set(name,fn)
  window.removeEventListener=(name)=>listeners.delete(name)
  let actual={id:61,username:'alice',isAdmin:false}
  mock.method(api,'post',async()=>({user:actual}))
  mock.method(api,'get',async url=>url==='/api/auth/me'?{user:actual}:{allowRegister:true})
  const auth=useAuth();await auth.login('alice','password');auth.loaded=true
  const player=usePlayer();player.queue=[song('private')];player.index=0
  actual={id:62,username:'bob',isAdmin:false}
  listeners.get('storage')({key:'tbmusic_auth_epoch'})
  assert.equal(auth.user,null);assert.equal(player.queue.length,0)
  await flush();assert.equal(auth.user.id,62)
})

test('API binds writes to the displayed account and resyncs on account mismatch',async()=>{
  mock.method(api,'post',async()=>({user:{id:71,username:'alice',isAdmin:false}}))
  const auth=useAuth();await auth.login('alice','password');auth.loaded=true
  const seen=[]
  mock.method(globalThis,'fetch',async(url,opts)=>{
    seen.push([url,opts.headers])
    if(url==='/api/history')return {status:409,ok:false,json:async()=>({code:'account_changed',error:'账户已变更'})}
    return {status:200,ok:true,json:async()=>url==='/api/auth/me'?{user:{id:72,username:'bob',isAdmin:false}}:{allowRegister:true}}
  })
  await assert.rejects(originalApi.post('/api/history',{}),e=>e instanceof ApiError&&e.code==='account_changed')
  assert.equal(seen[0][1]['X-TBMusic-User-ID'],'71')
  await flush();assert.equal(auth.user.id,72)
})

test('playlist transfer keeps its original source when navigation occurs during save',async()=>{
  const s=song('same'),saved=deferred(),removed=[]
  mock.method(api,'get',async url=>({list:[{id:url.includes('/1/')?11:33,musicInfo:s}]}))
  const library=useLibrary()
  mock.method(library,'addToPlaylist',()=>saved.promise)
  mock.method(library,'removeFromPlaylist',async(id,row)=>{removed.push([id,row])})
  const {state,app,router}=await mountLogic('/src/views/PlaylistDetailView.vue','/playlists/1')
  try {
    state.transferTarget=s
    const moving=state.onMove(2)
    await router.push('/playlists/3');await flush()
    saved.resolve(true);await moving
    assert.deepEqual(removed,[[1,11]])
    assert.equal(state.entries[0].id,33)
  } finally {app.unmount()}
})

test('failed blacklist changes leave playing audio and queue intact; success purges',async()=>{
  const library=useLibrary(),player=usePlayer(),pending=deferred(),s=song('playing')
  player.queue=[s];player.index=0;audios[0].src='/stream';audios[0].currentTime=42;await audios[0].play()
  mock.method(api,'post',()=>pending.promise)
  const result=library.toggleBlacklist(s)
  assert.equal(library.isBlacklisted(s),true)
  assert.equal(player.queue.length,1);assert.equal(audios[0].paused,false)
  pending.reject(new Error('offline'));assert.equal(await result,'error')
  assert.equal(player.queue.length,1);assert.equal(audios[0].currentTime,42);assert.equal(audios[0].paused,false)
  mock.method(api,'post',async()=>({id:91,createdAt:123}))
  assert.equal(await library.toggleBlacklist(s),'added')
  assert.equal(player.queue.length,0);assert.equal(audios[0].paused,true)
})

test('delayed blacklist refresh cannot undo a completed direct removal',async()=>{
  const library=useLibrary(),pending=deferred(),entry={id:7,musicInfo:song('stale'),addedAt:0}
  library.blacklist=[entry]
  mock.method(api,'get',()=>pending.promise);mock.method(api,'del',async()=>({ok:true}))
  const loading=library.loadBlacklist();await library.removeBlacklist(7)
  pending.resolve({list:[entry]});await loading
  assert.equal(library.blacklist.length,0)
})

test('brief playing events cannot reset the one-retry budget',async()=>{
  const requests=mock.method(api,'post',async()=>({stream:'/api/stream?url=broken',quality:'320k'}))
  const player=usePlayer();player.playSong(song('broken'));await nextTick();await nextTick()
  for(let i=0;i<3;i++){
    audios[0].dispatchEvent(new Event('playing'));audios[0].dispatchEvent(new Event('error'))
    await nextTick();await nextTick()
  }
  assert.equal(requests.mock.callCount(),2);assert.equal(audios[0].paused,true)
  player.clearQueue(false)
})

test('repeated quality changes preserve pause and position while resolution is pending',async()=>{
  const pending=[]
  mock.method(api,'post',()=>{const d=deferred();pending.push(d);return d.promise})
  const player=usePlayer();player.queue=[song('paused')];player.index=0;audios[0].src='/old';audios[0].currentTime=42
  player.setQualityPref('128k');player.setQualityPref('320k')
  pending.forEach(p=>p.resolve({stream:'/api/stream?url=a',quality:'320k'}));await nextTick();await nextTick()
  audios[0].dispatchEvent(new Event('loadedmetadata'))
  assert.equal(audios[0].paused,true);assert.equal(player.playing,false);assert.equal(audios[0].currentTime,42)
  player.clearQueue(false)
})

test('search does not request or append pages beyond the server cap',async()=>{
  mock.method(api,'get',async url=>url==='/api/sources'?{sources:{kw:{name:'酷我',searchTypes:['music']}}}:{list:[song('page')],page:Number(new URL(url,'http://localhost').searchParams.get('page')),hasMore:true})
  const {state,app}=await mountLogic('/src/views/SearchView.vue','/search?keyword=test')
  try {
    state.page=100;await state.search(false)
    const count=state.songs.length
    assert.equal(state.hasMore,false)
    await state.loadMore();assert.equal(state.page,100);assert.equal(state.songs.length,count)
  } finally {app.unmount()}
})

test('listening counts actual playback, excludes pauses, and counts repeat plays without quality duplicates',async()=>{
  let now=0, timerID=0, reports=0
  const scheduled=new Map()
  mock.method(performance,'now',()=>now)
  window.setTimeout=(fn,ms)=>{const id=++timerID;scheduled.set(id,{fn,due:now+ms});return id}
  window.clearTimeout=id=>scheduled.delete(id)
  const advance=async ms=>{now+=ms;for(const [id,t] of [...scheduled])if(t.due<=now&&scheduled.has(id)){scheduled.delete(id);t.fn()}await flush()}
  mock.method(api,'post',async url=>url==='/api/history'?{listenCount:++reports}:{quality:'320k',stream:'/stream'})
  const player=usePlayer(),library=useLibrary(),a=audios[0]
  player.playSong(song('count'));await flush()
  await advance(10000);assert.equal(reports,0,'resolved URL alone is not a listen')
  a.dispatchEvent(new Event('playing'));await advance(2000);a.pause()
  await advance(60000);assert.equal(reports,0,'pause must not count')
  await a.play();a.dispatchEvent(new Event('playing'));await advance(3000)
  assert.equal(reports,1);assert.equal(library.listenCount,1)
  player.setQualityPref('128k');await flush();a.dispatchEvent(new Event('playing'));await advance(6000)
  assert.equal(reports,1,'quality changes must not double count')
  player.mode='one';a.dispatchEvent(new Event('ended'));a.dispatchEvent(new Event('playing'));await advance(5000)
  assert.equal(reports,2,'replaying the same song must count')
  player.clearQueue(false)
})

test('listening stats reject stale accounts and never decrease on delayed reads or history clearing',async()=>{
  const library=useLibrary(),pending=deferred()
  mock.method(api,'get',()=>pending.promise)
  const loading=library.loadListenCount()
  mock.method(api,'post',async()=>({listenCount:12}))
  await library.reportPlay(song('a'))
  pending.resolve({user:{listenCount:10}});await loading
  assert.equal(library.listenCount,12)
  mock.method(api,'del',async()=>({ok:true}));await library.clearHistory()
  assert.equal(library.listenCount,12)
  const late=deferred();mock.method(api,'post',()=>late.promise)
  const reporting=library.reportPlay(song('old'))
  library.reset();late.resolve({listenCount:99});await reporting
  assert.equal(library.listenCount,null)
})

test('account settings and admin users load cumulative listening stats',async()=>{
  window.matchMedia=()=>({matches:false,addEventListener(){}})
  globalThis.document={documentElement:{setAttribute(){}},querySelector:()=>({})}
  globalThis.getComputedStyle=()=>({getPropertyValue:()=>''})
  mock.method(api,'get',async url=>url==='/api/admin/users'?{list:[{id:1,username:'listener',listenCount:1234}]}:url==='/api/auth/me'?{user:{listenCount:1234}}:{sources:{}})
  const settings=await mountLogic('/src/views/SettingsView.vue','/settings')
  assert.equal(useLibrary().listenCount,1234);settings.app.unmount()
  const users=await mountLogic('/src/views/UsersView.vue','/users')
  assert.equal(users.state.users[0].listenCount,1234);users.app.unmount()
})
