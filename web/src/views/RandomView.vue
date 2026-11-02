<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../api'
import { Song } from '../types'
import { usePlayer } from '../stores/player'
import SongList from '../components/SongList.vue'
import Icon from '../components/Icon.vue'
import { toast } from '../toast'

const player = usePlayer()
const songs = ref<Song[]>([])
const loading = ref(false)

// 仅生成随机歌单列表,不自动播放
async function generate() {
  if (loading.value) return
  loading.value = true
  try {
    const d = await api.get('/api/random-songs?count=100')
    songs.value = d.list || []
    if (!songs.value.length) toast('随机歌单生成失败,请重试')
  } catch (e: any) {
    toast(e?.message || '生成失败,请重试')
  } finally {
    loading.value = false
  }
}

function playAll() {
  if (songs.value.length) {
    player.setMode('loop')
    player.playAll(songs.value, 0)
    player.overlayOpen = true
  }
}

onMounted(generate)
</script>

<template>
  <div class="page">
    <div class="hero">
      <div class="hero-icon"><Icon name="shuffle" :size="30" /></div>
      <h2 class="page-title">随机歌单</h2>
      <p class="hero-sub">每次换一批 {{ songs.length || 100 }} 首随机歌曲,列表循环播放</p>
      <div class="hero-ops">
        <button class="btn" :disabled="loading" @click="generate">
          <Icon :name="loading ? 'disc' : 'refresh'" :size="15" :class="{ spinning: loading }" />
          {{ loading ? '生成中…' : '换一批' }}
        </button>
        <button class="btn primary" :disabled="!songs.length || loading" @click="playAll">
          <Icon name="play" :size="14" filled />
          全部播放
        </button>
      </div>
    </div>

    <div v-if="loading && !songs.length" class="loading-line">正在为你挑选歌曲…</div>
    <SongList v-else-if="songs.length" :songs="songs" />
  </div>
</template>

<style scoped>
.hero {
  text-align: center;
  padding: 26px 0 22px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
}
.hero-icon {
  width: 62px;
  height: 62px;
  border-radius: 18px;
  background: var(--accent-grad);
  color: #35240a;
  display: flex;
  align-items: center;
  justify-content: center;
  margin-bottom: 4px;
  box-shadow: 0 10px 30px color-mix(in srgb, var(--accent) 35%, transparent);
}
.page-title { margin: 0; }
.hero-sub { margin: 0 0 8px; color: var(--text-dim); font-size: 13.5px; }
.hero-ops { display: flex; gap: 10px; margin-top: 4px; }
.spinning { animation: rot 1s linear infinite; }
@keyframes rot { to { transform: rotate(360deg); } }
</style>
