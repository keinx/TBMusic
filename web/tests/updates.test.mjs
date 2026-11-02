import { test, beforeEach, afterEach, after, mock } from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'vite'
import { createPinia, setActivePinia, disposePinia } from 'pinia'

const release = { version: '1.0.0', buildId: 'build-a', builtAt: '2026-10-31T00:00:00.000Z' }
const vite = await createServer({ configFile: false, define: { __APP_RELEASE__: JSON.stringify(release) }, optimizeDeps: { noDiscovery: true, include: [] }, server: { middlewareMode: true, ws: false }, appType: 'custom' })
const { useUpdates, currentRelease } = await vite.ssrLoadModule('/src/stores/updates.ts')
let pinia, windowTarget, doc, intervals, reloads, stop, requests, latest, now
beforeEach(() => {
  pinia = createPinia(); setActivePinia(pinia)
  intervals = new Map(); reloads = 0; requests = []; latest = { ...release }; now = Date.now()
  doc = new EventTarget(); doc.visibilityState = 'visible'
  windowTarget = new EventTarget()
  Object.assign(windowTarget, {
    location: { reload() { reloads++ } }, setTimeout, clearTimeout,
    setInterval(fn, ms) { intervals.set(ms, fn); return ms }, clearInterval(id) { intervals.delete(id) },
  })
  globalThis.window = windowTarget
  globalThis.document = doc
  mock.method(Date, 'now', () => now)
  mock.method(globalThis, 'fetch', async (url, options) => {
    requests.push({ url, options })
    return new Response(JSON.stringify(latest), { headers: { 'Content-Type': 'application/json' } })
  })
})
afterEach(() => { stop?.(); stop = undefined; disposePinia(pinia); mock.restoreAll() })
after(() => vite.close())

test('new builds with the same version prompt once and only a user action reloads', async () => {
  const updates = useUpdates()
  assert.equal(currentRelease.version, '1.0.0')
  await updates.check(true)
  assert.equal(updates.available, false)
  latest = { ...release, buildId: 'build-b' }
  const one = updates.check(true), two = updates.check(true)
  await Promise.all([one, two])
  assert.equal(requests.length, 2, 'concurrent checks share a single request')
  assert.equal(updates.available, true)
  assert.equal(updates.noticeVisible, true)
  assert.equal(reloads, 0)
  assert.equal(requests[0].options.cache, 'no-store')
  assert.match(requests[0].url, /^\/version\.json\?t=/)
  updates.dismiss()
  await updates.check(true)
  assert.equal(updates.noticeVisible, false)
  assert.equal(updates.available, true, 'dismissal must retain the settings-page refresh action')
  latest = { ...release, buildId: 'build-c' }
  await updates.check(true)
  assert.equal(updates.noticeVisible, true)
  updates.refresh()
  assert.equal(reloads, 1)
})

test('bad responses and offline checks cannot invent an update or clear an existing one', async () => {
  const updates = useUpdates()
  latest = { buildId: 'broken' }
  await updates.check(true)
  assert.equal(updates.checkError, true)
  assert.equal(updates.available, false)
  latest = { ...release, buildId: 'build-b' }
  await updates.check(true)
  assert.equal(updates.available, true)
  for (const response of [new Response('deploying', { status: 503 }), new Response('<html>fallback</html>')]) {
    mock.method(globalThis, 'fetch', async () => response)
    await updates.check(true)
    assert.equal(updates.checkError, true)
    assert.equal(updates.available, true)
  }
  mock.method(globalThis, 'fetch', () => { throw new Error('offline') })
  await updates.check(true)
  assert.equal(updates.checking, false)
  mock.method(globalThis, 'fetch', async () => new Response(JSON.stringify(release)))
  await updates.check(true)
  assert.equal(updates.available, false, 'server rollback to the loaded build clears the prompt')
  assert.equal(updates.checkError, false)
  assert.equal(reloads, 0)
})

test('background tabs stay quiet and foreground/online/interval checks detect deployment', async () => {
  const updates = useUpdates()
  stop = updates.start()
  assert.equal(updates.start(), stop, 'monitoring must not install duplicate timers')
  await updates.check()
  assert.equal(requests.length, 1)
  doc.visibilityState = 'hidden'; now += 60000
  intervals.get(60000)()
  windowTarget.dispatchEvent(new Event('focus'))
  assert.equal(requests.length, 1)
  latest = { ...release, buildId: 'build-b' }
  doc.visibilityState = 'visible'
  doc.dispatchEvent(new Event('visibilitychange'))
  await updates.check()
  assert.equal(updates.available, true)
  assert.equal(requests.length, 2)
  windowTarget.dispatchEvent(new Event('focus'))
  await updates.check()
  assert.equal(requests.length, 2, 'focus and visibility events are throttled')
  now += 60000; intervals.get(60000)(); await updates.check()
  assert.equal(requests.length, 3)
  windowTarget.dispatchEvent(new Event('online')); await updates.check()
  assert.equal(requests.length, 4)
  stop(); stop = undefined
  assert.equal(intervals.size, 0)
  now += 60000; windowTarget.dispatchEvent(new Event('focus'))
  assert.equal(requests.length, 4)
})

test('a stalled version response times out and later checks can recover', async () => {
  const updates = useUpdates()
  let timeout
  windowTarget.setTimeout = fn => { timeout = fn; return 1 }
  windowTarget.clearTimeout = () => {}
  mock.method(globalThis, 'fetch', (_url, { signal }) => new Promise((_resolve, reject) => {
    signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
  }))
  const pending = updates.check(true)
  await Promise.resolve()
  timeout()
  await pending
  assert.equal(updates.checking, false)
  assert.equal(updates.checkError, true)
  mock.method(globalThis, 'fetch', async () => new Response(JSON.stringify(release)))
  await updates.check(true)
  assert.equal(updates.checkError, false)
})
