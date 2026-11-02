<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { usePlayer } from '../stores/player'
import { useRoute } from 'vue-router'
import { api } from '../api'
import { songKey } from '../types'
import { fmtTime } from '../lyrics'
import Icon from './Icon.vue'
import CoverImage from './CoverImage.vue'
import Spectrum from './Spectrum.vue'
import SongActions from './SongActions.vue'

const player = usePlayer()
const route = useRoute()
// 与 NavBar 一致:这些页面(详情/二级页)手机端无底部导航
const hideTabbar = computed(() =>
  ['/history', '/blacklist', '/users', '/system', '/settings', '/detail'].some((x) => route.path.startsWith(x))
)

// 封面:歌曲自带 pic 直接用;没有(旧数据/部分源)就向服务端补取一次,
// 否则底部播放条会一直显示占位图
const picFromApi = ref('')
let picSeq = 0
watch(() => (player.current ? songKey(player.current) : ''), async (_, __, onCleanup) => {
  const seq = ++picSeq
  picFromApi.value = ''
  const s = player.current
  if (!s || s.pic) return
  const controller = new AbortController()
  onCleanup(() => controller.abort())
  try {
    const d = await api.post('/api/pic', { source: s.source, musicInfo: s }, { signal: controller.signal })
    if (seq === picSeq && !controller.signal.aborted && d?.url) {
      picFromApi.value = d.url
      s.pic = d.url
      player.updateMediaSession(s, d.url)
    }
  } catch { /* 取不到就用占位图 */ }
}, { immediate: true })

const pic = computed(() => player.current?.pic || picFromApi.value)
const durationPct = computed(() => {
  if (!player.duration) return '0%'
  return ((player.currentTime / player.duration) * 100).toFixed(1) + '%'
})

</script>

<template>
  <div v-if="player.current" class="mini" :class="{ 'no-tabbar': hideTabbar }" @click="player.overlayOpen = true">
    <div class="mini-spectrum">
      <Spectrum :height="34" :bars="34" :opacity="0.3" :animated="false" />
    </div>

    <div class="mini-cover" :class="{ spin: player.playing }">
      <CoverImage :src="pic" />
      <i class="mini-cover-hole"></i>
    </div>
    <div class="mini-info">
      <div class="mini-name">{{ player.current.name }}</div>
      <div class="mini-singer">{{ player.current.singer }}</div>
      <div class="mini-progress">
        <div class="mini-bar">
          <i :style="{ width: durationPct }"></i>
        </div>
        <span class="mini-time">{{ fmtTime(player.currentTime) }}</span>
      </div>
    </div>
    <div class="mini-actions" @click.stop>
      <SongActions :song="player.current" />
      <button class="icon-btn" @click="player.prev()"><Icon name="skip-back" :size="18" filled /></button>
      <button class="icon-btn mini-play" @click="player.toggle()">
        <Icon v-if="player.urlLoading" name="disc" :size="18" class="spin-icon" />
        <Icon v-else :name="player.playing ? 'pause' : 'play'" :size="18" filled />
      </button>
      <button class="icon-btn" @click="player.next()"><Icon name="skip-forward" :size="18" filled /></button>
      <button class="icon-btn m-queue-btn" title="播放列表" @click="$router.push('/queue')"><Icon name="list" :size="17" /></button>
      <button class="icon-btn m-expand-btn" title="展开播放页" @click.stop="player.overlayOpen = true"><Icon name="chevron-up" :size="20" /></button>
    </div>
  </div>
</template>

<style scoped>
.mini {
  position: fixed;
  left: 12px;
  right: 12px;
  bottom: calc(10px + env(safe-area-inset-bottom, 0px));
  z-index: 130;
  height: var(--player-height);
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 0 14px;
  background: color-mix(in srgb, var(--panel) 68%, transparent);
  backdrop-filter: blur(20px) saturate(1.2);
  -webkit-backdrop-filter: blur(20px) saturate(1.2);
  border: 1px solid var(--border);
  border-radius: 16px;
  box-shadow: var(--shadow);
  cursor: pointer;
  max-width: 900px;
  margin: 0 auto;
  overflow: hidden;
}
.m-expand-btn { display: none; }
@media (max-width: 768px) {
  .mini {
    left: 0;
    right: 0;
    /* 紧挨底部导航栏(实测底栏高 47px) */
    bottom: calc(47px + env(safe-area-inset-bottom, 0px));
    border-radius: 14px 14px 0 0;
    max-width: 100%;
  }
  /* 二级/详情页无底部导航,播放条贴到最底 */
  .mini.no-tabbar { bottom: calc(0px + env(safe-area-inset-bottom, 0px)); }
  .m-expand-btn { display: inline-flex; }
}
.mini-spectrum {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 34px;
  pointer-events: none;
}
.mini-cover {
  position: relative;
  width: 46px;
  height: 46px;
  border-radius: 50%;
  overflow: hidden;
  flex-shrink: 0;
  background: var(--bg-soft);
  display: flex;
  align-items: center;
  justify-content: center;
  border: 2px solid var(--bg-soft);
  box-shadow: 0 0 0 1px var(--border);
}
.mini-cover :is(img, svg) { display: block; width: 100%; height: 100%; object-fit: cover; }
.mini-cover.spin :is(img, svg) { animation: disc-rot 14s linear infinite; }
.mini-cover-hole {
  position: absolute;
  width: 10px;
  height: 10px;
  border-radius: 50%;
  background: var(--panel);
  border: 1px solid var(--border);
}
@keyframes disc-rot { to { transform: rotate(360deg); } }

.mini-info { flex: 1; min-width: 0; }
.mini-name { font-size: 14.5px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.mini-singer { font-size: 12px; color: var(--text-dim); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.mini-progress { display: flex; align-items: center; gap: 8px; margin-top: 4px; }
.mini-bar { flex: 1; height: 3px; border-radius: 2px; background: var(--bg-soft); overflow: hidden; }
.mini-bar i { display: block; height: 100%; background: var(--accent-grad); border-radius: 2px; }
.mini-time { font-size: 10.5px; color: var(--text-dim); min-width: 30px; text-align: right; }
.mini-actions { display: flex; align-items: center; gap: 2px; }
.mini-play {
  width: 42px;
  height: 42px;
  border: 1px solid var(--border);
  color: var(--text);
}
.spin-icon { animation: mini-rot 1s linear infinite; }
@keyframes mini-rot { to { transform: rotate(360deg); } }
@media (max-width: 768px) {
  .m-queue-btn { display: none; }
  .mini { gap: 6px; padding: 0 8px; }
  .mini-actions { gap: 0; }
  .mini-actions :deep(.icon-btn) { width: 28px; height: 36px; }
  .mini-actions .mini-play { width: 36px; }
}
</style>
