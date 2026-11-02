<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '../api'
import { usePlayer } from '../stores/player'
import Icon from '../components/Icon.vue'
import MBack from '../components/MBack.vue'
import { toast } from '../toast'

const player = usePlayer()
const cacheInfo = ref<{ bytes: number; limit: number; entries: number; hits: number; evictions: number } | null>(null)
const cacheSizeMB = ref<number | string>('')
const cacheBusy = ref<'loading' | 'saving' | 'clearing' | ''>('')
async function loadCache() {
  if (cacheBusy.value) return
  cacheBusy.value = 'loading'
  try {
    const firstLoad = !cacheInfo.value
    cacheInfo.value = await api.get('/api/admin/cache')
    if (firstLoad && cacheInfo.value) cacheSizeMB.value = cacheInfo.value.limit / 1024 ** 2
  } catch (e: any) { toast(e?.message || '缓存信息加载失败') }
  finally { cacheBusy.value = '' }
}
async function saveCacheSize() {
  if (cacheBusy.value || !cacheInfo.value) return
  const sizeMB = Number(cacheSizeMB.value)
  if (!Number.isInteger(sizeMB) || sizeMB < 1 || sizeMB > 1048576) {
    toast('缓存大小请输入 1-1048576 之间的整数（MB）')
    return
  }
  cacheBusy.value = 'saving'
  try {
    cacheInfo.value = await api.put('/api/admin/cache', { sizeMB }, { timeoutMs: 120000 })
    cacheSizeMB.value = sizeMB
    toast('缓存大小已保存并生效')
  } catch (e: any) { toast(e?.message || '缓存大小保存失败') }
  finally { cacheBusy.value = '' }
}
async function clearCache() {
  if (cacheBusy.value) return
  cacheBusy.value = 'clearing'
  try {
    cacheInfo.value = await api.del('/api/admin/cache', { timeoutMs: 120000 })
    toast('资源缓存已清空')
  } catch (e: any) { toast(e?.message || '清空缓存失败') }
  finally { cacheBusy.value = '' }
}
function formatBytes(n: number) { return n >= 1024 ** 3 ? (n / 1024 ** 3).toFixed(2) + ' GB' : (n / 1024 ** 2).toFixed(2) + ' MB' }
onMounted(loadCache)

interface ScriptItem {
  name: string
  url: string
  ready?: boolean
  version?: string
  error?: string
}

const scripts = ref<ScriptItem[]>([])
let initialScripts = '' // 快照:保存时比对是否需要重载脚本
const allowRegister = ref(true)
const inviteRequired = ref(false)
const inviteCode = ref('')
const saving = ref(false)
let refreshTimer = 0
const retryTimers = new Map<string, number>()
let disposed = false

// 音频域名白名单:逐条增删改
const streamHosts = ref<string[]>([])
const newHost = ref('')
const addingHost = ref(false)

// 新增采集接口表单(名称 + 地址)
const newName = ref('')
const newUrl = ref('')

// 行内编辑状态
const editIdx = ref(-1)
const editName = ref('')
const editUrl = ref('')
const retrying = ref<Record<string, boolean>>({})

const primaryName = computed(() => scripts.value[0]?.name || '-')
const dirty = computed(() => snapshot(scripts.value) !== initialScripts)

function snapshot(list: ScriptItem[]): string {
  return JSON.stringify(list.map((s) => ({ name: s.name, url: s.url })))
}

async function load(statusOnly = false) {
  try {
    const d = await api.get('/api/admin/settings')
    if (disposed) return
    if (statusOnly) {
      const statuses: ScriptItem[] = Array.isArray(d.scripts) ? d.scripts : []
      for (const script of scripts.value) {
        const status = statuses.find(s => s.url === script.url)
        if (status) {
          script.ready = status.ready
          script.version = status.version
          script.error = status.error
        }
      }
      return
    }
    scripts.value = Array.isArray(d.scripts) ? d.scripts : []
    initialScripts = snapshot(scripts.value)
    allowRegister.value = !!d.allowRegister
    inviteRequired.value = !!d.inviteRequired
    inviteCode.value = d.inviteCode || ''
    streamHosts.value = Array.isArray(d.streamHosts) ? d.streamHosts : []
  } catch (e: any) { toast(e?.message || '加载失败') }
}
onBeforeUnmount(() => {
  disposed = true
  if (refreshTimer) window.clearTimeout(refreshTimer)
  for (const timer of retryTimers.values()) window.clearTimeout(timer)
  retryTimers.clear()
})
onMounted(() => load())

/* ---------- 采集脚本:添加/编辑/删除/拖拽排序/重试加载 ---------- */
function guessName(url: string): string {
  try {
    const parts = new URL(url).pathname.split('/').filter(Boolean)
    for (let i = parts.length - 1; i >= 0; i--) {
      const p = parts[i].replace(/\.js$/i, '')
      if (p && p.toLowerCase() !== 'latest') return p
    }
  } catch { /* ignore */ }
  return '自定义采集接口'
}

function validUrl(url: string): boolean {
  return /^https?:\/\//i.test(url)
}

function addScript() {
  const url = newUrl.value.trim()
  if (!url) { toast('请填写音源 URL'); return }
  if (!validUrl(url)) { toast('音源 URL 需以 http(s) 开头'); return }
  if (scripts.value.some((s) => s.url === url)) { toast('该接口已在列表中'); return }
  const name = newName.value.trim() || guessName(url)
  scripts.value.push({ name, url })
  newName.value = ''
  newUrl.value = ''
}

function removeScript(i: number) {
  if (scripts.value.length <= 1) { toast('至少保留一个采集接口'); return }
  if (editIdx.value === i) cancelEdit()
  scripts.value.splice(i, 1)
}

const dragIndex = ref(-1)
function onDragStart(i: number, e: DragEvent) {
  if (editIdx.value >= 0) { e.preventDefault(); return } // 编辑中不允许拖拽
  dragIndex.value = i
  if (e.dataTransfer) {
    e.dataTransfer.effectAllowed = 'move'
    e.dataTransfer.setData('text/plain', String(i)) // Firefox 需要设置数据才会触发拖拽
  }
}
function onDrop(i: number) {
  if (dragIndex.value >= 0 && dragIndex.value !== i) moveScript(dragIndex.value, i)
  dragIndex.value = -1
}
function moveScript(from: number, to: number) {
  if (to < 0 || to >= scripts.value.length || from === to) return
  const list = scripts.value.slice()
  const [item] = list.splice(from, 1)
  list.splice(to, 0, item)
  scripts.value = list
}

function startEdit(i: number) {
  editIdx.value = i
  editName.value = scripts.value[i].name
  editUrl.value = scripts.value[i].url
}
function cancelEdit() {
  editIdx.value = -1
  editName.value = ''
  editUrl.value = ''
}
function commitEdit() {
  const i = editIdx.value
  if (i < 0) return
  const url = editUrl.value.trim()
  if (!url) { toast('音源 URL 不能为空'); return }
  if (!validUrl(url)) { toast('音源 URL 需以 http(s) 开头'); return }
  if (scripts.value.some((s, k) => k !== i && s.url === url)) { toast('该接口已在列表中'); return }
  const name = editName.value.trim() || guessName(url)
  scripts.value[i] = { name, url }
  cancelEdit()
}

// 重试加载:配置未保存时地址可能已变,先提示保存
async function retryScript(sc: ScriptItem) {
  if (retrying.value[sc.url]) return
  if (dirty.value) { toast('请先点击底部「保存设置」,再重试加载'); return }
  retrying.value[sc.url] = true
  try {
    await api.post('/api/admin/scripts/retry', { url: sc.url })
    toast('正在重新加载,请稍候…')
    if (disposed) return
    const timer = window.setTimeout(async () => {
      retryTimers.delete(sc.url)
      try {
        await load(true)
        if (!disposed) await player.loadSources()
      } finally { retrying.value[sc.url] = false }
    }, 8000)
    retryTimers.set(sc.url, timer)
  } catch (e: any) {
    retrying.value[sc.url] = false
    toast(e?.message || '重试失败')
  }
}

/* ---------- 音频白名单:增删改 ---------- */
const editHost = ref('')      // 正在编辑的原值
const editHostValue = ref('')
const savingHost = ref(false)

async function addHost() {
  const host = newHost.value.trim()
  if (!host || addingHost.value) return
  addingHost.value = true
  try {
    const d = await api.post('/api/admin/stream-hosts', { host })
    streamHosts.value = d.list || []
    newHost.value = ''
    toast('已添加')
  } catch (e: any) {
    toast(e?.message || '添加失败')
  } finally {
    addingHost.value = false
  }
}

async function removeHost(host: string) {
  try {
    const d = await api.del('/api/admin/stream-hosts?host=' + encodeURIComponent(host))
    streamHosts.value = d.list || []
    toast('已删除')
  } catch (e: any) {
    toast(e?.message || '删除失败')
  }
}

function startHostEdit(h: string) {
  editHost.value = h
  editHostValue.value = h
}
function cancelHostEdit() {
  editHost.value = ''
  editHostValue.value = ''
}
async function commitHostEdit() {
  const old = editHost.value
  const val = editHostValue.value.trim()
  if (!old) return
  if (!val) { toast('域名不能为空'); return }
  if (val === old) { cancelHostEdit(); return }
  if (savingHost.value) return
  savingHost.value = true
  try {
    const d = await api.put('/api/admin/stream-hosts', { old, host: val })
    streamHosts.value = d.list || []
    cancelHostEdit()
    toast('已保存')
  } catch (e: any) {
    toast(e?.message || '保存失败')
  } finally {
    savingHost.value = false
  }
}

/* ---------- 保存(注册开关 + 采集接口) ---------- */
async function save() {
  if (saving.value) return
  if (editIdx.value >= 0) { toast('请先完成正在编辑的采集接口'); return }
  if (inviteRequired.value && !inviteCode.value.trim()) {
    toast('开启邀请码后必须设置邀请码')
    return
  }
  saving.value = true
  try {
    const body: Record<string, unknown> = {
      allowRegister: allowRegister.value,
      inviteRequired: inviteRequired.value,
      inviteCode: inviteCode.value.trim(),
    }
    const changed = snapshot(scripts.value) !== initialScripts
    if (changed) {
      body.scripts = scripts.value.map((s) => ({ name: s.name, url: s.url }))
    }
    const savedScripts = snapshot(scripts.value)
    await api.put('/api/admin/settings', body)
    if (disposed) return
    toast(changed ? '已保存,采集接口正在后台重载…' : '已保存')
    initialScripts = savedScripts
    if (refreshTimer) window.clearTimeout(refreshTimer)
    // 重载要下载十几个脚本,给足时间再刷新状态
    refreshTimer = window.setTimeout(async () => {
      refreshTimer = 0
      try {
        await player.loadSources()
        await load(true)
        if (disposed) return
        toast('采集接口状态已刷新')
      } catch { /* ignore */ }
    }, changed ? 15000 : 2000)
  } catch (e: any) {
    toast(e?.message || '保存失败')
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="page sys">
    <MBack />
    <h2 class="page-title"><Icon name="sliders" :size="20" style="color: var(--accent); vertical-align: -3px" /> 系统设置</h2>

    <!-- 前台注册 -->
    <section class="set-card">
      <h3><Icon name="users" :size="16" /> 前台注册</h3>
      <div class="set-row">
        <div>
          <div>允许新用户注册</div>
          <div class="tip2">关闭后登录页将隐藏注册入口,仅已有账户可登录</div>
        </div>
        <button class="switch" :class="{ on: allowRegister }" @click="allowRegister = !allowRegister">
          <span class="knob"></span>
        </button>
      </div>
      <div class="set-row">
        <div>
          <div>注册需要邀请码</div>
          <div class="tip2">开启后,前台注册需填写正确的邀请码</div>
        </div>
        <button class="switch" :class="{ on: inviteRequired }" @click="inviteRequired = !inviteRequired">
          <span class="knob"></span>
        </button>
      </div>
      <div class="set-row" v-if="inviteRequired">
        <span>邀请码</span>
        <input v-model="inviteCode" class="input invite-input" placeholder="设置注册邀请码" />
      </div>
    </section>

    <!-- 资源缓存 -->
    <section class="set-card" aria-labelledby="cache-title">
      <h3 id="cache-title"><Icon name="database" :size="16" /> 资源缓存</h3>
      <p class="tip">缓存播放地址、搜索、歌单、歌词和封面，音频文件不缓存。空间不足时先清理过期数据，再淘汰命中次数少的条目。</p>
      <p v-if="cacheInfo" class="cache-stats" aria-live="polite">已用 {{ formatBytes(cacheInfo.bytes) }} / {{ formatBytes(cacheInfo.limit) }} · {{ cacheInfo.entries }} 条 · 命中 {{ cacheInfo.hits }} 次 · 淘汰 {{ cacheInfo.evictions }} 条</p>
      <form class="cache-size-form" @submit.prevent="saveCacheSize">
        <label for="cache-size">缓存空间上限</label>
        <div class="cache-size-controls">
          <div class="cache-size-input">
            <input id="cache-size" v-model="cacheSizeMB" class="input" type="number" inputmode="numeric" min="1" max="1048576" step="1" required :disabled="!!cacheBusy || !cacheInfo" aria-describedby="cache-size-tip" />
            <span>MB</span>
          </div>
          <button class="btn small" type="submit" :disabled="!!cacheBusy || !cacheInfo">{{ cacheBusy === 'saving' ? '保存中…' : '保存大小' }}</button>
        </div>
      </form>
      <p id="cache-size-tip" class="tip2">默认 3072 MB。保存后立即生效；调小容量会自动清理超出的缓存。</p>
      <div class="cache-actions">
        <button class="btn ghost small" :disabled="!!cacheBusy" @click="loadCache"><Icon name="refresh" :size="14" /> {{ cacheBusy === 'loading' ? '刷新中…' : '刷新缓存统计' }}</button>
        <button class="btn ghost small cache-clear" :disabled="!!cacheBusy || !cacheInfo" @click="clearCache"><Icon name="trash" :size="14" /> {{ cacheBusy === 'clearing' ? '清空中…' : '清空缓存' }}</button>
      </div>
    </section>

    <!-- 采集接口(多音源,可排序/编辑) -->
    <section class="set-card">
      <h3><Icon name="globe" :size="16" /> 采集接口(音源)</h3>
      <p class="tip">
        列表第一个为<b>主音源</b>。主音源取不到播放地址时,会在其余音源中随机顺序逐个尝试(每个只试一次);
        全部失败则提示「当前歌曲没有可用资源」。拖动行可调整顺序。
      </p>
      <ul class="src-list">
        <li
          v-for="(sc, i) in scripts"
          :key="sc.url"
          class="src-row"
          :class="{ primary: i === 0, dragging: dragIndex === i, editing: editIdx === i }"
          :draggable="editIdx === i ? 'false' : 'true'"
          @dragstart="onDragStart(i, $event)"
          @dragover.prevent
          @drop.prevent="onDrop(i)"
          @dragend="dragIndex = -1"
        >
          <!-- 编辑态 -->
          <template v-if="editIdx === i">
            <span class="src-idx">{{ i + 1 }}</span>
            <div class="src-edit">
              <input v-model="editName" class="input sm" placeholder="音源名称" />
              <input
                v-model="editUrl"
                class="input sm mono"
                placeholder="音源 URL(以 http(s):// 开头)"
                @keyup.enter="commitEdit"
              />
            </div>
            <span class="row-ops">
              <button class="mini" title="保存修改" @click="commitEdit"><Icon name="check" :size="14" /></button>
              <button class="mini" title="取消" @click="cancelEdit"><Icon name="x" :size="14" /></button>
            </span>
          </template>

          <!-- 展示态 -->
          <template v-else>
            <span class="drag-handle" title="拖动排序">⋮⋮</span>
            <span class="src-idx">{{ i + 1 }}</span>
            <div class="src-main">
              <div class="src-name">
                {{ sc.name }}
                <span v-if="i === 0" class="badge">主音源</span>
                <span v-if="sc.version" class="src-ver">v{{ sc.version }}</span>
              </div>
            </div>
            <span class="src-state" :class="sc.ready ? 'ok' : 'bad'">
              {{ sc.ready ? '正常' : (sc.error || '待保存') }}
            </span>
            <span class="row-ops">
              <button
                class="mini"
                title="重新加载此脚本"
                :disabled="retrying[sc.url]"
                @click="retryScript(sc)"
              >
                <Icon name="refresh" :size="14" />
              </button>
              <button class="mini" title="编辑" @click="startEdit(i)"><Icon name="edit" :size="14" /></button>
              <button class="mini danger" title="移除" @click="removeScript(i)"><Icon name="trash" :size="14" /></button>
            </span>
          </template>
        </li>
      </ul>

      <div class="script-add">
        <input v-model="newName" class="input" placeholder="音源名称(留空自动识别)" />
        <input
          v-model="newUrl"
          class="input mono"
          placeholder="音源 URL(以 http(s):// 开头)"
          @keyup.enter="addScript"
        />
        <button class="btn" @click="addScript"><Icon name="plus" :size="16" /> 添加</button>
      </div>
      <p class="tip2">当前主音源:{{ primaryName }}</p>
      <p class="tip2 dirty" v-if="dirty">有未保存的修改,点击底部「保存设置」后生效</p>
    </section>

    <!-- 音频代理白名单 -->
    <section class="set-card">
      <h3><Icon name="globe" :size="16" /> 音频域名白名单</h3>
      <p class="tip">
        音频代理(/api/stream)只转发下列域名的直链,防止本站被当成开放代理。
        支持 <code>*.example.com</code> 通配所有子域名,单条 <code>*</code> 表示放开全部(不推荐)。
      </p>
      <div class="host-add">
        <input
          v-model="newHost"
          class="input"
          placeholder="如 *.example.com 或 cdn.example.com"
          @keyup.enter="addHost"
        />
        <button class="btn" :disabled="addingHost" @click="addHost">
          <Icon name="plus" :size="16" /> {{ addingHost ? '添加中…' : '添加' }}
        </button>
      </div>
      <ul class="host-list">
        <li v-for="h in streamHosts" :key="h">
          <template v-if="editHost === h">
            <input
              v-model="editHostValue"
              class="input sm mono"
              placeholder="如 *.example.com"
              @keyup.enter="commitHostEdit"
            />
            <span class="row-ops">
              <button class="mini" title="保存修改" :disabled="savingHost" @click="commitHostEdit">
                <Icon name="check" :size="14" />
              </button>
              <button class="mini" title="取消" @click="cancelHostEdit"><Icon name="x" :size="14" /></button>
            </span>
          </template>
          <template v-else>
            <span class="host-name">{{ h }}</span>
            <span class="row-ops">
              <button class="mini" title="编辑" @click="startHostEdit(h)"><Icon name="edit" :size="14" /></button>
              <button class="mini danger" title="移除" @click="removeHost(h)"><Icon name="trash" :size="14" /></button>
            </span>
          </template>
        </li>
      </ul>
      <p class="tip2" v-if="!streamHosts.length">白名单为空:所有音频代理请求都会被拒绝。</p>
    </section>

    <button class="btn primary save-btn" :disabled="saving" @click="save">
      <Icon name="check" :size="16" /> {{ saving ? '保存中…' : '保存设置' }}
    </button>
  </div>
</template>

<style scoped>

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
  color: var(--text);
}
.set-card h3 :deep(svg) { color: var(--accent); }
.set-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 7px 0;
  font-size: 14px;
}
.ok { color: #3ecf6f; }
.bad { color: var(--danger); }
.tip { font-size: 12px; color: var(--text-dim); margin: 0; line-height: 1.7; }
.tip2 { font-size: 12px; color: var(--text-dim); margin-top: 6px; }
.tip2.dirty { color: #ffb454; }
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
.save-btn { width: 100%; justify-content: center; padding: 12px 0; font-size: 15px; }
.invite-input { width: 220px; text-align: center; letter-spacing: 2px; }

.cache-stats { font-size: 13px; line-height: 1.8; color: var(--text-dim); }
.cache-size-form { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; margin: 16px 0 8px; }
.cache-size-controls, .cache-size-input, .cache-actions { display: flex; align-items: center; gap: 8px; }
.cache-size-input { color: var(--text-dim); }
.cache-size-input .input { width: 128px; min-width: 0; }
.cache-actions { flex-wrap: wrap; margin-top: 16px; }
.cache-clear { color: var(--danger); }
.cache-size-controls .btn, .cache-actions .btn { min-height: 36px; }

/* 采集脚本列表 */
.src-list { list-style: none; margin: 12px 0 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
.src-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  border-radius: 10px;
  background: var(--bg-soft);
  border: 1px solid var(--border);
  font-size: 13.5px;
  cursor: grab;
  transition: border-color 0.15s, opacity 0.15s;
}
.src-row.primary { border-color: color-mix(in srgb, var(--accent) 55%, var(--border)); }
.src-row.dragging { opacity: 0.45; }
.src-row.editing { cursor: default; border-color: var(--accent); }
.drag-handle { color: var(--text-dim); letter-spacing: -2px; user-select: none; cursor: grab; }
.src-idx { width: 20px; color: var(--text-dim); font-size: 12.5px; text-align: center; flex-shrink: 0; }
.src-main { flex: 1; min-width: 0; }
.src-name { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; }
.badge {
  font-size: 10.5px;
  padding: 0 6px;
  border-radius: 4px;
  background: var(--accent);
  color: #fff;
  font-weight: 600;
  line-height: 1.7;
}
.src-ver { font-size: 11px; color: var(--text-dim); }
.src-state {
  font-size: 12px;
  flex-shrink: 0;
  max-width: 150px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.src-edit { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 6px; }
.row-ops { display: inline-flex; gap: 4px; flex-shrink: 0; }
.mini {
  width: 26px;
  height: 26px;
  border-radius: 7px;
  border: 1px solid var(--border);
  background: transparent;
  color: var(--text-dim);
  font-size: 13px;
  line-height: 1;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}
.mini:disabled { opacity: 0.35; }
.mini:hover { color: var(--accent); border-color: var(--accent); }
.mini.danger:hover { color: var(--danger); border-color: var(--danger); }

/* 添加采集接口 */
.script-add {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.6fr) auto;
  gap: 8px;
  margin: 12px 0 4px;
}
.script-add .input { width: 100%; min-width: 0; }

.input.sm { padding: 6px 10px; font-size: 13px; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }

/* 音频域名白名单 */
.host-add { display: flex; gap: 8px; margin: 12px 0 4px; }
.host-add .input { flex: 1; min-width: 0; }
.host-list {
  list-style: none;
  margin: 10px 0 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.host-list li {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 8px 12px;
  border-radius: 10px;
  background: var(--bg-soft);
  border: 1px solid var(--border);
  font-size: 13.5px;
}
.host-name { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; word-break: break-all; }
.host-list .input.sm { flex: 1; min-width: 0; }

@media (max-width: 560px) {
  .script-add { grid-template-columns: 1fr; }
  .script-add .btn { justify-content: center; }
  .src-state { display: none; }
  .src-edit { gap: 8px; }
}
</style>
