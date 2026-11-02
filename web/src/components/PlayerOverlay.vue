<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { usePlayer } from '../stores/player'
import { api } from '../api'
import { parseLyrics, activeLineIndex, fmtTime, LyricLine } from '../lyrics'
import { songKey, qualityLabel } from '../types'
import { toast } from '../toast'
import SongActions from './SongActions.vue'
import Icon from './Icon.vue'
import CoverImage from './CoverImage.vue'
import Spectrum from './Spectrum.vue'
import { usePrefs } from '../stores/prefs'
import { coverURL } from '../utils/cover'

const player = usePlayer()
const prefs = usePrefs()

const lyrics = ref<LyricLine[]>([])
const lyricLoading = ref(false)
const queueOpen = ref(false)
const qualityOpen = ref(false)

const song = computed(() => player.current)
const activeIdx = computed(() => activeLineIndex(lyrics.value, player.currentTime))
const qualities = computed(() => player.supportedQualities(song.value))

// 当前封面地址:先于下面的 computed 声明,避免依赖 computed 的惰性求值才不报 TDZ
const coverUrl = computed(() => song.value?.pic || '')
let metaSeq = 0

// 背景图 CSS 注入面:仅接受 http(s) 地址,并剔除会截断 url("...") 的字符
const bgStyle = computed(() => {
  const u = coverURL(coverUrl.value)
  if (!u) return {}
  return { backgroundImage: `url("${u.replace(/["\\]/g, '')}")` }
})

// 仅加载歌词;封面复用 MiniPlayer 的请求。关闭播放页或切歌立即释放旧请求。
async function loadMeta(s: NonNullable<typeof song.value>, signal: AbortSignal) {
  const seq = ++metaSeq
  lyrics.value = []
  lyricLoading.value = true
  try {
    const lyr = await api.post('/api/lyric', { source: s.source, musicInfo: s }, { signal })
    if (seq !== metaSeq || signal.aborted) return
    lyrics.value = parseLyrics(lyr.lyric || '', lyr.tlyric || '')
  } catch { /* 歌词缺失不提示 */ } finally {
    if (seq === metaSeq) lyricLoading.value = false
  }
}
watch([song, () => player.overlayOpen], ([s, open], _, onCleanup) => {
  if (!s || !open) return
  const controller = new AbortController()
  onCleanup(() => controller.abort())
  loadMeta(s, controller.signal)
}, { immediate: true })

// 歌词滚动跟随
const lyricWrap = ref<HTMLElement>()
watch(activeIdx, (idx) => {
  if (idx < 0 || !lyricWrap.value) return
  const el = lyricWrap.value.querySelector(`[data-line="${idx}"]`) as HTMLElement | null
  if (el) {
    const wrap = lyricWrap.value
    const top = el.offsetTop - wrap.clientHeight / 2 + el.clientHeight / 2
    wrap.scrollTo({ top, behavior: 'smooth' })
  }
})

function seekToLine(i: number) {
  const line = lyrics.value[i]
  if (line) player.seek(Math.max(0, line.time - 0.2))
}

const modeIcon = computed(() =>
  player.mode === 'one' ? 'repeat-one' : player.mode === 'random' ? 'shuffle' : 'repeat'
)
const modeLabel = computed(() =>
  player.mode === 'one' ? '单曲循环' : player.mode === 'random' ? '随机播放' : '列表循环'
)
function cycleMode() {
  const order = ['loop', 'one', 'random'] as const
  const i = order.indexOf(player.mode)
  player.setMode(order[(i + 1) % order.length])
  toast(modeLabel.value)
}

// 进度条拖动
const seeking = ref(false)
const seekPreview = ref(0)
function onSeekInput(e: Event) {
  const v = parseFloat((e.target as HTMLInputElement).value)
  // 空值/非法值不得写入预览,否则会显示 NaN:NaN 并把 footer 时间带崩
  if (Number.isFinite(v)) seekPreview.value = v
}
function onSeekChange(e: Event) {
  const v = parseFloat((e.target as HTMLInputElement).value)
  if (Number.isFinite(v)) player.seek(v) // store 内部还会夹到 [0, duration]
  seeking.value = false
}
</script>

<template>
  <transition name="slide-up">
    <div v-if="player.overlayOpen && song" class="player-overlay">
      <!-- 背景 -->
      <div class="po-bg" :style="bgStyle"></div>
      <div class="po-bg-mask"></div>
      <!-- 频谱跳动背景 -->
      <div class="po-spectrum">
        <Spectrum :height="130" :bars="56" :opacity="0.9" :animated="true" />
      </div>

      <button class="icon-btn po-close" @click="player.overlayOpen = false">
        <Icon name="chevron-down" :size="26" />
      </button>

      <div class="po-body">
        <!-- 左:封面+控件 -->
        <section class="po-left">
          <div class="po-cover" :class="{ disc: prefs.discSpin, playing: player.playing }">
            <CoverImage :src="coverUrl" />
            <i v-if="prefs.discSpin" class="po-cover-hole"></i>
          </div>
          <div class="po-meta">
            <div class="po-name">{{ song.name }}</div>
            <div class="po-singer">{{ song.singer }}<template v-if="song.albumName"> · {{ song.albumName }}</template></div>
          </div>

          <!-- 进度 -->
          <div class="po-progress">
            <span>{{ fmtTime(seeking ? seekPreview : player.currentTime) }}</span>
            <input
              type="range" min="0" :max="player.duration || 1" step="0.5"
              :value="seeking ? seekPreview : player.currentTime"
              @input="seeking = true; onSeekInput($event)"
              @change="onSeekChange"
            />
            <span>{{ fmtTime(player.duration) }}</span>
          </div>

          <!-- 控制按钮 -->
          <div class="po-controls">
            <button class="icon-btn" :title="modeLabel" @click="cycleMode">
              <Icon :name="modeIcon" :size="19" />
            </button>
            <button class="icon-btn big" @click="player.prev()"><Icon name="skip-back" :size="25" filled /></button>
            <button class="po-play" @click="player.toggle()">
              <Icon v-if="player.urlLoading" name="disc" :size="26" class="spin" />
              <Icon v-else :name="player.playing ? 'pause' : 'play'" :size="player.playing ? 24 : 26" filled />
            </button>
            <button class="icon-btn big" @click="player.next()"><Icon name="skip-forward" :size="25" filled /></button>
            <button class="icon-btn" title="播放队列" @click="queueOpen = true"><Icon name="list" :size="19" /></button>
          </div>

          <!-- 音量 + 音质 + 收藏/拉黑 -->
          <div class="po-extra">
            <span class="po-route" v-if="player.playbackRoute">{{ player.playbackRoute === 'direct' ? '直连播放' : '中转播放' }}</span>
            <div class="po-vol">
              <button class="icon-btn" @click="player.setMuted(!player.muted)">
                <Icon :name="player.muted || player.volume === 0 ? 'volume-x' : 'volume'" :size="17" />
              </button>
              <input type="range" min="0" max="1" step="0.02" :value="player.muted ? 0 : player.volume" @input="player.setVolume(parseFloat(($event.target as HTMLInputElement).value))" />
            </div>
            <div class="po-quality" v-if="qualities.length">
              <button class="q-btn" @click="qualityOpen = !qualityOpen">
                {{ qualityLabel(player.currentQuality || player.qualityPref) }} ▾
              </button>
              <div v-if="qualityOpen" class="q-menu" @click="qualityOpen = false">
                <button
                  v-for="q in qualities" :key="q"
                  :class="{ on: q === player.qualityPref }"
                  @click="player.setQualityPref(q)"
                >{{ qualityLabel(q) }}{{ q === player.qualityPref ? ' ✓' : '' }}</button>
              </div>
            </div>
            <SongActions :song="song" />
          </div>
          <p v-if="player.urlError" class="po-err">{{ player.urlError }}</p>
        </section>

        <!-- 右:歌词 -->
        <section class="po-right">
          <div v-if="lyricLoading" class="loading-line">歌词加载中…</div>
          <div v-else-if="!lyrics.length" class="empty">
            纯音乐,或该歌曲暂无歌词<br /><br />
            <button class="btn small" @click="queueOpen = true">查看播放队列</button>
          </div>
          <div v-else ref="lyricWrap" class="lyrics">
            <div
              v-for="(l, i) in lyrics" :key="i" :data-line="i"
              class="lyric-line" :class="{ active: i === activeIdx }"
              @click="seekToLine(i)"
            >
              <p class="l-main">{{ l.text }}</p>
              <p v-if="l.trans" class="l-trans">{{ l.trans }}</p>
            </div>
          </div>
        </section>
      </div>

      <!-- 队列抽屉 -->
      <transition name="fade">
        <div v-if="queueOpen" class="queue-mask" @click.self="queueOpen = false">
          <div class="queue-panel">
            <div class="queue-head">
              <span>播放队列({{ player.queue.length }})</span>
              <span class="queue-ops">
                <button class="btn small" @click="player.clearQueue(true)">清空(保留当前)</button>
                <button class="icon-btn" @click="queueOpen = false"><Icon name="x" :size="16" /></button>
              </span>
            </div>
            <div class="queue-list">
              <div
                v-for="(s, i) in player.queue" :key="songKey(s) + i"
                class="q-row" :class="{ current: i === player.index }"
                @click="player.jumpTo(i)"
              >
                <span class="q-name">{{ s.singer }} - {{ s.name }}</span>
                <button class="icon-btn q-del" @click.stop="player.removeAt(i)"><Icon name="x" :size="13" /></button>
              </div>
            </div>
          </div>
        </div>
      </transition>

    </div>
  </transition>
</template>

<style scoped>
.player-overlay {
  position: fixed;
  inset: 0;
  z-index: 900;
  background: var(--bg);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.po-bg {
  position: absolute;
  inset: -40px;
  background-size: cover;
  background-position: center;
  filter: blur(70px) brightness(0.4) saturate(1.4);
  transform: scale(1.2);
}
[data-theme='light'] .po-bg { filter: blur(70px) brightness(1.1) saturate(1.2); }
.po-bg-mask { position: absolute; inset: 0; background: rgba(0, 0, 0, 0.25); }
[data-theme='light'] .po-bg-mask { background: rgba(255, 255, 255, 0.35); }
.po-spectrum {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  pointer-events: none;
  z-index: 1;
}
.po-cover {
  position: relative;
  width: 300px;
  height: 300px;
  border-radius: 18px;
  overflow: hidden;
  box-shadow: 0 18px 50px rgba(0, 0, 0, 0.5);
  background: var(--panel);
  display: flex; align-items: center; justify-content: center;
  transition: transform 0.4s;
}
.po-cover.playing { transform: scale(1.02); }
.po-cover.disc { border-radius: 50%; }
.po-cover.disc :is(img, svg) { animation: disc-rot 16s linear infinite; animation-play-state: paused; }
.po-cover.disc.playing :is(img, svg) { animation-play-state: running; }
.po-cover-hole {
  position: absolute;
  width: 52px;
  height: 52px;
  border-radius: 50%;
  background: var(--bg);
  border: 6px solid var(--bg-soft);
  box-shadow: 0 0 0 1px var(--border);
}
@keyframes disc-rot { to { transform: rotate(360deg); } }
.po-close {
  position: absolute;
  top: 14px;
  left: 16px;
  z-index: 5;
  color: #fff;
  background: rgba(255, 255, 255, 0.12);
}
[data-theme='light'] .po-close { color: var(--text); }
.po-body {
  position: relative;
  z-index: 2;
  flex: 1;
  display: flex;
  gap: 40px;
  max-width: 1100px;
  width: 100%;
  margin: 0 auto;
  padding: 60px 30px 30px;
  overflow: hidden;
}
.po-left {
  width: 380px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 16px;
  color: #fff;
}
[data-theme='light'] .po-left { color: var(--text); }
.po-cover :is(img, svg) { display: block; width: 100%; height: 100%; object-fit: cover; }
.po-meta { text-align: center; max-width: 100%; }
.po-name { font-size: 21px; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.po-singer { font-size: 13.5px; opacity: 0.75; margin-top: 4px; }

.po-progress { display: flex; align-items: center; gap: 10px; width: 100%; font-size: 12px; opacity: 0.9; }
.po-progress input[type='range'] { flex: 1; accent-color: var(--accent); height: 4px; }

.po-controls { display: flex; align-items: center; gap: 10px; }
.po-controls .icon-btn { color: inherit; }
.po-controls .icon-btn.big :deep(svg) { width: 25px; height: 25px; }
.po-play {
  width: 62px; height: 62px;
  border-radius: 50%;
  background: var(--accent);
  color: #fff;
  display: flex; align-items: center; justify-content: center;
  box-shadow: 0 8px 24px color-mix(in srgb, var(--accent) 40%, transparent);
}
.spin { animation: rot 1s linear infinite; }
@keyframes rot { to { transform: rotate(360deg); } }

.po-extra { display: flex; align-items: center; gap: 14px; flex-wrap: wrap; justify-content: center; }
.po-route { font-size: 11px; opacity: 0.7; }
.po-extra-actions { display: inline-flex; align-items: center; gap: 4px; }
.po-vol { display: flex; align-items: center; gap: 6px; }
.po-vol input[type='range'] { width: 90px; accent-color: var(--accent); height: 4px; }
.po-quality { position: relative; }
.q-btn {
  padding: 5px 12px;
  border-radius: 999px;
  border: 1px solid rgba(255, 255, 255, 0.3);
  color: inherit;
  font-size: 12.5px;
}
[data-theme='light'] .q-btn { border-color: var(--border); }
.q-menu {
  position: absolute;
  bottom: calc(100% + 8px);
  left: 50%;
  transform: translateX(-50%);
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 10px;
  padding: 4px;
  display: flex;
  flex-direction: column;
  min-width: 110px;
  box-shadow: var(--shadow);
  z-index: 10;
}
.q-menu button {
  padding: 8px 14px;
  border-radius: 7px;
  color: var(--text);
  font-size: 13px;
  text-align: left;
}
.q-menu button:hover, .q-menu button.on { background: var(--accent-soft); color: var(--accent); }
.po-err { font-size: 12.5px; color: var(--danger); }
.po-extra .icon-btn { color: inherit; }

.po-right { flex: 1; min-width: 0; position: relative; color: #fff; }
[data-theme='light'] .po-right { color: var(--text); }
.lyrics {
  height: 100%;
  overflow-y: auto;
  padding: 20% 10px;
  text-align: center;
  mask-image: linear-gradient(transparent, #000 15%, #000 85%, transparent);
  -webkit-mask-image: linear-gradient(transparent, #000 15%, #000 85%, transparent);
}
.lyric-line {
  padding: 9px 0;
  opacity: 0.55;
  cursor: pointer;
  transition: all 0.2s;
}
.lyric-line:hover { opacity: 0.85; }
.lyric-line.active { opacity: 1; font-size: 17px; font-weight: 600; }
.l-main { margin: 0; font-size: 15.5px; }
.l-trans { margin: 2px 0 0; font-size: 13px; opacity: 0.75; }

.queue-mask {
  position: absolute;
  inset: 0;
  z-index: 20;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: center;
  justify-content: center;
}
.queue-panel {
  width: 480px;
  max-width: calc(100% - 30px);
  max-height: 70vh;
  display: flex;
  flex-direction: column;
  background: var(--panel);
  border-radius: 16px;
  border: 1px solid var(--border);
  box-shadow: var(--shadow);
  overflow: hidden;
  color: var(--text);
}
.queue-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  font-size: 14.5px;
  border-bottom: 1px solid var(--border);
}
.queue-ops { display: flex; align-items: center; gap: 6px; }
.queue-list { overflow-y: auto; padding: 8px; }
.q-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  border-radius: 8px;
  cursor: pointer;
  font-size: 13.5px;
}
.q-row:hover { background: var(--panel-hover); }
.q-row.current { color: var(--accent); }
.q-name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.q-del {
  width: 26px;
  height: 26px;
  font-size: 12px;
  visibility: hidden;
  /* 全局 .icon-btn 的 transition:all 会让 visibility 延迟生效,x 号迟迟不消失 */
  transition: none;
}
.q-row:hover .q-del { visibility: visible; }

.slide-up-enter-active, .slide-up-leave-active { transition: transform 0.28s ease; }
.slide-up-enter-from, .slide-up-leave-to { transform: translateY(100%); }
.fade-enter-active, .fade-leave-active { transition: opacity 0.2s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }

@media (max-width: 768px) {
  .po-spectrum { height: 86px; }
  .po-body {
    flex-direction: column;
    gap: 8px;
    padding: 52px 18px 16px;
    overflow-y: auto;
  }
  .po-left { width: 100%; gap: 10px; }
  .po-cover { width: min(64vw, 250px); height: min(64vw, 250px); }
  .po-right { flex: 1; min-height: 260px; }
  .lyrics { padding: 8% 6px; }
  .po-vol input[type='range'] { width: 64px; }
}
</style>
