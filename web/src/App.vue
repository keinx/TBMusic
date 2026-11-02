<script setup lang="ts">

import { watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuth } from './stores/auth'
import { usePlayer } from './stores/player'
import { useLibrary } from './stores/library'
import { usePrefs } from './stores/prefs'
import LoginOverlay from './components/LoginOverlay.vue'
import NavBar from './components/NavBar.vue'
import MiniPlayer from './components/MiniPlayer.vue'
import PlayerOverlay from './components/PlayerOverlay.vue'
import ConfirmDialog from './components/ConfirmDialog.vue'
import AmbientBackground from './components/AmbientBackground.vue'
import UpdateNotice from './components/UpdateNotice.vue'
import { toasts } from './toast'

const auth = useAuth()
const library = useLibrary()
const route = useRoute()
const router = useRouter()

// usePlayer() 的实例化副作用(音频元素/事件绑定)必须先于任何组件使用;usePrefs() 应用主题
usePlayer()
usePrefs()

// 登录后加载库数据(watch 单一入口;LoginOverlay 的 emit 仅用于关弹窗)
async function onLoggedIn() {
  try {
    await Promise.all([library.loadPlaylists(), library.loadBlacklist()])
  } catch { /* 加载失败不阻塞 */ }
}
// 登录完成由 watch 统一处理(初始挂载时 auth.init 尚未完成,直接读恒为 false)
watch(() => auth.user?.id, (v) => {
  if (v) {
    onLoggedIn()
    return
  }
  // 会话失效(401)/主动登出后,管理页只剩 403 的半失效界面:立即退回首页。
  // 必须等 auth.loaded 为 true,否则刷新深链时会话尚未校验完就被弹走。
  if (auth.loaded && route.meta.admin) router.replace('/')
})
</script>

<template>
  <div class="app-shell">
    <AmbientBackground />
    <NavBar />
    <main class="app-main">
      <router-view :key="auth.user?.id ?? 'guest'" />
    </main>

    <MiniPlayer :key="`mini:${auth.user?.id ?? 'guest'}`" />
    <PlayerOverlay :key="`overlay:${auth.user?.id ?? 'guest'}`" />
    <LoginOverlay />
    <ConfirmDialog />
    <UpdateNotice />

    <div class="toast-wrap" role="status" aria-live="polite">
      <div v-for="t in toasts" :key="t.id" class="toast">{{ t.text }}</div>
    </div>
  </div>
</template>

<style scoped>
.app-shell {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
}
.app-main {
  flex: 1;
  width: 100%;
}
</style>
