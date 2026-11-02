<script setup lang="ts">
// 全站统一背景(最早首页样式):深色底 + 几个不同颜色的光斑 + 呼吸灯效果(播放页除外)
// 每次刷新随机色相 + 随机位置(四个象限各一个,保证分散)
import { computed } from 'vue'

function rnd(min: number, max: number): number {
  return Math.random() * (max - min) + min
}

const baseHue = Math.floor(Math.random() * 360)
function hue(offset: number): number {
  return (baseHue + offset + 360) % 360
}

// 四个象限,乱序分配给光斑,象限内再随机落点
const quadrants = [
  { x: [-12, 36], y: [-12, 34] },
  { x: [54, 92], y: [-12, 34] },
  { x: [-12, 36], y: [54, 92] },
  { x: [54, 92], y: [54, 92] },
].sort(() => Math.random() - 0.5)

const blobs = quadrants.map((q, i) => ({
  color: `hsl(${hue(i * 74 + Math.floor(rnd(0, 44)))} 60% 46%)`,
  left: rnd(q.x[0], q.x[1]),
  top: rnd(q.y[0], q.y[1]),
  size: rnd(22, 42),
  dur: rnd(6.5, 10),
  delay: -rnd(0, 8),
  // 不规则形状:八个角半径各自随机,配合模糊形成有机色斑
  radius: (() => {
    const r = () => Math.round(rnd(26, 74))
    return `${r()}% ${r()}% ${r()}% ${r()}% / ${r()}% ${r()}% ${r()}% ${r()}%`
  })(),
}))

const blobStyles = computed(() => blobs.map((b) => ({
  outer: {
    left: b.left + 'vw',
    top: b.top + 'vh',
    width: b.size + 'vw',
    height: b.size + 'vw',
  },
  inner: {
    background: b.color,
    borderRadius: b.radius,
    animationDuration: b.dur + 's',
    animationDelay: b.delay + 's',
  },
})))
</script>

<template>
  <div class="ambient" aria-hidden="true">
    <div class="ab-base"></div>
    <div v-for="(s, i) in blobStyles" :key="i" class="ab-blob" :style="s.outer"><div class="inner" :style="s.inner"></div></div>
    <div class="ab-vignette"></div>
  </div>
</template>

<style scoped>
.ambient {
  position: fixed;
  inset: 0;
  z-index: -1;
  overflow: hidden;
  pointer-events: none;
}
/* 深色底 */
.ab-base { position: absolute; inset: 0; background: var(--bg); }
.ab-blob { position: absolute; will-change: transform; animation: ab-drift 32s ease-in-out infinite alternate; }
.ab-blob .inner {
  width: 100%;
  height: 100%;
  filter: blur(85px);
  animation: ab-breathe 8s ease-in-out infinite;
}

/* 呼吸灯:光斑明暗起伏(偏暗,深色为主) */
@keyframes ab-breathe {
  0% { opacity: 0.16; transform: scale(0.95); }
  50% { opacity: 0.4; transform: scale(1.06); }
  100% { opacity: 0.16; transform: scale(0.95); }
}
@keyframes ab-drift {
  from { transform: translate(0, 0) rotate(0deg); }
  to { transform: translate(6vw, 5vh) rotate(14deg); }
}
/* 边缘轻压,保持深色为主的基调 */
.ab-vignette {
  position: absolute;
  inset: 0;
  background: radial-gradient(closest-side at 50% 46%, transparent 58%, color-mix(in srgb, var(--bg) 38%, transparent) 145%, color-mix(in srgb, var(--bg) 60%, transparent) 210%);
}
[data-theme='light'] .ab-blob .inner { opacity: 0.38; }
</style>
