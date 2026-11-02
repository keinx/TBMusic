<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { loadLibrarySongs } from '../utils/libraryPages'
import { songKey, Song, SongEntry } from '../types'
import { useLibrary } from '../stores/library'
import { usePlayer } from '../stores/player'
import SongList from '../components/SongList.vue'
import CollectDialog from '../components/CollectDialog.vue'
import Icon from '../components/Icon.vue'
import { confirmDialog } from '../dialog'
import { toast } from '../toast'
import { sessionVersion } from '../api'

const route = useRoute()
const library = useLibrary()
const player = usePlayer()

const entries = ref<SongEntry[]>([])
const playlistName = ref('')
const loading = ref(false)
const transferTarget = ref<Song | null>(null)

const playlistId = () => parseInt(route.params.id as string, 10)

let loadSeq = 0
async function load() {
  const seq = ++loadSeq
  loading.value = true
  try {
    const list = await loadLibrarySongs(`/api/playlists/${playlistId()}/songs`)
    if (seq !== loadSeq) return
    entries.value = list
    const p = library.playlists.find((x) => x.id === playlistId())
    playlistName.value = p?.name || '歌单'
  } catch (e: any) {
    if (seq === loadSeq) toast(e?.message || '加载失败')
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

onMounted(load)
watch(() => route.params.id, load)
onBeforeUnmount(() => { loadSeq++ })

async function onRemove(song: Song) {
  const sourceId = playlistId(), sourceSeq = loadSeq, session = sessionVersion()
  const entry = findEntry(song)
  if (!entry) return
  if (!(await confirmDialog(`从歌单移除「${song.singer} - ${song.name}」?`, { title: '移除歌曲', danger: true }))) return
  if (session !== sessionVersion()) return
  try {
    await library.removeFromPlaylist(sourceId, entry.id)
    if (loadSeq === sourceSeq && playlistId() === sourceId) entries.value = entries.value.filter((x) => x.id !== entry.id)
    toast('已从歌单移除')
  } catch (e: any) { toast(e?.message || '移除失败') }
}

function findEntry(song: Song): SongEntry | undefined {
  return entries.value.find((e) =>
    songKey(e.musicInfo) === songKey(song))
}

// 转移:加入目标歌单 + 从当前歌单移除
async function onMove(targetId: number) {
  const song = transferTarget.value
  if (!song) return
  const sourceId = playlistId(), sourceSeq = loadSeq, session = sessionVersion()
  const entry = findEntry(song)
  try {
    await library.addToPlaylist(targetId, song)
    if (session !== sessionVersion()) return
    if (entry) {
      await library.removeFromPlaylist(sourceId, entry.id)
      if (loadSeq === sourceSeq && playlistId() === sourceId) entries.value = entries.value.filter((x) => x.id !== entry.id)
    }
  } catch (e: any) { toast(e?.message || '转移失败') }
}

function playAll() {
  const songs = entries.value.map((e) => e.musicInfo)
  if (songs.length) player.playAll(songs, 0)
}
</script>

<template>
  <div class="page">
    <div class="head-row">
      <div>
        <button class="btn small" @click="$router.back()"><Icon name="chevron-left" :size="14" /> 返回</button>
        <h2 class="page-title">{{ playlistName }}<span class="count-chip">{{ entries.length }} 首</span></h2>
      </div>
      <button class="btn primary" v-if="entries.length" @click="playAll"><Icon name="play" :size="13" filled /> 播放全部</button>
    </div>

    <div v-if="loading" class="loading-line">加载中…</div>
    <SongList v-else-if="entries.length" :songs="entries.map(e => e.musicInfo)" mode="playlist" @remove="onRemove" @transfer="transferTarget = $event" />
    <div v-else class="empty">歌单是空的,去搜索页收藏喜欢的歌吧</div>

    <CollectDialog
      v-if="transferTarget"
      :song="transferTarget"
      :from-playlist-id="playlistId()"
      @close="transferTarget = null"
      @move="onMove"
    />
  </div>
</template>

<style scoped>
.head-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 16px; flex-wrap: wrap; }
.page-title { margin: 8px 0 0; }
</style>
