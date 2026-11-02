import { defineStore } from 'pinia'
import { ref, computed, onScopeDispose } from 'vue'
import { api, ApiError, invalidateSessionRequests, setUnauthorizedHandler, setExpectedAccount } from '../api'
import { safeStorage } from '../utils/safeStorage'
import { useLibrary } from './library'
import { usePlayer } from './player'
import { closeDialog } from '../dialog'

interface User {
  id: number
  username: string
  isAdmin: boolean
  listenCount?: number
}

export const useAuth = defineStore('auth', () => {
  const user = ref<User | null>(null)
  const loaded = ref(false)
  const allowRegister = ref(true)
  const inviteRequired = ref(false)
  let authRequest = 0

  const loggedIn = computed(() => !!user.value)
  const isAdmin = computed(() => !!user.value?.isAdmin)

  function setUser(next: User | null) {
    if (user.value?.id !== next?.id) {
      invalidateSessionRequests()
      useLibrary().reset()
      usePlayer().setAccount(next?.id ?? null)
      closeDialog(null)
    }
    user.value = next
    if (next) useLibrary().updateListenCount(next.listenCount)
    setExpectedAccount(next?.id ?? null)
  }

  async function init() {
    const seq = ++authRequest
    try {
      const d = await api.get('/api/auth/me')
      if (seq === authRequest) setUser(d.user)
    } catch (error) {
      if (seq === authRequest && (!loaded.value || (error instanceof ApiError && error.status === 401))) setUser(null)
    } finally {
      if (seq === authRequest) loaded.value = true
    }
    loadAuthConfig()
  }

  async function loadAuthConfig() {
    try {
      const d = await api.get('/api/auth/config')
      allowRegister.value = !!d.allowRegister
      inviteRequired.value = !!d.inviteRequired
    } catch { /* ignore */ }
  }

  async function login(username: string, password: string) {
    const seq = ++authRequest
    const d = await api.post('/api/auth/login', { username, password })
    if (seq !== authRequest) throw new DOMException('账户已变更', 'AbortError')
    setUser(d.user)
    publishAccountChange()
  }

  async function register(username: string, password: string, inviteCode = '') {
    const seq = ++authRequest
    const d = await api.post('/api/auth/register', { username, password, inviteCode })
    if (seq !== authRequest) throw new DOMException('账户已变更', 'AbortError')
    setUser(d.user)
    publishAccountChange()
  }

  async function logout() {
    const seq = ++authRequest
    try { await api.post('/api/auth/logout') } catch (error) {
      if (!(error instanceof ApiError && error.status === 401)) throw error
    }
    if (seq !== authRequest) return
    setUser(null)
    publishAccountChange()
    loadAuthConfig() // 登出后刷新注册开关/邀请码配置,注册表单需要
  }

  async function changePassword(oldPassword: string, newPassword: string) {
    await api.post('/api/auth/change-password', { oldPassword, newPassword })
    publishAccountChange()
  }

  // 任何接口返回 401 时,清空登录态展示遮罩
  setUnauthorizedHandler((changed) => { setUser(null); if (changed) void init() })

  const syncKey = 'tbmusic_auth_epoch'
  const channel = typeof window !== 'undefined' && window.BroadcastChannel ? new window.BroadcastChannel(syncKey) : null
  function publishAccountChange() {
    safeStorage.setItem(syncKey, `${Date.now()}:${Math.random()}`)
    channel?.postMessage('changed')
  }
  function syncAccount() { setUser(null); void init() }
  function onStorage(event: StorageEvent) { if (event.key === syncKey) syncAccount() }
  function onFocus() { if (loaded.value) void init() }
  function onVisible() { if (document.visibilityState === 'visible') onFocus() }
  if (channel) channel.onmessage = syncAccount
  if (typeof window !== 'undefined') {
    window.addEventListener('storage', onStorage)
    window.addEventListener('focus', onFocus)
  }
  if (typeof document !== 'undefined') document.addEventListener?.('visibilitychange', onVisible)
  onScopeDispose(() => {
    channel?.close()
    if (typeof window !== 'undefined') {
      window.removeEventListener?.('storage', onStorage)
      window.removeEventListener?.('focus', onFocus)
    }
    if (typeof document !== 'undefined') document.removeEventListener?.('visibilitychange', onVisible)
    setExpectedAccount(null)
  })

  return { user, loaded, loggedIn, isAdmin, allowRegister, inviteRequired, init, loadAuthConfig, login, register, logout, changePassword }
})
