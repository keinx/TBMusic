<script setup lang="ts">
import { ref, watch } from 'vue'
import { useAuth } from '../stores/auth'
import { toast } from '../toast'
import AppLogo from './AppLogo.vue'

const auth = useAuth()

const tab = ref<'login' | 'register'>('login')
const username = ref('')
const password = ref('')
const inviteCode = ref('')
const loading = ref(false)
watch(() => auth.user?.id, () => { password.value = ''; inviteCode.value = '' }, { flush: 'sync' })

if (!auth.allowRegister) tab.value = 'login'

async function submit() {
  if (loading.value) return
  if (!username.value.trim() || !password.value) {
    toast('请输入用户名和密码')
    return
  }
  loading.value = true
  try {
    if (tab.value === 'login') {
      await auth.login(username.value.trim(), password.value)
      toast(`欢迎回来,${auth.user?.username}`)
    } else {
      if (auth.inviteRequired && !inviteCode.value.trim()) {
        toast('请填写邀请码')
        loading.value = false
        return
      }
      await auth.register(username.value.trim(), password.value, inviteCode.value.trim())
      toast('注册成功,已自动登录')
    }
    password.value = ''
    inviteCode.value = ''
  } catch (e: any) {
    toast(e?.message || '操作失败')
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <transition name="fade">
    <div v-if="auth.loaded && !auth.loggedIn" class="login-overlay">
      <div class="login-box">
        <AppLogo :size="58" style="margin: 0 auto 10px; display: block; filter: drop-shadow(0 10px 26px color-mix(in srgb, var(--accent) 40%, transparent))" />
        <h1 class="login-title">投币音乐</h1>
        <div class="login-tabs">
          <button :class="{ active: tab === 'login' }" @click="tab = 'login'">登录</button>
          <button v-if="auth.allowRegister" :class="{ active: tab === 'register' }" @click="tab = 'register'">注册</button>
        </div>
        <form @submit.prevent="submit">
          <input v-model="username" class="input" placeholder="用户名" autocomplete="username" />
          <input v-model="password" class="input" type="password" placeholder="密码" autocomplete="current-password" />
          <input v-if="tab === 'register' && auth.inviteRequired" v-model="inviteCode" class="input invite" placeholder="邀请码" />
          <button class="btn primary login-btn" type="submit" :disabled="loading">
            {{ loading ? '请稍候…' : tab === 'login' ? '登录' : '注册并登录' }}
          </button>
        </form>
      </div>
    </div>
  </transition>
</template>

<style scoped>
.login-overlay {
  position: fixed;
  inset: 0;
  z-index: 2000;
  background: rgba(10, 10, 16, 0.72);
  backdrop-filter: blur(18px);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
}
.login-box {
  width: 360px;
  max-width: 100%;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 18px;
  padding: 34px 30px 26px;
  box-shadow: var(--shadow);
  text-align: center;
}
.login-title {
  margin: 0 0 18px;
  font-size: 20px;
}
.login-tabs {
  display: flex;
  background: var(--bg-soft);
  border-radius: 10px;
  padding: 4px;
  margin-bottom: 16px;
}
.login-tabs button {
  flex: 1;
  padding: 8px 0;
  border-radius: 8px;
  font-size: 14px;
  color: var(--text-dim);
}
.login-tabs button.active {
  background: var(--panel);
  color: var(--text);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.2);
}
.login-box form {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.login-btn { justify-content: center; padding: 11px 0; margin-top: 4px; font-size: 15px; }
.invite { text-align: center; letter-spacing: 4px; }
.fade-enter-active, .fade-leave-active { transition: opacity 0.25s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }
</style>
