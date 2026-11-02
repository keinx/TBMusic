<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { songKey, Song, SongEntry } from '../types'
import { useLibrary } from '../stores/library'
import SongList from '../components/SongList.vue'
import Icon from '../components/Icon.vue'
import MBack from '../components/MBack.vue'
import { toast } from '../toast'

const library = useLibrary()
// 直接使用 store 数据,避免本地副本与 store 脱节 + 重复请求
const entries = computed(() => library.blacklist)

onMounted(() => { library.loadBlacklist() })

async function onRemove(song: Song) {
  const entry = entries.value.find((e) => songKey(e.musicInfo) === songKey(song))
  if (!entry) return
  try {
    await library.removeBlacklist(entry.id)
    toast('已取消拉黑')
  } catch (e: any) { toast(e?.message || '操作失败') }
}
</script>

<template>
  <div class="page">
    <MBack />
    <div class="head-row">
      <h2 class="page-title">
        <Icon name="ban" :size="20" style="color: var(--danger); vertical-align: -3px" />
        歌曲黑名单<span class="count-chip">{{ entries.length }} 首</span>
      </h2>
    </div>
    <p class="tip">被拉黑的歌曲在播放队列中会自动跳过,搜索结果中会变灰标记。</p>

    <SongList v-if="entries.length" :songs="entries.map(e => e.musicInfo)" mode="blacklist" @remove="onRemove" />
    <div v-else class="empty">歌曲黑名单为空</div>
  </div>
</template>

<style scoped>
.head-row { margin-bottom: 6px; }
.page-title { display: flex; align-items: center; gap: 8px; margin: 0; }
.tip { color: var(--text-dim); font-size: 12.5px; margin: 0 0 14px; }
</style>
