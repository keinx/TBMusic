<script setup lang="ts">
import { ref, watch, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api'
import { queryString } from '../utils/query'
import { SEARCH_TYPES, SOURCE_NAMES, Song, Album, Songlist, Artist } from '../types'
import { usePlayer } from '../stores/player'
import SongList from '../components/SongList.vue'
import Icon from '../components/Icon.vue'
import CoverImage from '../components/CoverImage.vue'
import { toast } from '../toast'

const route = useRoute()
const router = useRouter()
const player = usePlayer()

const keyword = ref(queryString(route.query.keyword))
const inputKw = ref(keyword.value)
const type = ref<'music' | 'album' | 'songlist' | 'artist' | 'lyric'>('music')
const sourceId = ref('kw')

// 恢复上次的分类/渠道选择(从详情页返回时保持状态)
const SEARCH_STATE_KEY = 'tbmusic_search_state'
try {
  const saved = JSON.parse(sessionStorage.getItem(SEARCH_STATE_KEY) || '{}')
  if (SEARCH_TYPES.some((t) => t.id === saved.type)) type.value = saved.type
  if (SOURCE_NAMES[saved.source]) sourceId.value = saved.source
  if (!route.query.keyword && typeof saved.keyword === 'string') {
    keyword.value = saved.keyword
    inputKw.value = saved.keyword
  }
} catch { /* ignore */ }

watch([type, sourceId, keyword], () => {
  try {
    sessionStorage.setItem(SEARCH_STATE_KEY, JSON.stringify({
      type: type.value, source: sourceId.value, keyword: keyword.value,
    }))
  } catch { /* ignore */ }
}, { immediate: true })

const sourceOptions = ref<{ id: string; name: string; types: string[] }[]>([])
const loading = ref(false)
const page = ref(1)
const hasMore = ref(false)
const total = ref(0)
const songs = ref<Song[]>([])
const albums = ref<Album[]>([])
const songlists = ref<Songlist[]>([])
const artists = ref<Artist[]>([])

// 源能力(默认 kw)
onMounted(async () => {
  await player.loadSources()
  const entries = Object.entries(player.sources || {})
  if (entries.length) {
    sourceOptions.value = entries.map(([id, info]) => ({ id, name: info.name || SOURCE_NAMES[id] || id, types: info.searchTypes || [] }))
    if (!sourceOptions.value.find((s) => s.id === sourceId.value)) {
      sourceId.value = sourceOptions.value[0].id
    }
  }
  if (keyword.value) search(true)
})

watch(() => route.query.keyword, (kw) => {
  if (kw && kw !== keyword.value) {
    keyword.value = queryString(kw)
    inputKw.value = queryString(kw)
    search(true)
  }
})

function supports(t: string): boolean {
  const s = sourceOptions.value.find((x) => x.id === sourceId.value)
  return !s || !s.types?.length || s.types.includes(t)
}

let searchSeq = 0 // 请求序号:快速切源/切分类时丢弃过期响应
async function search(reset: boolean): Promise<boolean> {
  const kw = keyword.value.trim()
  if (!kw) return false
  const seq = ++searchSeq
  if (reset) {
    page.value = 1
    songs.value = []
    albums.value = []
    songlists.value = []
    artists.value = []
  }
  if (!supports(type.value)) {
    toast(`${SOURCE_NAMES[sourceId.value] || sourceId.value} 暂不支持${SEARCH_TYPES.find(t => t.id === type.value)?.name}搜索`)
    return false
  }
  loading.value = true
  try {
    const d = await api.get(
      `/api/search?source=${sourceId.value}&type=${type.value}&keyword=${encodeURIComponent(kw)}&page=${page.value}&limit=30`
    )
    if (seq !== searchSeq) return // 已切走,丢弃过期结果
    total.value = d.total || 0
    if (d.page !== undefined && d.page !== page.value) { hasMore.value = false; return false }
    hasMore.value = !!d.hasMore && page.value < 100
    const list: any[] = d.list || []
    if (type.value === 'music' || type.value === 'lyric') songs.value.push(...list)
    else if (type.value === 'album') albums.value.push(...list)
    else if (type.value === 'songlist') songlists.value.push(...list)
    else if (type.value === 'artist') artists.value.push(...list)
    return true
  } catch (e: any) {
    if (seq !== searchSeq) return false
    toast(e?.message || '搜索失败')
    hasMore.value = false
    return false
  } finally {
    if (seq === searchSeq) loading.value = false
  }
}

async function loadMore() {
  if (loading.value || !hasMore.value || page.value >= 100) return
  const prev = page.value
  page.value = prev + 1
  // 请求失败要把页码退回去,否则下次「加载更多」会跳过一整页
  const ok = await search(false)
  if (!ok && page.value === prev + 1) page.value = prev
}

function switchType(t: string) {
  if (type.value === t) return
  if (sourceOptions.value.length && !supports(t)) {
    toast(`${SOURCE_NAMES[sourceId.value] || sourceId.value} 暂不支持${SEARCH_TYPES.find(x => x.id === t)?.name}搜索`)
    return
  }
  type.value = t as any
  songs.value = []; albums.value = []; songlists.value = []; artists.value = []
  page.value = 1
  if (keyword.value.trim()) search(true)
}
function switchSource(id: string) {
  if (sourceId.value === id) return
  sourceId.value = id
  songs.value = []; albums.value = []; songlists.value = []; artists.value = []
  page.value = 1
  if (keyword.value.trim()) {
    if (!supports(type.value)) {
      // 切到不支持的源+类型,自动回歌曲 tab
      type.value = 'music'
    }
    search(true)
  }
}

function submitSearch() {
  const kw = inputKw.value.trim()
  if (!kw) { toast('请输入关键词'); return }
  keyword.value = kw
  // 同步 URL(便于分享/返回);相同关键词不重复 replace,避免路由告警
  if (route.query.keyword !== kw) router.replace({ query: { keyword: kw } })
  // 必须直接发起搜索:watch(route.query.keyword) 里判断 kw !== keyword.value,
  // 而上面已同步赋值,靠它触发会永远短路 → 点搜索没反应
  search(true)
}

function goDetail(t: string, item: { id: string; name?: string; pic?: string }) {
  router.push({
    path: `/detail/${t}/${sourceId.value}/${encodeURIComponent(item.id)}`,
    query: { name: item.name || '', pic: item.pic || '' },
  })
}

function playAllResults() {
  if (songs.value.length) player.playAll(songs.value, 0)
}
</script>

<template>
  <div class="page">
    <div class="glass-card search-panel">
    <div class="search-bar">
      <input v-model="inputKw" class="input" placeholder="输入关键词,回车搜索" @keyup.enter="submitSearch" />
      <button class="btn primary" @click="submitSearch">搜索</button>
    </div>

    <div class="tabs">
      <div class="type-tabs">
        <button
          v-for="t in SEARCH_TYPES" :key="t.id"
          class="tab" :class="{ active: type === t.id, disabled: !supports(t.id) && sourceOptions.length }"
          @click="switchType(t.id)"
        >{{ t.name }}</button>
      </div>
      <div class="source-tabs" v-if="sourceOptions.length">
        <button
          v-for="s in sourceOptions" :key="s.id"
          class="stab" :class="{ active: sourceId === s.id }"
          @click="switchSource(s.id)"
        >{{ s.name }}</button>
      </div>
    </div>

    <div v-if="keyword && total" class="result-meta">
      共约 {{ total }} 条结果
      <button v-if="(type === 'music' || type === 'lyric') && songs.length" class="btn small" @click="playAllResults"><Icon name="play" :size="13" filled /> 播放全部</button>
    </div>
    </div>

    <!-- 歌曲/歌词结果 -->
    <template v-if="type === 'music' || type === 'lyric'">
      <SongList v-if="songs.length" :songs="songs" />
      <div v-else-if="!loading && keyword" class="empty">没有找到相关歌曲</div>
    </template>

    <!-- 专辑 -->
    <template v-else-if="type === 'album'">
      <div v-if="albums.length" class="card-grid">
        <div v-for="a in albums" :key="a.id" class="card" @click="goDetail('album', a)">
          <CoverImage :src="a.pic" />
          <div class="card-name">{{ a.name }}</div>
          <div class="card-sub">{{ a.singer }}<template v-if="a.publish"> · {{ a.publish }}</template></div>
        </div>
      </div>
      <div v-else-if="!loading && keyword" class="empty">没有找到相关专辑</div>
    </template>

    <!-- 歌单 -->
    <template v-else-if="type === 'songlist'">
      <div v-if="songlists.length" class="card-grid">
        <div v-for="sl in songlists" :key="sl.id" class="card" @click="goDetail('songlist', sl)">
          <CoverImage :src="sl.pic" />
          <div class="card-name">{{ sl.name }}</div>
          <div class="card-sub">{{ sl.creator }}<template v-if="sl.count"> · {{ sl.count }}首</template></div>
        </div>
      </div>
      <div v-else-if="!loading && keyword" class="empty">没有找到相关歌单</div>
    </template>

    <!-- 歌手 -->
    <template v-else-if="type === 'artist'">
      <div v-if="artists.length" class="card-grid artist-grid">
        <div v-for="ar in artists" :key="ar.id" class="card artist-card" @click="goDetail('artist', ar)">
          <CoverImage :src="ar.pic" class="round" />
          <div class="card-name">{{ ar.name }}</div>
          <div class="card-sub">歌手</div>
        </div>
      </div>
      <div v-else-if="!loading && keyword" class="empty">没有找到相关歌手</div>
    </template>

    <div v-if="loading" class="loading-line">加载中…</div>
    <div v-else-if="hasMore" class="loading-line">
      <button class="btn" @click="loadMore">加载更多</button>
    </div>
  </div>
</template>

<style scoped>
.search-bar { display: flex; gap: 10px; margin-bottom: 14px; }
.tabs { display: flex; flex-direction: column; gap: 10px; margin-bottom: 14px; }
.type-tabs { display: flex; gap: 4px; border-bottom: 1px solid var(--border); overflow-x: auto; }
.tab {
  padding: 9px 16px;
  font-size: 14.5px;
  color: var(--text-dim);
  border-bottom: 2px solid transparent;
  white-space: nowrap;
}
.tab.active { color: var(--accent); border-bottom-color: var(--accent); }
.tab.disabled { opacity: 0.35; }
.source-tabs { display: flex; gap: 8px; flex-wrap: wrap; }
.stab {
  padding: 5px 14px;
  border-radius: 999px;
  font-size: 13px;
  background: var(--panel);
  border: 1px solid var(--border);
  color: var(--text-dim);
}
.stab.active { background: var(--accent-soft); color: var(--accent); border-color: var(--accent); }
.result-meta {
  display: flex;
  align-items: center;
  gap: 12px;
  color: var(--text-dim);
  font-size: 13px;
  margin-bottom: 10px;
}
.card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 16px;
}
.card { cursor: pointer; min-width: 0; }
.card :is(img, svg) {
  display: block;
  width: 100%;
  aspect-ratio: 1;
  object-fit: cover;
  border-radius: 12px;
  background: var(--panel);
}
.card :is(img, svg).round { border-radius: 50%; }
/* 标题、描述各占一行,超出省略号 */
.card-name {
  font-size: 13.5px;
  margin-top: 8px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.card-sub {
  font-size: 12px;
  color: var(--text-dim);
  margin-top: 2px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.artist-grid { grid-template-columns: repeat(auto-fill, minmax(120px, 1fr)); }
.search-panel { padding: 14px 16px; margin-bottom: 14px; background: color-mix(in srgb, var(--panel) 84%, transparent); }
</style>
