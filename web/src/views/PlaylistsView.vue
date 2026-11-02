<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useLibrary } from '../stores/library'
import { toast } from '../toast'
import Icon from '../components/Icon.vue'
import { confirmDialog, promptDialog } from '../dialog'

const router = useRouter()
const library = useLibrary()
const creating = ref(false)
const newName = ref('')

onMounted(() => { library.loadPlaylists() })

async function create() {
  const name = newName.value.trim()
  if (!name) { toast('请输入歌单名'); return }
  try {
    const p = await library.createPlaylist(name)
    if (p) toast(`已创建「${name}」`)
    newName.value = ''
    creating.value = false
  } catch (e: any) { toast(e?.message || '创建失败') }
}

async function remove(id: number, name: string, isDefault: boolean) {
  if (isDefault) { toast('默认歌单不可删除'); return }
  if (!(await confirmDialog(`删除歌单「${name}」?其中的收藏不会被删除其他歌单里的副本。`, { title: '删除歌单', danger: true }))) return
  try { await library.deletePlaylist(id) } catch (e: any) { toast(e?.message || '删除失败') }
}

async function rename(id: number, name: string) {
  const nn = await promptDialog('修改歌单名称:', { title: '重命名歌单', input: name })
  if (nn === null || !nn.trim() || nn.trim() === name) return
  try {
    await library.renamePlaylist(id, nn.trim())
    toast('已重命名')
  } catch (e: any) { toast(e?.message || '重命名失败') }
}
</script>

<template>
  <div class="page">
    <div class="head-row">
      <h2 class="page-title">我的收藏</h2>
      <button class="btn primary" @click="creating = !creating"><Icon name="plus" :size="15" /> 新建歌单</button>
    </div>

    <div v-if="creating" class="create-row">
      <input v-model="newName" class="input" placeholder="歌单名称" @keyup.enter="create" />
      <button class="btn primary" @click="create">创建</button>
    </div>

    <div v-if="library.playlists.length" class="pl-grid">
      <div v-for="p in library.playlists" :key="p.id" class="pl-card" @click="router.push(`/playlists/${p.id}`)">
        <div class="pl-badge"><Icon :name="p.isDefault ? 'star' : 'music'" :size="18" :filled="p.isDefault" /></div>
        <div class="pl-info">
          <div class="pl-name">
            {{ p.name }}
            <em v-if="p.isDefault" class="pl-def">默认</em>
          </div>
          <div class="pl-count">{{ p.count }} 首</div>
        </div>
        <div class="pl-ops" @click.stop>
          <button v-if="!p.isDefault" class="icon-btn" title="重命名" @click="rename(p.id, p.name)"><Icon name="edit" :size="15" /></button>
          <button v-if="!p.isDefault" class="icon-btn" title="删除" @click="remove(p.id, p.name, p.isDefault)"><Icon name="trash" :size="15" /></button>
        </div>
      </div>
    </div>
    <div v-else class="empty">还没有歌单,点右上角新建一个吧</div>
  </div>
</template>

<style scoped>
.head-row { display: flex; align-items: center; justify-content: space-between; margin-bottom: 14px; }
.head-row .page-title { margin: 0; }
.create-row { display: flex; gap: 8px; margin-bottom: 16px; }
.create-row .input { flex: 1; }
.pl-grid { display: flex; flex-direction: column; gap: 8px; }
.pl-card {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 13px 16px;
  background: color-mix(in srgb, var(--panel) 68%, transparent);
  backdrop-filter: blur(20px) saturate(1.2);
  -webkit-backdrop-filter: blur(20px) saturate(1.2);
  border: 1px solid var(--border);
  border-radius: 14px;
  cursor: pointer;
  transition: background 0.15s;
}
.pl-card:hover { border-color: var(--accent); }
.pl-badge {
  width: 44px; height: 44px;
  border-radius: 12px;
  background: var(--accent-soft);
  color: var(--accent);
  display: flex; align-items: center; justify-content: center;
  font-size: 20px;
  flex-shrink: 0;
}
.pl-info { flex: 1; min-width: 0; }
.pl-name { font-size: 15px; }
.pl-def {
  font-style: normal;
  font-size: 10.5px;
  color: var(--accent);
  border: 1px solid var(--accent);
  padding: 0 5px;
  border-radius: 4px;
  margin-left: 6px;
  vertical-align: 1px;
}
.pl-count { font-size: 12.5px; color: var(--text-dim); margin-top: 2px; }
.pl-ops { display: flex; gap: 2px; }
</style>
