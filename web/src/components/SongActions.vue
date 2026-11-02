<script setup lang="ts">
import { computed } from 'vue'
import { Song } from '../types'
import { useLibrary } from '../stores/library'
import { usePlayer } from '../stores/player'
import { toast } from '../toast'
import { downloadSong, isDownloading } from '../utils/download'
import Icon from './Icon.vue'

const props = defineProps<{ song: Song }>()
const library = useLibrary()
const player = usePlayer()
const saving = computed(() => library.isPending('fav', props.song))
const banning = computed(() => library.isPending('ban', props.song))
const busy = computed(() => saving.value || banning.value)

async function favorite() {
  if (busy.value) return
  const song = props.song
  const removing = library.isFavorited(song)
  const notice = toast(removing ? '正在取消收藏…' : '正在收藏…', 0)
  const result = await library.toggleFavorite(song)
  if (result === 'busy') { notice.dismiss(); return }
  notice.update(result === 'error' ? '收藏操作失败，请重试' : result === 'removed' ? '已取消收藏' : '已收藏到默认歌单')
}

async function blacklist() {
  if (busy.value) return
  const song = props.song
  const removing = library.isBlacklisted(song)
  const notice = toast(removing ? '正在取消拉黑…' : '正在拉黑…', 0)
  const result = await library.toggleBlacklist(song)
  if (result === 'busy') { notice.dismiss(); return }
  if (result === 'added') player.purgeBlacklisted([song])
  notice.update(result === 'error' ? '拉黑操作失败，请重试' : result === 'removed' ? '已取消拉黑' : `已拉黑「${song.name}」`)
}
</script>

<template>
  <span class="song-actions" @click.stop @dblclick.stop>
    <button class="icon-btn" :class="{ 'action-pending': saving }" :disabled="busy" :aria-busy="saving"
      :title="saving ? '正在保存收藏…' : library.isFavorited(song) ? '取消收藏' : '收藏到默认歌单'" @click="favorite">
      <Icon name="heart" :size="17" :filled="library.isFavorited(song)" :style="{ color: library.isFavorited(song) ? 'var(--heart)' : undefined }" />
    </button>
    <button class="icon-btn" :disabled="isDownloading(song)" :aria-busy="isDownloading(song)"
      :title="isDownloading(song) ? '正在准备下载…' : '下载歌曲'" @click="downloadSong(song)">
      <Icon :name="isDownloading(song) ? 'disc' : 'download'" :size="17" :class="{ 'action-spin': isDownloading(song) }" />
    </button>
    <button class="icon-btn" :class="{ 'action-pending': banning }" :disabled="busy" :aria-busy="banning"
      :title="banning ? '正在保存黑名单…' : library.isBlacklisted(song) ? '取消拉黑' : '拉黑'" @click="blacklist">
      <Icon name="ban" :size="16" :style="{ color: library.isBlacklisted(song) ? 'var(--danger)' : undefined }" />
    </button>
  </span>
</template>

<style scoped>
.song-actions { display: inline-flex; align-items: center; gap: 2px; flex-shrink: 0; }
.icon-btn { position: relative; color: inherit; }
.icon-btn:disabled { cursor: default; }
.action-pending::after {
  content: ''; position: absolute; inset: 2px; border: 1px solid transparent;
  border-top-color: var(--accent); border-radius: 50%; animation: action-rotate 0.8s linear infinite;
}
.action-spin { animation: action-rotate 1s linear infinite; }
@keyframes action-rotate { to { transform: rotate(360deg); } }
</style>
