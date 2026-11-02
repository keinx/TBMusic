<script setup lang="ts">
import { computed } from 'vue'
import { usePlayer } from '../stores/player'
import { songKey, SOURCE_NAMES } from '../types'
import Icon from '../components/Icon.vue'
import SongActions from '../components/SongActions.vue'
import { toast } from '../toast'

const player = usePlayer()

const songs = computed(() => player.queue)
const currentKey = computed(() => {
  const c = player.current
  return c ? songKey(c) : ''
})

function play(i: number) {
  player.jumpTo(i)
}

function remove(i: number) {
  if (i === player.index) { toast('当前播放的歌曲不能移除'); return }
  player.removeAt(i)
}

function clearAll() {
  player.clearQueue(false)
  toast('播放列表已清空')
}
</script>

<template>
  <div class="page">
    <div class="head-row">
      <h2 class="page-title">
        <Icon name="list" :size="20" style="color: var(--accent); vertical-align: -3px" />
        播放列表<span class="count-chip">{{ songs.length }} 首</span>
      </h2>
      <div class="ops">
        <button class="btn" v-if="songs.length" @click="player.overlayOpen = true"><Icon name="play" :size="13" filled /> 展开播放页</button>
        <button class="btn danger" v-if="songs.length" @click="clearAll"><Icon name="trash" :size="13" /> 清空列表</button>
      </div>
    </div>

    <div v-if="songs.length" class="queue-body">
      <div
        v-for="(s, i) in songs" :key="songKey(s) + i"
        class="q-row" :class="{ current: songKey(s) === currentKey }"
      >
        <button class="q-main" @click="play(i)">
          <span class="q-index">
            <Icon v-if="songKey(s) === currentKey && player.playing" name="play" :size="12" filled style="color: var(--accent)" />
            <template v-else>{{ i + 1 }}</template>
          </span>
          <span class="q-info">
            <span class="q-name">{{ s.name }}</span>
            <span class="q-sub">
              <span class="src-chip">{{ SOURCE_NAMES[s.source] || s.source }}</span>
              {{ s.singer }}<template v-if="s.albumName"> · {{ s.albumName }}</template>
            </span>
          </span>
        </button>
        <span class="q-actions" @dblclick.stop>
          <SongActions :song="s" />
          <button class="icon-btn" title="移出播放列表" @click="remove(i)">
            <Icon name="x" :size="15" />
          </button>
        </span>
        <span class="q-dur">{{ s.interval }}</span>
      </div>
    </div>
    <div v-else class="empty">播放列表是空的,去搜索或随机歌单添加喜欢的歌吧</div>
  </div>
</template>

<style scoped>
.head-row { display: flex; align-items: center; justify-content: space-between; gap: 10px; margin-bottom: 14px; flex-wrap: wrap; }
.page-title { display: flex; align-items: center; gap: 8px; margin: 0; }
.ops { display: flex; gap: 8px; align-items: center; flex-wrap: nowrap; }
.queue-body {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 2px;
  /* 磨砂玻璃:与全站背景隔开 */
  background: color-mix(in srgb, var(--panel) 60%, transparent);
  backdrop-filter: blur(18px) saturate(1.15);
  -webkit-backdrop-filter: blur(18px) saturate(1.15);
  border: 1px solid var(--border);
  border-radius: 14px;
  padding: 8px 6px;
}
.q-row {
  display: flex;
  align-items: center;
  border-radius: 10px;
  padding-right: 14px;
}
.q-row:hover { background: color-mix(in srgb, var(--accent) 6%, transparent); }
.q-row.current { background: color-mix(in srgb, var(--accent) 10%, transparent); }
.q-main {
  flex: 1;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 10px;
  min-width: 0;
  text-align: left;
  color: inherit;
}
.q-index {
  width: 26px;
  display: inline-flex;
  justify-content: center;
  color: var(--text-dim);
  font-size: 13px;
  flex-shrink: 0;
}
.q-info { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.q-name { font-size: 14.5px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.q-row.current .q-name { color: var(--accent); }
.q-sub { font-size: 12.5px; color: var(--text-dim); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.src-chip {
  display: inline-block;
  font-size: 10.5px;
  padding: 0 6px;
  border-radius: 4px;
  background: var(--bg-soft);
  margin-right: 6px;
}
.q-dur { color: var(--text-dim); font-size: 12.5px; flex-shrink: 0; min-width: 40px; text-align: right; }
.q-actions { visibility: hidden; display: inline-flex; gap: 2px; flex-shrink: 0; }
.q-row:hover .q-actions { visibility: visible; }
/* 手机端:歌名过长时可左右滑动查看完整信息,右侧按钮固定不动 */
@media (max-width: 768px) {
  .q-info {
    overflow-x: auto;
    scrollbar-width: none;
    -webkit-overflow-scrolling: touch;
  }
  .q-info::-webkit-scrollbar { display: none; }
  .q-name,
  .q-sub {
    overflow: visible;
    text-overflow: unset;
    width: max-content;
    max-width: none;
  }
}
@media (hover: none) {
  .q-actions { visibility: visible; }
  .q-dur { display: none; }
}
.spinning { animation: rot 1s linear infinite; }
@keyframes rot { to { transform: rotate(360deg); } }
</style>
