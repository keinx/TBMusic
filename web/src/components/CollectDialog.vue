<script setup lang="ts">
import { ref, computed } from 'vue'
import { Song } from '../types'
import { useLibrary } from '../stores/library'
import { toast } from '../toast'
import Icon from './Icon.vue'

// 转移歌单弹窗:选择目标歌单,实际移动由父页面在 move 事件中完成
const props = defineProps<{ song: Song; fromPlaylistId: number }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'move', targetId: number): void }>()
const library = useLibrary()

const newName = ref('')
const creating = ref(false)

const targets = computed(() =>
  library.playlists.filter((p) => p.id !== props.fromPlaylistId)
)

function choose(p: { id: number }) {
  emit('move', p.id)
  emit('close')
}

async function createAndMove() {
  const name = newName.value.trim()
  if (!name) { toast('请输入歌单名'); return }
  try {
    const p = await library.createPlaylist(name)
    if (p) {
      newName.value = ''
      creating.value = false
      // 与函数名/按钮语义一致:建好后直接转移到该歌单
      emit('move', p.id)
      emit('close')
    }
  } catch (e: any) { toast(e?.message || '创建失败') }
}
</script>

<template>
  <div class="mask" @click.self="emit('close')">
    <div class="dialog">
      <h3 class="dlg-title"><Icon name="transfer" :size="17" style="color: var(--accent)" /> 转移到歌单</h3>
      <p class="dlg-song">{{ song.singer }} - {{ song.name }}</p>

      <div class="plist">
        <button v-for="p in targets" :key="p.id" class="pl-item" @click="choose(p)">
          <span class="pl-icon"><Icon :name="p.isDefault ? 'star' : 'music'" :size="16" :filled="p.isDefault" /></span>
          <span class="pl-name">
            {{ p.name }}<em v-if="p.isDefault" class="pl-def">默认</em>
          </span>
          <span class="pl-count">{{ p.count }} 首</span>
        </button>
        <p v-if="!targets.length" class="empty" style="padding: 20px 0">没有其他歌单,先新建一个吧</p>
      </div>

      <div class="dlg-foot">
        <template v-if="!creating">
          <button class="btn" @click="creating = true"><Icon name="plus" :size="14" /> 新建歌单</button>
          <button class="btn" @click="emit('close')">取消</button>
        </template>
        <template v-else>
          <input v-model="newName" class="input" placeholder="新歌单名称" @keyup.enter="createAndMove" />
          <button class="btn primary" @click="createAndMove">创建</button>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.mask {
  position: fixed;
  inset: 0;
  z-index: 1500;
  background: rgba(8, 8, 14, 0.55);
  backdrop-filter: blur(4px);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 18px;
}
.dialog {
  width: 420px;
  max-width: 100%;
  max-height: 82vh;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  gap: 12px;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 16px;
  padding: 20px;
  box-shadow: var(--shadow);
}
.dlg-title { margin: 0; font-size: 17px; display: flex; align-items: center; gap: 8px; }
.dlg-song { margin: -6px 0 0; font-size: 13px; color: var(--text-dim); }
.plist { overflow-y: auto; display: flex; flex-direction: column; gap: 2px; min-height: 120px; }
.pl-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 10px;
  border-radius: 9px;
  color: inherit;
  text-align: left;
}
.pl-item:hover { background: var(--panel-hover); }
.pl-icon {
  width: 34px; height: 34px;
  display: flex; align-items: center; justify-content: center;
  border-radius: 9px;
  background: var(--accent-soft);
  color: var(--accent);
  flex-shrink: 0;
}
.pl-name { flex: 1; font-size: 14px; }
.pl-def {
  font-style: normal;
  font-size: 10.5px;
  color: var(--accent);
  border: 1px solid var(--accent);
  padding: 0 5px;
  border-radius: 4px;
  margin-left: 6px;
}
.pl-count { font-size: 12px; color: var(--text-dim); }
.dlg-foot { display: flex; gap: 8px; }
.dlg-foot .input { flex: 1; }
</style>
