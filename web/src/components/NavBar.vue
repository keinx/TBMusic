<script setup lang="ts">
import { toast } from '../toast'
import { ref, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuth } from '../stores/auth'
import { usePlayer } from '../stores/player'
import Icon from './Icon.vue'
import AppLogo from './AppLogo.vue'

const route = useRoute()
const router = useRouter()
const auth = useAuth()
const player = usePlayer()
const menuOpen = ref(false)

const mainItems = [
  { to: '/', label: '首页', icon: 'home' },
  { to: '/hot', label: '热门', icon: 'flame' },
  { to: '/random', label: '随机', icon: 'shuffle' },
  { to: '/playlists', label: '收藏', icon: 'heart' },
]

const menuItems = computed(() => {
  const items = [
    { to: '/queue', label: '播放列表', icon: 'list' },
    { to: '/history', label: '播放历史', icon: 'clock' },
    { to: '/blacklist', label: '歌曲黑名单', icon: 'ban' },
  ]
  if (auth.isAdmin) {
    items.push({ to: '/users', label: '用户列表', icon: 'users' })
    items.push({ to: '/system', label: '系统设置', icon: 'sliders' })
  }
  items.push({ to: '/settings', label: '个人设置', icon: 'settings' })
  return items
})

const active = computed(() => {
  const p = route.path
  if (p === '/') return '/'
  for (const it of mainItems) {
    if (it.to !== '/' && p.startsWith(it.to)) return it.to
  }
  return ''
})

const menuActive = computed(() =>
  ['/history', '/blacklist', '/users', '/system', '/settings', '/my', '/queue'].some((x) => route.path.startsWith(x))
)

// 移动端底部 Tab:播放列表图标同尺寸,下方文字为当前队列歌曲数(0/999/999+)
const queueLabel = computed(() => {
  const n = player.queue.length
  return n > 999 ? '999+' : String(n)
})
const tabItems = computed(() => [
  { to: '/', label: '首页', icon: 'home' },
  { to: '/hot', label: '热门', icon: 'flame' },
  { to: '/queue', label: queueLabel.value, icon: 'disc' },
  { to: '/playlists', label: '收藏', icon: 'heart' },
  { to: '/my', label: '我的', icon: 'user' },
])
// 这些页面(详情/二级页)移动端不显示底部导航
const hideTabbar = computed(() =>
  ['/history', '/blacklist', '/users', '/system', '/settings', '/detail'].some((x) => route.path.startsWith(x))
)

function closeMenu() {
  setTimeout(() => (menuOpen.value = false), 120)
}

async function doLogout() {
  closeMenu()
  try {
    await auth.logout()
    router.push('/')
  } catch (e: any) { toast(e?.message || '退出失败，请重试') }
}
</script>

<template>
  <!-- 桌面顶栏 -->
  <header class="navbar topbar" v-if="auth.loggedIn">
    <router-link to="/" class="brand">
      <AppLogo :size="30" />
      <span class="brand-name">投币音乐</span>
    </router-link>

    <nav class="nav-links">
      <router-link v-for="it in mainItems" :key="it.to" :to="it.to" class="nav-link" :class="{ active: active === it.to }">
        <Icon :name="it.icon" :size="17" />
        <span>{{ it.label }}</span>
      </router-link>
    </nav>

    <div class="nav-right">
      <button class="icon-btn nav-search" title="搜索" @click="router.push('/search')">
        <Icon name="search" :size="18" />
      </button>

      <!-- 我的 下拉 -->
      <div class="me-menu" @mouseenter="menuOpen = true" @mouseleave="menuOpen = false">
        <button class="me-btn" :class="{ active: menuActive }" @click="menuOpen = !menuOpen">
          <span class="me-avatar"><Icon name="user" :size="15" /></span>
          <span class="me-name">{{ auth.user?.username }}</span>
          <Icon name="chevron-down" :size="14" />
        </button>
        <transition name="drop">
          <div v-if="menuOpen" class="me-dropdown">
            <div class="me-head">
              <span class="me-avatar lg"><Icon name="user" :size="18" /></span>
              <div>
                <div class="me-head-name">{{ auth.user?.username }}</div>
                <div class="me-head-role">{{ auth.isAdmin ? '超级管理员' : '普通用户' }}</div>
              </div>
            </div>
            <router-link v-for="it in menuItems" :key="it.to" :to="it.to" class="me-item" @click="closeMenu">
              <Icon :name="it.icon" :size="16" />
              <span>{{ it.label }}</span>
            </router-link>
            <button class="me-item logout" @click="doLogout">
              <Icon name="logout" :size="16" />
              <span>退出登录</span>
            </button>
          </div>
        </transition>
      </div>
    </div>
  </header>

  <!-- 移动端顶部(仅 logo + 搜索) -->
  <header class="m-topbar" v-if="auth.loggedIn">
    <router-link to="/" class="brand">
      <AppLogo :size="26" />
    </router-link>
    <span class="m-title">投币音乐</span>
    <button class="icon-btn" :class="{ on: route.path.startsWith('/random') }" title="随机歌单" @click="router.push('/random')">
      <Icon name="shuffle" :size="19" />
    </button>
    <button class="icon-btn" :class="{ on: route.path.startsWith('/search') }" @click="router.push('/search')">
      <Icon name="search" :size="19" />
    </button>
  </header>

  <!-- 移动端底部 Tab -->
  <nav class="tabbar" v-if="auth.loggedIn && !hideTabbar">
    <router-link
      v-for="it in tabItems" :key="it.to" :to="it.to"
      class="tab-item" :class="{ active: active === it.to || (it.to === '/my' && menuActive && !route.path.startsWith('/queue')) || (it.to === '/queue' && route.path.startsWith('/queue')) }"
    >
      <Icon :name="it.icon" :size="20" />
      <span v-if="it.label" class="tab-label">{{ it.label }}</span>
    </router-link>
  </nav>
</template>

<style scoped>
.topbar {
  position: sticky;
  top: 0;
  z-index: 100;
  display: flex;
  align-items: center;
  gap: 22px;
  padding: 0 22px;
  height: 58px;
  background: color-mix(in srgb, var(--bg) 82%, transparent);
  backdrop-filter: blur(14px);
  border-bottom: 1px solid var(--border);
}
.brand {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 700;
  font-size: 16px;
  color: var(--text);
}
.nav-links { display: flex; gap: 4px; flex: 1; }
.nav-link {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  padding: 7px 15px;
  border-radius: 9px;
  font-size: 14.5px;
  color: var(--text-dim);
  transition: all 0.15s;
}
.nav-link:hover { color: var(--text); background: var(--panel); }
.nav-link.active { color: var(--text); background: var(--accent-soft); }
.nav-link.active :deep(svg) { color: var(--accent); }

.nav-right { margin-left: auto; display: flex; align-items: center; gap: 8px; }
.nav-search { color: var(--text-dim); }

.me-menu { position: relative; }
.me-btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  padding: 5px 12px 5px 6px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--panel);
  color: var(--text);
  font-size: 13.5px;
  transition: all 0.15s;
}
.me-btn:hover, .me-btn.active { border-color: var(--accent); }
.me-avatar {
  width: 26px;
  height: 26px;
  border-radius: 50%;
  background: var(--accent-soft);
  color: var(--accent);
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.me-avatar.lg { width: 36px; height: 36px; }
.me-dropdown {
  position: absolute;
  top: calc(100% + 10px);
  right: 0;
  width: 200px;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 14px;
  box-shadow: var(--shadow);
  padding: 6px;
  z-index: 200;
}
.me-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 10px 12px;
  border-bottom: 1px solid var(--border);
  margin-bottom: 6px;
}
.me-head-name { font-size: 14px; font-weight: 600; }
.me-head-role { font-size: 11.5px; color: var(--text-dim); margin-top: 1px; }
.me-item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 9px 10px;
  border-radius: 9px;
  font-size: 13.5px;
  color: var(--text);
  text-align: left;
}
.me-item :deep(svg) { color: var(--text-dim); }
.me-item:hover { background: var(--panel-hover); }
.me-item.logout { color: var(--danger); }
.me-item.logout :deep(svg) { color: var(--danger); }

.drop-enter-active, .drop-leave-active { transition: all 0.15s; }
.drop-enter-from, .drop-leave-to { opacity: 0; transform: translateY(-6px); }

.m-topbar { display: none; }
.tabbar { display: none; }

@media (max-width: 768px) {
  .topbar { display: none; }
  .m-topbar {
    position: sticky;
    top: 0;
    z-index: 100;
    display: flex;
    align-items: center;
    gap: 10px;
    height: 50px;
    padding: 0 14px;
    background: color-mix(in srgb, var(--bg) 88%, transparent);
    backdrop-filter: blur(14px);
    border-bottom: 1px solid var(--border);
  }
  .m-title {
    flex: 1;
    font-weight: 700;
    font-size: 15px;
    color: var(--text);
  }
  .m-topbar .icon-btn.on { color: var(--accent); }
  .tabbar {
    display: flex;
    position: fixed;
    bottom: 0;
    left: 0;
    right: 0;
    z-index: 120;
    background: color-mix(in srgb, var(--bg) 92%, transparent);
    backdrop-filter: blur(14px);
    border-top: 1px solid var(--border);
    padding: 5px 0 calc(5px + env(safe-area-inset-bottom, 0px));
  }
  .tab-item {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 2px;
    color: var(--text-dim);
  }
  .tab-item.active { color: var(--accent); }
  .tab-label { font-size: 10.5px; }
}
</style>
