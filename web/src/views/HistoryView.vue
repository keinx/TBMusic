<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../api'
import { useLibrary } from '../stores/library'
import { usePlayer } from '../stores/player'
import SongList from '../components/SongList.vue'
import Icon from '../components/Icon.vue'
import MBack from '../components/MBack.vue'
import { confirmDialog } from '../dialog'
import { toast } from '../toast'

const library = useLibrary()
const player = usePlayer()
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    await library.loadHistory()
  } catch { /* ignore */ } finally {
    loading.value = false
  }
}

onMounted(load)

async function clearAll() {
  if (!(await confirmDialog('确定清空全部播放历史?', { title: '清空历史', danger: true }))) return
  try {
    await library.clearHistory()
    toast('已清空')
  } catch (e: any) { toast(e?.message || '操作失败') }
}

function replay() {
  const songs = library.history.map((h) => h.musicInfo)
  if (songs.length) player.playAll(songs, 0)
}
</script>

<template>
  <div class="page">
    <MBack />
    <div class="head-row">
      <h2 class="page-title">
        <Icon name="clock" :size="20" style="color: var(--accent); vertical-align: -3px" />
        播放历史<span class="count-chip">{{ library.history.length }} 首</span>
      </h2>
      <div class="ops">
        <button class="btn" v-if="library.history.length" @click="replay"><Icon name="play" :size="14" filled /> 全部重播</button>
        <button class="btn danger" v-if="library.history.length" @click="clearAll"><Icon name="trash" :size="14" /> 清空</button>
      </div>
    </div>

    <div v-if="loading" class="loading-line">加载中…</div>
    <SongList v-else-if="library.history.length" :songs="library.history.map(h => h.musicInfo)" />
    <div v-else class="empty">暂无播放历史</div>
  </div>
</template>

<style scoped>
.head-row { display: flex; align-items: center; justify-content: space-between; gap: 10px; margin-bottom: 14px; flex-wrap: wrap; }
.page-title { display: flex; align-items: center; gap: 8px; margin: 0; }
.ops { display: flex; gap: 8px; align-items: center; flex-wrap: nowrap; }
.ops .btn { padding: 8px 16px; }
</style>
