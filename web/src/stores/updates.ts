import { computed, ref } from 'vue'
import { defineStore } from 'pinia'

export interface Release { version: string; buildId: string; builtAt: string }
declare const __APP_RELEASE__: Release
export const currentRelease: Release = __APP_RELEASE__

function validRelease(value: unknown): value is Release {
  if (!value || typeof value !== 'object') return false
  const v = value as Release
  return typeof v.version === 'string' && /^[\w.+-]{1,64}$/.test(v.version) &&
    typeof v.buildId === 'string' && /^[\w.-]{1,100}$/.test(v.buildId) &&
    typeof v.builtAt === 'string' && v.builtAt.length <= 40 && Number.isFinite(Date.parse(v.builtAt))
}

export const useUpdates = defineStore('updates', () => {
  const latest = ref<Release | null>(null)
  const checking = ref(false)
  const checkError = ref(false)
  const dismissedBuild = ref('')
  const available = computed(() => !!latest.value && latest.value.buildId !== currentRelease.buildId)
  const noticeVisible = computed(() => available.value && latest.value?.buildId !== dismissedBuild.value)
  let pending: Promise<void> | null = null
  let lastCheck = 0
  let stopMonitoring: (() => void) | null = null

  function check(force = false): Promise<void> {
    if (pending) return pending
    if (!force && lastCheck && Date.now() - lastCheck < 15000) return Promise.resolve()
    lastCheck = Date.now()
    checking.value = true
    checkError.value = false
    pending = Promise.resolve().then(async () => {
      const controller = new AbortController()
      const timer = window.setTimeout(() => controller.abort(), 10000)
      try {
        const resp = await fetch(`/version.json?t=${Date.now()}`, {
          cache: 'no-store', signal: controller.signal, headers: { Accept: 'application/json' },
        })
        if (!resp.ok) throw new Error('version unavailable')
        const data: unknown = await resp.json()
        if (!validRelease(data)) throw new Error('invalid version')
        latest.value = data
      } catch {
        checkError.value = true // Offline/deploying: preserve a known update, never force a reload.
      } finally {
        window.clearTimeout(timer)
        checking.value = false
        pending = null
      }
    })
    return pending
  }

  function start() {
    if (stopMonitoring) return stopMonitoring
    const whenVisible = () => { if (document.visibilityState === 'visible') void check() }
    const afterOnline = () => { if (document.visibilityState === 'visible') void check(true) }
    const afterChunkError = () => { void check(true) }
    const timer = window.setInterval(whenVisible, 60000)
    document.addEventListener('visibilitychange', whenVisible)
    window.addEventListener('focus', whenVisible)
    window.addEventListener('pageshow', whenVisible)
    window.addEventListener('online', afterOnline)
    window.addEventListener('vite:preloadError', afterChunkError)
    whenVisible()
    stopMonitoring = () => {
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', whenVisible)
      window.removeEventListener('focus', whenVisible)
      window.removeEventListener('pageshow', whenVisible)
      window.removeEventListener('online', afterOnline)
      window.removeEventListener('vite:preloadError', afterChunkError)
      stopMonitoring = null
    }
    return stopMonitoring
  }

  function dismiss() { dismissedBuild.value = latest.value?.buildId || '' }
  function refresh() { window.location.reload() }
  return { latest, checking, checkError, available, noticeVisible, check, start, dismiss, refresh }
})
