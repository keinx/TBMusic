<script setup lang="ts">
import { Song, songKey, SOURCE_NAMES, qualityLabel } from '../types'
import { usePlayer } from '../stores/player'
import { useLibrary } from '../stores/library'
import { toast } from '../toast'
import Icon from './Icon.vue'
import SongActions from './SongActions.vue'

// mode:
//  list     — 完整功能(加入队列/收藏/拉黑)
//  playlist — 收藏歌单内(转移到其他歌单 + 从歌单移除)
//  blacklist— 歌曲黑名单(仅取消拉黑)
const props = withDefaults(defineProps<{
  songs: Song[]
  showIndex?: boolean
  mode?: 'list' | 'playlist' | 'blacklist'
}>(), { mode: 'list' })

const emit = defineEmits<{
  (e: 'remove', song: Song): void
  (e: 'transfer', song: Song): void
}>()

const player = usePlayer()
const library = useLibrary()

function isCurrent(s: Song): boolean {
  const c = player.current
  return !!c && songKey(c) === songKey(s)
}

function playSong(s: Song) {
  if (library.isBlacklisted(s)) { toast('该歌曲已被拉黑'); return }
  player.playSong(s)
}

function toggleQueue(s: Song) {
  if (player.isQueued(s)) {
    if (player.removeFromQueue(s)) toast('已从播放队列移除')
    else toast('当前播放的歌曲不能移除')
  } else {
    player.addToQueue(s)
    toast('已加入播放队列')
  }
}

async function onRemove(s: Song) {
  emit('remove', s)
}
</script>

<template>
  <div class="song-list">
    <div
      v-for="(s, i) in songs"
      :key="songKey(s) + i"
      class="song-row"
      :class="{ current: isCurrent(s), black: library.isBlacklisted(s) && mode !== 'blacklist' }"
    >
      <button class="row-main" @click="playSong(s)" :title="`${s.singer} - ${s.name}`">
        <span class="row-index" v-if="showIndex !== false">
          <Icon v-if="isCurrent(s) && player.playing" name="play" :size="12" filled style="color: var(--accent)" />
          <template v-else>{{ i + 1 }}</template>
        </span>
        <span class="row-info">
          <span class="row-name">
            {{ s.name }}
            <span v-for="q in (s.qualities || []).slice(-2)" :key="q" class="q-tag">{{ qualityLabel(q) }}</span>
          </span>
          <span class="row-sub">
            <span class="src-chip">{{ SOURCE_NAMES[s.source] || s.source }}</span>
            {{ s.singer }}<template v-if="s.albumName"> · {{ s.albumName }}</template>
          </span>
        </span>
      </button>
      <span class="row-actions" @dblclick.stop>
        <template v-if="mode === 'list'">
          <button class="icon-btn" :title="player.isQueued(s) ? '已在播放队列(点击移除)' : '加入播放队列'" @click="toggleQueue(s)">
            <Icon name="playlist-add" :size="17" :style="{ color: player.isQueued(s) ? 'var(--accent)' : undefined }" />
          </button>
          <SongActions :song="s" />
        </template>
        <template v-else-if="mode === 'playlist'">
          <button class="icon-btn" title="转移到其他歌单" @click="emit('transfer', s)">
            <Icon name="transfer" :size="16" />
          </button>
          <button class="icon-btn" title="从歌单移除" @click="onRemove(s)">
            <Icon name="x" :size="15" />
          </button>
        </template>
        <template v-else>
          <button class="icon-btn" title="删除(取消拉黑)" @click="onRemove(s)">
            <Icon name="trash" :size="15" style="color: var(--danger)" />
          </button>
        </template>
      </span>
      <span class="row-dur">{{ s.interval }}</span>
    </div>
  </div>
</template>

<style scoped>
.song-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  /* 磨砂玻璃:与全站背景隔开,文字更清晰 */
  background: color-mix(in srgb, var(--panel) 60%, transparent);
  backdrop-filter: blur(18px) saturate(1.15);
  -webkit-backdrop-filter: blur(18px) saturate(1.15);
  border: 1px solid var(--border);
  border-radius: 14px;
  padding: 8px 6px;
}
.song-row {
  display: flex;
  align-items: center;
  border-radius: 10px;
  transition: background 0.12s;
  padding-right: 14px;
}
.song-row:hover { background: color-mix(in srgb, var(--accent) 6%, transparent); }
.song-row.current { background: color-mix(in srgb, var(--accent) 10%, transparent); }
.song-row.black { opacity: 0.45; }
.row-main {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 10px;
  min-width: 0;
  text-align: left;
  color: inherit;
}
.row-index {
  width: 26px;
  display: inline-flex;
  justify-content: center;
  color: var(--text-dim);
  font-size: 13px;
  flex-shrink: 0;
}
.row-info { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.row-name {
  font-size: 14.5px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.q-tag {
  font-size: 10.5px;
  padding: 0 5px;
  border-radius: 4px;
  border: 1px solid var(--accent);
  color: var(--accent);
  margin-left: 6px;
  vertical-align: 1px;
}
.row-sub {
  font-size: 12.5px;
  color: var(--text-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.src-chip {
  display: inline-block;
  font-size: 10.5px;
  padding: 0 6px;
  border-radius: 4px;
  background: var(--bg-soft);
  margin-right: 6px;
}
.row-dur { color: var(--text-dim); font-size: 12.5px; flex-shrink: 0; min-width: 40px; text-align: right; margin-left: 2px; }
.row-actions {
  visibility: hidden;
  display: inline-flex;
  gap: 2px;
  flex-shrink: 0;
}
.song-row:hover .row-actions { visibility: visible; }
@media (hover: none) {
  .row-actions { visibility: visible; }
  .row-dur { display: none; }
}
/* 手机端:歌名过长时可左右滑动查看完整信息,右侧按钮固定不动 */
@media (max-width: 768px) {
  .row-info {
    overflow-x: auto;
    scrollbar-width: none;
    -webkit-overflow-scrolling: touch;
  }
  .row-info::-webkit-scrollbar { display: none; }
  .row-name,
  .row-sub {
    overflow: visible;
    text-overflow: unset;
    width: max-content;
    max-width: none;
  }
}
</style>
