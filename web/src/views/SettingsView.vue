<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useAuth } from '../stores/auth'
import { usePlayer } from '../stores/player'
import { useLibrary } from '../stores/library'
import { usePrefs, SpectrumStyle, AccentColor } from '../stores/prefs'
import { qualityLabel, SOURCE_NAMES } from '../types'
import { directRecords, clearDirectRecords } from '../utils/directPlayback'
import { currentRelease, useUpdates } from '../stores/updates'
import Icon from '../components/Icon.vue'
import MBack from '../components/MBack.vue'
import { toast } from '../toast'

const auth = useAuth()
const library = useLibrary()
const countError = ref(false)
onMounted(async () => {
  try { await library.loadListenCount() } catch { countError.value = true }
})

const player = usePlayer()
const connections = ref(directRecords())
const updates = useUpdates()
const buildTime = new Date(currentRelease.builtAt).toLocaleString('zh-CN', { hour12: false })
function clearConnections() {
  try {
    clearDirectRecords()
    connections.value = directRecords()
    toast('播放连接记录已清空')
  } catch { toast('清空失败，请检查浏览器存储权限') }
}
onMounted(() => { player.loadSources() }) // 直达设置页时补拉音源能力(幂等)
const prefs = usePrefs()

// 修改密码
const oldPw = ref('')
const newPw = ref('')
const newPw2 = ref('')
const changing = ref(false)

// 默认音质下拉(与播放页音质选择同款)
const qOpen = ref(false)
function pickQuality(q: string) {
  player.setQualityPref(q)
  qOpen.value = false
}

const qualities = computed(() => {
  const all: string[] = []
  for (const s of Object.values(player.sources || {})) {
    for (const q of s.qualitys || []) if (!all.includes(q)) all.push(q)
  }
  return all.length ? all : ['128k', '320k', 'flac']
})

const spectrumOptions = [
  { id: 'bars', label: '条形' },
  { id: 'wave', label: '波浪' },
  { id: 'dots', label: '圆点' },
]

const accentOptions: { id: AccentColor; label: string; color: string }[] = [
  { id: 'blue', label: '蓝色', color: '#3d8bff' },
  { id: 'yellow', label: '黄色', color: '#ff9642' },
  { id: 'green', label: '绿色', color: '#2ecf8f' },
  { id: 'purple', label: '紫色', color: '#a06bff' },
  { id: 'red', label: '红色', color: '#ff5f6d' },
]

async function changePassword() {
  if (changing.value) return
  if (!oldPw.value || !newPw.value) { toast('请填写完整'); return }
  if (newPw.value.length < 6) { toast('新密码至少 6 位'); return }
  if (newPw.value !== newPw2.value) { toast('两次输入的新密码不一致'); return }
  changing.value = true
  try {
    await auth.changePassword(oldPw.value, newPw.value)
    toast('密码修改成功')
    oldPw.value = ''
    newPw.value = ''
    newPw2.value = ''
  } catch (e: any) {
    toast(e?.message || '修改失败')
  } finally {
    changing.value = false
  }
}
</script>

<template>
  <div class="page settings">
    <MBack />
    <h2 class="page-title"><Icon name="settings" :size="20" style="color: var(--accent); vertical-align: -3px" /> 个人设置</h2>

    <section class="set-card">
      <h3><Icon name="user" :size="16" /> 账户</h3>
      <div class="set-row">
        <span>用户名</span>
        <b>{{ auth.user?.username }}<em v-if="auth.isAdmin" class="badge">超级管理员</em></b>
      </div>
      <div class="set-row">
        <div><div>累计听歌</div><div class="tip2">实际播放满 5 秒计 1 次，重复播放累计，清空历史不归零</div></div>
        <b aria-live="polite">{{ library.listenCount === null ? (countError ? '暂时无法获取' : '加载中…') : `${library.listenCount.toLocaleString('zh-CN')} 次` }}</b>
      </div>
      <div class="set-row">
        <span>退出登录</span>
        <button class="btn small" @click="auth.logout()">
          <Icon name="logout" :size="14" /> 退出
        </button>
      </div>
    </section>

    <section class="set-card">
      <h3><Icon name="key" :size="16" /> 修改密码</h3>
      <div class="pw-form">
        <input v-model="oldPw" type="password" class="input" placeholder="旧密码" autocomplete="current-password" />
        <input v-model="newPw" type="password" class="input" placeholder="新密码(至少 6 位)" autocomplete="new-password" />
        <input v-model="newPw2" type="password" class="input" placeholder="再次输入新密码" autocomplete="new-password" @keyup.enter="changePassword" />
        <button class="btn primary" :disabled="changing" @click="changePassword">
          <Icon name="check" :size="15" /> {{ changing ? '提交中…' : '确认修改' }}
        </button>
      </div>
    </section>

    <section class="set-card">
      <h3><Icon name="disc" :size="16" /> 外观与播放</h3>
      <div class="set-row">
        <span>主题</span>
        <div class="seg">
          <button :class="{ on: prefs.theme === 'dark' }" @click="prefs.setTheme('dark')">深色</button>
          <button :class="{ on: prefs.theme === 'light' }" @click="prefs.setTheme('light')">浅色</button>
          <button :class="{ on: prefs.theme === 'system' }" @click="prefs.setTheme('system')">跟随系统</button>
        </div>
      </div>
      <div class="set-row">
        <div>
          <div>主题色</div>
          <div class="tip2">全站按钮、进度条与频谱的点缀色</div>
        </div>
        <div class="accent-dots">
          <button
            v-for="a in accentOptions" :key="a.id"
            class="accent-dot" :class="{ on: prefs.accent === a.id }"
            :style="{ background: a.color }"
            :title="a.label"
            @click="prefs.setAccent(a.id)"
          >
            <Icon v-if="prefs.accent === a.id" name="check" :size="12" style="color: #fff" />
          </button>
        </div>
      </div>
      <div class="set-row">
        <span>默认音质(不可用时自动降低)</span>
        <div class="qual-drop">
          <div v-if="qOpen" class="qd-mask" @click="qOpen = false"></div>
          <button class="qd-btn" @click="qOpen = !qOpen">
            {{ qualityLabel(player.qualityPref) }}
            <Icon name="chevron-down" :size="14" style="margin-left: 2px" />
          </button>
          <div v-if="qOpen" class="qd-menu">
            <button
              v-for="q in qualities" :key="q"
              :class="{ on: q === player.qualityPref }"
              @click="pickQuality(q)"
            >
              {{ qualityLabel(q) }}
              <Icon v-if="q === player.qualityPref" name="check" :size="13" />
            </button>
          </div>
        </div>
      </div>
      <div class="set-row">
        <div>
          <div>光盘旋转封面</div>
          <div class="tip2">播放页封面变为旋转唱片样式</div>
        </div>
        <button class="switch" :class="{ on: prefs.discSpin }" @click="prefs.setDiscSpin(!prefs.discSpin)">
          <span class="knob"></span>
        </button>
      </div>
      <div class="set-row spectrum-row">
        <div>
          <div>频谱动画样式</div>
          <div class="tip2">中转播放时随音乐跳动；直连播放使用装饰动画</div>
        </div>
        <div class="seg">
          <button v-for="opt in spectrumOptions" :key="opt.id" :class="{ on: prefs.spectrumStyle === opt.id }" @click="prefs.setSpectrumStyle(opt.id as SpectrumStyle)">
            {{ opt.label }}
          </button>
        </div>
      </div>
    </section>

    <section class="set-card" aria-labelledby="connections-title">
      <h3 id="connections-title"><Icon name="transfer" :size="16" /> 播放连接</h3>
      <p class="tip2">优先直连，失败自动中转。记录仅用于当前浏览器；中转记录 24 小时后重新尝试直连。</p>
      <div class="set-row"><span>当前连接</span><b>{{ player.playbackRoute === 'direct' ? '直连播放' : player.playbackRoute === 'proxy' ? '中转播放' : '尚未播放' }}</b></div>
      <div class="setting-actions">
        <button class="btn small" @click="connections = directRecords()"><Icon name="refresh" :size="14" /> 刷新连接记录</button>
        <button class="btn small clear-records" @click="clearConnections"><Icon name="trash" :size="14" /> 清空连接记录</button>
      </div>
      <div class="set-row connection-row" v-for="item in connections" :key="item.source + item.origin">
        <div class="connection-host"><b>{{ SOURCE_NAMES[item.source] || item.source }}</b><div class="tip2">{{ item.origin }}</div></div>
        <span>{{ item.mode === 'direct' ? '直连可用' : '使用中转' }}</span>
      </div>
      <p v-if="!connections.length" class="tip2 connection-empty">暂无连接记录，播放成功后会自动记录音源兼容情况。</p>
    </section>

    <section class="set-card" aria-labelledby="version-title">
      <h3 id="version-title"><Icon name="info" :size="16" /> 版本</h3>
      <div class="set-row"><span>当前版本</span><b>v{{ currentRelease.version }}</b></div>
      <div class="set-row"><span>构建时间</span><span class="version-time">{{ buildTime }}</span></div>
      <p class="tip2" aria-live="polite">{{ updates.available ? '系统已更新，刷新页面即可使用最新版本。' : updates.checking ? '正在检查更新…' : updates.checkError ? '暂时无法检查更新，请稍后重试。' : updates.latest ? '当前已是最新版本。' : '页面会自动检查系统更新。' }}</p>
      <div class="setting-actions">
        <button class="btn small" :disabled="updates.checking" @click="updates.check(true)"><Icon name="refresh" :size="14" /> {{ updates.checking ? '检查中…' : '检查更新' }}</button>
        <button v-if="updates.available" class="btn primary small" @click="updates.refresh">刷新页面</button>
      </div>
    </section>
  </div>
</template>

<style scoped>
.connection-host { min-width: 0; overflow-wrap: anywhere; }
.connection-row > span { flex-shrink: 0; }
.connection-empty { margin: 12px 0 0; }
.setting-actions { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 12px; }
.setting-actions .btn { min-height: 36px; }
.clear-records { color: var(--danger); }
.version-time { font-size: 12px; color: var(--text-dim); text-align: right; }

.page-title { display: flex; align-items: center; gap: 8px; }
.set-card {
  background: color-mix(in srgb, var(--panel) 68%, transparent);
  backdrop-filter: blur(20px) saturate(1.2);
  -webkit-backdrop-filter: blur(20px) saturate(1.2);
  border: 1px solid var(--border);
  border-radius: 14px;
  padding: 18px 20px;
  margin-bottom: 14px;
}
.set-card h3 {
  margin: 0 0 12px;
  font-size: 15.5px;
  display: flex;
  align-items: center;
  gap: 8px;
}
.set-card h3 :deep(svg) { color: var(--accent); }
.set-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 8px 0;
  font-size: 14px;
}
.badge {
  font-style: normal;
  font-size: 10.5px;
  color: var(--accent);
  border: 1px solid var(--accent);
  border-radius: 4px;
  padding: 0 5px;
  margin-left: 8px;
  vertical-align: 1px;
}
.pw-form { display: flex; flex-direction: column; gap: 9px; }
.pw-form .btn { justify-content: center; }
.seg { display: flex; background: var(--bg-soft); border-radius: 9px; padding: 3px; }
.seg button { padding: 6px 14px; border-radius: 7px; font-size: 13px; color: var(--text-dim); }
.seg button.on { background: var(--panel); color: var(--accent); box-shadow: 0 1px 6px rgba(0,0,0,.2); }
/* 默认音质下拉:与播放页音质选择同款 */
.qual-drop { position: relative; }
.qd-mask { position: fixed; inset: 0; z-index: 5; }
.qd-btn {
  display: inline-flex;
  align-items: center;
  padding: 6px 14px;
  border-radius: 999px;
  border: 1px solid var(--border);
  color: var(--text);
  font-size: 12.5px;
  background: var(--panel);
  transition: border-color 0.15s, color 0.15s;
}
.qd-btn:hover { border-color: var(--accent); color: var(--accent); }
.qd-menu {
  position: absolute;
  top: calc(100% + 8px);
  right: 0;
  z-index: 6;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 4px;
  display: flex;
  flex-direction: column;
  min-width: 110px;
  box-shadow: var(--shadow);
}
.qd-menu button {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 14px;
  border-radius: 7px;
  color: var(--text);
  font-size: 13px;
  text-align: left;
}
.qd-menu button:hover, .qd-menu button.on { background: var(--accent-soft); color: var(--accent); }
.tip2 { font-size: 12px; color: var(--text-dim); margin-top: 2px; }
.accent-dots { display: flex; gap: 10px; align-items: center; }
.accent-dot {
  width: 28px;
  height: 28px;
  border-radius: 50%;
  border: 2px solid transparent;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: all 0.15s;
  flex-shrink: 0;
}
.accent-dot.on { border-color: var(--text); transform: scale(1.12); }
.spectrum-row .seg button { min-width: 52px; }
.switch {
  width: 48px;
  height: 27px;
  border-radius: 999px;
  background: var(--bg-soft);
  border: 1px solid var(--border);
  position: relative;
  transition: all 0.2s;
  flex-shrink: 0;
}
.switch .knob {
  position: absolute;
  top: 2px;
  left: 2px;
  width: 21px;
  height: 21px;
  border-radius: 50%;
  background: var(--text-dim);
  transition: all 0.2s;
}
.switch.on { background: var(--accent); border-color: var(--accent); }
.switch.on .knob { left: 23px; background: #fff; }
@media (min-width: 769px) {
  .spectrum-row .seg button { padding: 6px 12px; }
}
</style>
