<script setup lang="ts">
import { toast } from '../toast'
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuth } from '../stores/auth'
import Icon from '../components/Icon.vue'

const auth = useAuth()
const router = useRouter()

const items = computed(() => {
  const list = [
    { to: '/history', label: '播放历史', icon: 'clock', desc: '最近听过的歌' },
    { to: '/blacklist', label: '歌曲黑名单', icon: 'ban', desc: '不想再听到的歌' },
  ]
  if (auth.isAdmin) {
    list.push({ to: '/users', label: '用户列表', icon: 'users', desc: '管理全部用户' })
    list.push({ to: '/system', label: '系统设置', icon: 'sliders', desc: '采集接口与注册开关' })
  }
  list.push({ to: '/settings', label: '个人设置', icon: 'settings', desc: '主题、音质与密码' })
  return list
})

async function logout() {
  try {
    await auth.logout()
    router.push('/')
  } catch (e: any) { toast(e?.message || '退出失败，请重试') }
}
</script>

<template>
  <div class="page">
    <div class="my-card">
      <span class="my-avatar"><Icon name="user" :size="26" /></span>
      <div>
        <div class="my-name">{{ auth.user?.username }}</div>
        <div class="my-role">{{ auth.isAdmin ? '超级管理员' : '普通用户' }}</div>
      </div>
    </div>

    <div class="my-list">
      <router-link v-for="it in items" :key="it.to" :to="it.to" class="my-item">
        <span class="my-item-icon"><Icon :name="it.icon" :size="18" /></span>
        <span class="my-item-body">
          <span class="my-item-name">{{ it.label }}</span>
          <span class="my-item-desc">{{ it.desc }}</span>
        </span>
        <Icon name="chevron-down" :size="16" style="transform: rotate(-90deg); color: var(--text-dim)" />
      </router-link>
    </div>

    <button class="btn logout-btn" @click="logout">
      <Icon name="logout" :size="16" /> 退出登录
    </button>
  </div>
</template>

<style scoped>
.my-card {
  display: flex;
  align-items: center;
  gap: 14px;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 16px;
  padding: 18px;
  margin-bottom: 14px;
}
.my-avatar {
  width: 52px;
  height: 52px;
  border-radius: 50%;
  background: var(--accent-soft);
  color: var(--accent);
  display: flex;
  align-items: center;
  justify-content: center;
}
.my-name { font-size: 17px; font-weight: 600; }
.my-role { font-size: 12.5px; color: var(--text-dim); margin-top: 2px; }
.my-list { display: flex; flex-direction: column; gap: 6px; }
.my-item {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 13px 14px;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 13px;
  color: var(--text);
}
.my-item:active { background: var(--panel-hover); }
.my-item-icon {
  width: 38px;
  height: 38px;
  border-radius: 10px;
  background: var(--bg-soft);
  color: var(--accent);
  display: flex;
  align-items: center;
  justify-content: center;
}
.my-item-body { flex: 1; display: flex; flex-direction: column; }
.my-item-name { font-size: 14.5px; }
.my-item-desc { font-size: 11.5px; color: var(--text-dim); margin-top: 1px; }
.logout-btn {
  margin-top: 16px;
  width: 100%;
  justify-content: center;
  color: var(--danger);
}
</style>
