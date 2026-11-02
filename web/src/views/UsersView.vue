<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../api'
import Icon from '../components/Icon.vue'
import MBack from '../components/MBack.vue'
import { confirmDialog, promptDialog } from '../dialog'
import { toast } from '../toast'

interface UserRow {
  id: number
  username: string
  isAdmin: boolean
  createdAt: number
  remark: string
  favCount: number
  blackCount: number
  historyCount: number
  listenCount: number
}

const users = ref<UserRow[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const d = await api.get('/api/admin/users')
    users.value = d.list || []
  } catch (e: any) { toast(e?.message || '加载失败') } finally {
    loading.value = false
  }
}
onMounted(load)

async function remove(u: UserRow) {
  if (u.isAdmin || u.username === 'admin') {
    toast('超级管理员账户不可删除')
    return
  }
  if (!(await confirmDialog(`删除用户「${u.username}」?其收藏 ${u.favCount} 首、拉黑 ${u.blackCount} 首、播放历史 ${u.historyCount} 条将一并删除。`, { title: '删除用户', danger: true }))) return
  try {
    await api.del(`/api/admin/users/${u.id}`)
    users.value = users.value.filter((x) => x.id !== u.id)
    toast('已删除')
  } catch (e: any) { toast(e?.message || '删除失败') }
}

async function resetPassword(u: UserRow) {
  const pw = await promptDialog(`为用户「${u.username}」设置新密码(至少 6 位):`, { title: '重置密码', placeholder: '新密码' })
  if (pw === null) return
  if (!pw || pw.length < 6) { toast('密码至少 6 位'); return }
  try {
    await api.post(`/api/admin/users/${u.id}/password`, { password: pw })
    toast(`已重置「${u.username}」的密码`)
  } catch (e: any) { toast(e?.message || '重置失败') }
}

async function editRemark(u: UserRow) {
  const r = await promptDialog(`为用户「${u.username}」设置备注(最多 30 字,清空即删除):`, {
    title: '用户备注',
    input: u.remark || '',
    placeholder: '输入备注',
  })
  if (r === null) return
  try {
    const d = await api.put(`/api/admin/users/${u.id}/remark`, { remark: r.trim() })
    u.remark = d.remark || ''
    toast(u.remark ? '备注已保存' : '备注已删除')
  } catch (e: any) { toast(e?.message || '保存失败') }
}

function fmtDate(ts: number): string {
  const d = new Date(ts * 1000)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}
</script>

<template>
  <div class="page">
    <MBack />
    <h2 class="page-title">
      <Icon name="users" :size="20" style="color: var(--accent); vertical-align: -3px" />
      用户列表<span class="count-chip">{{ users.length }} 人</span>
    </h2>

    <div v-if="loading" class="loading-line">加载中…</div>
    <div v-else class="user-list">
      <div v-for="u in users" :key="u.id" class="user-row">
        <span class="u-avatar"><Icon name="user" :size="16" /></span>
        <span class="u-name">
          {{ u.username }}
          <em v-if="u.isAdmin" class="u-badge">超管</em>
          <em v-if="u.remark" class="u-remark">{{ u.remark }}</em>
        </span>
        <span class="u-stats">
          <span class="u-stat" title="收藏歌曲"><Icon name="heart" :size="13" /> {{ u.favCount }}</span>
          <span class="u-stat" title="拉黑歌曲"><Icon name="ban" :size="13" /> {{ u.blackCount }}</span>
          <span class="u-stat" title="播放历史"><Icon name="clock" :size="13" /> {{ u.historyCount }}</span>
          <span class="u-stat" title="累计有效播放次数，清空历史不会归零"><Icon name="disc" :size="13" /> 累计听歌 {{ (u.listenCount ?? 0).toLocaleString('zh-CN') }} 次</span>
        </span>
        <span class="u-date">{{ fmtDate(u.createdAt) }}</span>
        <span class="u-ops">
          <button class="btn small" @click="editRemark(u)"><Icon name="edit" :size="13" /> 备注</button>
          <button class="btn small" @click="resetPassword(u)" :disabled="u.isAdmin"><Icon name="key" :size="13" /> 改密</button>
          <button class="btn small danger" :disabled="u.isAdmin" @click="remove(u)" :title="u.isAdmin ? '超级管理员不可删除' : '删除用户'">
            <Icon name="trash" :size="13" /> 删除
          </button>
        </span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.page-title { display: flex; align-items: center; gap: 8px; }
.user-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.user-row {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 11px 14px;
  background: color-mix(in srgb, var(--panel) 68%, transparent);
  backdrop-filter: blur(20px) saturate(1.2);
  -webkit-backdrop-filter: blur(20px) saturate(1.2);
  border: 1px solid var(--border);
  border-radius: 12px;
}
.u-avatar {
  width: 34px;
  height: 34px;
  border-radius: 50%;
  background: var(--bg-soft);
  color: var(--text-dim);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.u-name {
  min-width: 90px;
  font-size: 14.5px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  white-space: nowrap;
}
.u-badge,
.u-remark {
  font-style: normal;
  font-size: 10.5px;
  border-radius: 4px;
  padding: 0 5px;
  margin-left: 6px;
  display: inline-flex;
  align-items: center;
  line-height: 1.6;
  flex-shrink: 0;
}
.u-badge { color: var(--accent); border: 1px solid var(--accent); }
.u-remark {
  color: var(--accent);
  border: 1px solid var(--accent);
  max-width: 140px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.u-stats { flex: 1; display: flex; flex-wrap: wrap; gap: 8px 14px; }
.u-stat {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12.5px;
  color: var(--text-dim);
}
.u-stat svg { color: var(--text-dim); }
.u-date { font-size: 12.5px; color: var(--text-dim); }
.u-ops { display: flex; gap: 6px; }
@media (max-width: 768px) {
  .u-date { display: none; }
  .user-row { flex-wrap: wrap; row-gap: 8px; padding: 12px 12px; }
  .u-name { min-width: 0; flex: 1; }
  .u-stats { gap: 10px; }
  .u-ops { order: 10; width: 100%; justify-content: flex-end; }
}
</style>
