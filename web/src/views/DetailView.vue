<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api'
import { queryString } from '../utils/query'
import { Song, SOURCE_NAMES } from '../types'
import { usePlayer } from '../stores/player'
import SongList from '../components/SongList.vue'
import Icon from '../components/Icon.vue'
import CoverImage from '../components/CoverImage.vue'
import { toast } from '../toast'

const route = useRoute()
const router = useRouter()
const player = usePlayer()

const type = ref('album')
const source = ref('kw')
const id = ref('')
const title = ref('')
const pic = ref('')
const songs = ref<Song[]>([])
const loading = ref(false)

const typeNames: Record<string, string> = { album: '专辑', songlist: '歌单', artist: '歌手' }

const hasMore = ref(false)
const page = ref(0)
const total = ref(0)
const playingAll = ref(false)
let loadSeq = 0

async function loadMore(seq = loadSeq): Promise<boolean> {
  if (loading.value || seq !== loadSeq) return false
  loading.value = true
  try {
    const nextPage = page.value + 1
    if (nextPage > 100) throw new Error('已达到服务端分页上限，无法继续加载')
    const d = await api.get(
      `/api/detail?type=${encodeURIComponent(type.value)}&source=${encodeURIComponent(source.value)}&id=${encodeURIComponent(id.value)}&page=${nextPage}&limit=100`
    )
    if (seq !== loadSeq) return false
    if (Number.isInteger(d.page) && d.page !== nextPage) throw new Error('分页响应异常，请重试')
    const list: Song[] = Array.isArray(d.list) ? d.list : []
    songs.value.push(...list)
    page.value = nextPage
    total.value = Number.isFinite(d.total) ? d.total : songs.value.length
    hasMore.value = !!d.hasMore && list.length > 0
    if (!songs.value.length) toast('暂未获取到曲目(可能该源不支持)')
    return true
  } catch (e: any) {
    if (seq === loadSeq) toast(e?.message || '加载失败')
    return false
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

function load() {
  const seq = ++loadSeq
  type.value = queryString(route.params.type)
  source.value = queryString(route.params.source)
  id.value = queryString(route.params.id)
  songs.value = []
  page.value = total.value = 0
  hasMore.value = true
  loading.value = playingAll.value = false
  pic.value = queryString(route.query.pic)
  title.value = queryString(route.query.name)
  void loadMore(seq)
}
onMounted(load)
watch(() => route.fullPath, load)
onBeforeUnmount(() => { loadSeq++ })

async function playAll() {
  if (playingAll.value || loading.value) return
  const seq = loadSeq
  playingAll.value = true
  try {
    while (hasMore.value && seq === loadSeq) {
      if (!await loadMore(seq)) return
    }
    if (seq === loadSeq && songs.value.length) player.playAll(songs.value, 0)
  } finally {
    if (seq === loadSeq) playingAll.value = false
  }
}

// 返回上一页;直接进入无历史时回搜索页
function goBack() {
  if (window.history.length > 1) router.back()
  else router.push('/search')
}
</script>

<template>
  <div class="page">
    <div class="detail-top">
      <button class="btn small" @click="goBack"><Icon name="chevron-left" :size="14" /> 返回</button>
    </div>
    <div class="detail-head">
      <div class="detail-cover">
        <CoverImage :src="pic" />
      </div>
      <div class="detail-info">
        <div class="detail-type">{{ typeNames[type] || type }} · {{ SOURCE_NAMES[source] || source }}</div>
        <h2 class="detail-title">{{ title || '曲目列表' }}</h2>
        <button class="btn primary" v-if="songs.length" :disabled="loading || playingAll" @click="playAll"><Icon name="play" :size="14" filled /> {{ playingAll ? '正在加载全部…' : `播放全部(${Math.max(total, songs.length)})` }}</button>
      </div>
    </div>

    <SongList v-if="songs.length" :songs="songs" />
    <div v-if="loading" class="loading-line">加载中…</div>
    <button v-else-if="hasMore" class="btn" :disabled="playingAll" @click="loadMore()">{{ songs.length ? '加载更多' : '重新加载' }}</button>
    <div v-else-if="!songs.length" class="empty">暂无曲目</div>
  </div>
</template>

<style scoped>
.detail-top { margin-bottom: 12px; }
.detail-head { display: flex; gap: 20px; margin-bottom: 22px; align-items: flex-end; }
.detail-cover {
  width: 150px; height: 150px;
  border-radius: 14px;
  overflow: hidden;
  flex-shrink: 0;
  background: var(--panel);
  display: flex; align-items: center; justify-content: center;
}
.detail-cover :is(img, svg) { display: block; width: 100%; height: 100%; object-fit: cover; }
.detail-info { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 8px; align-items: flex-start; }
.detail-type { font-size: 12.5px; color: var(--accent); }
.detail-title { margin: 0; font-size: 24px; }

@media (max-width: 768px) {
  .detail-head { gap: 14px; align-items: center; }
  .detail-cover { width: 110px; height: 110px; }
  .detail-title { font-size: 19px; }
}
</style>
