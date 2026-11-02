<script setup lang="ts">
// 随音乐跳动的频谱可视化:条形 / 波浪 / 圆点 三种样式
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { usePlayer } from '../stores/player'
import { usePrefs } from '../stores/prefs'

const props = withDefaults(defineProps<{
  height?: number
  bars?: number
  opacity?: number
    animated?: boolean  // 无音频时是否有轻微起伏
}>(), {
  height: 100,
  bars: 48,
  opacity: 1,
  animated: false,
})

const player = usePlayer()
const prefs = usePrefs()
const canvasEl = ref<HTMLCanvasElement>()
let rafId = 0
let phase = 0
let smooth: number[] = []

function currentStyle(): string {
  return prefs.spectrumStyle || 'bars'
}

// 主题色缓存:仅在 accent/theme 变化时重新读取,避免每帧 getComputedStyle
let cachedColors: [string, string] | null = null
function invalidateColors() { cachedColors = null }
const themeObserver = new MutationObserver(invalidateColors)
themeObserver.observe(document.documentElement, {
  attributes: true, attributeFilter: ['data-accent', 'data-theme'],
})
function accentColors(): [string, string] {
  if (cachedColors) return cachedColors
  const cs = getComputedStyle(document.documentElement)
  const a = cs.getPropertyValue('--accent').trim() || '#ff9642'
  const b = cs.getPropertyValue('--accent-2').trim() || '#ffc857'
  cachedColors = [a, b]
  return cachedColors
}

function hexToRgb(hex: string): [number, number, number] {
  const m = hex.replace('#', '')
  if (m.length >= 6) {
    return [parseInt(m.slice(0, 2), 16), parseInt(m.slice(2, 4), 16), parseInt(m.slice(4, 6), 16)]
  }
  return [255, 150, 66]
}

function draw() {
  const canvas = canvasEl.value
  if (!canvas) return
  const ctx = canvas.getContext('2d')
  if (!ctx) return
  const dpr = Math.min(window.devicePixelRatio || 1, 2)
  const w = canvas.clientWidth
  const h = canvas.clientHeight
  if (w === 0 || h === 0) {
    rafId = requestAnimationFrame(draw)
    return
  }
  if (canvas.width !== Math.round(w * dpr) || canvas.height !== Math.round(h * dpr)) {
    canvas.width = Math.round(w * dpr)
    canvas.height = Math.round(h * dpr)
  }
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
  ctx.clearRect(0, 0, w, h)

  const bars = props.bars
  let data = player.playing ? player.getSpectrum(bars) : []
  if (data.length !== bars) data = []
  phase += 0.03
  if (smooth.length !== bars) smooth = new Array(bars).fill(0)
  for (let i = 0; i < bars; i++) {
    const idle = props.animated ? 0.06 + 0.045 * (0.5 + 0.5 * Math.sin(phase + i * 0.55)) : 0.035
    const target = data.length ? data[i] : idle
    smooth[i] += (target - smooth[i]) * (data.length ? 0.55 : 0.08)
  }

  const [c1, c2] = accentColors()
  const [r1, g1, b1] = hexToRgb(c1)
  const [r2, g2, b2] = hexToRgb(c2)

  const st = currentStyle()

  const grad = ctx.createLinearGradient(0, h, 0, 0)
  grad.addColorStop(0, `rgba(${r1},${g1},${b1},0.14)`)
  grad.addColorStop(0.55, `rgba(${r1},${g1},${b1},0.42)`)
  grad.addColorStop(1, `rgba(${r2},${g2},${b2},0.75)`)
  ctx.fillStyle = grad
  ctx.strokeStyle = grad
  ctx.lineWidth = 2.5
  ctx.lineCap = 'round'

  if (st === 'wave') {
    // 平滑波浪(镜像填充)
    const mid = h * 0.5
    ctx.beginPath()
    const step = w / (bars - 1)
    ctx.moveTo(0, mid)
    for (let i = 0; i < bars; i++) {
      const x = i * step
      const y = mid - smooth[i] * mid * 0.95
      if (i === 0) ctx.lineTo(x, y)
      else {
        const px = (i - 1) * step
        const py = mid - smooth[i - 1] * mid * 0.95
        ctx.quadraticCurveTo(px + step / 2, py, x, y)
      }
    }
    ctx.lineTo(w, mid)
    for (let i = bars - 1; i >= 0; i--) {
      const x = i * step
      const y = mid + smooth[i] * mid * 0.7
      const px = (i) * step
      ctx.quadraticCurveTo(px - step / 2, mid + smooth[i] * mid * 0.7, x, y)
    }
    ctx.closePath()
    ctx.globalAlpha = 0.85
    ctx.fill()
    ctx.globalAlpha = 1
  } else if (st === 'dots') {
    const step = w / bars
    const r = Math.max(1.5, step * 0.22)
    for (let i = 0; i < bars; i++) {
      const x = i * step + step / 2
      const y = h - smooth[i] * h * 0.9 - r
      ctx.globalAlpha = 0.35 + smooth[i] * 0.65
      ctx.beginPath()
      ctx.arc(x, Math.max(r, y), r, 0, Math.PI * 2)
      ctx.fill()
    }
    ctx.globalAlpha = 1
  } else {
    // bars 条形
    const gap = Math.max(2, w * 0.004)
    const bw = (w - gap * (bars - 1)) / bars
    for (let i = 0; i < bars; i++) {
      const bh = Math.max(2, smooth[i] * h * 0.92)
      const x = i * (bw + gap)
      const y = h - bh
      const r = Math.min(bw / 2, 3)
      ctx.beginPath()
      ctx.moveTo(x, h)
      ctx.lineTo(x, y + r)
      ctx.arcTo(x, y, x + r, y, r)
      ctx.lineTo(x + bw - r, y)
      ctx.arcTo(x + bw, y, x + bw, y + r, r)
      ctx.lineTo(x + bw, h)
      ctx.closePath()
      ctx.fill()
    }
  }

  // 暂停且无空闲动画时停帧,不再满帧重绘静止画面
  if (!player.playing && !props.animated) {
    rafId = 0
    return
  }
  rafId = requestAnimationFrame(draw)
}

onMounted(() => { rafId = requestAnimationFrame(draw) })
onBeforeUnmount(() => {
  if (rafId) cancelAnimationFrame(rafId)
  themeObserver.disconnect()
})
// 恢复播放/切换样式时重启绘制循环
watch(() => player.playing, (p) => { if (p && !rafId) rafId = requestAnimationFrame(draw) })
watch(() => props.animated, (a) => { if (a && !rafId) rafId = requestAnimationFrame(draw) })
watch(() => prefs.spectrumStyle, () => {
  smooth = []
  if (!rafId) rafId = requestAnimationFrame(draw) // 停帧状态下切样式也要重绘一帧
})
// 主题色变化时(MutationObserver 置空缓存),停帧状态同样重绘一帧
watch(() => prefs.accent, () => { if (!rafId) rafId = requestAnimationFrame(draw) })
watch(() => prefs.theme, () => { if (!rafId) rafId = requestAnimationFrame(draw) })
</script>

<template>
  <canvas ref="canvasEl" class="spectrum" :style="{ height: height > 0 ? height + 'px' : '100%', opacity }"></canvas>
</template>

<style scoped>
.spectrum {
  display: block;
  width: 100%;
  pointer-events: none;
}
</style>
