<script setup lang="ts">
// 投币音乐 LOGO:硬币 + 音符,配色跟随主题色(var(--accent) / var(--accent-2))
import { useId } from 'vue'

defineProps<{ size?: number }>()
// 每实例唯一 id:移动端同时存在多个 LOGO(桌面栏 display:none)时,
// 部分手机浏览器解析 url(#id) 会命中隐藏实例导致不渲染
const uid = 'lg' + (useId().replace(/[^a-zA-Z0-9]/g, '') || Math.random().toString(36).slice(2, 8))
</script>

<template>
  <svg :width="size || 30" :height="size || 30" viewBox="0 0 48 48" fill="none" aria-hidden="true">
    <defs>
      <linearGradient :id="uid + '-coin'" x1="6" y1="4" x2="42" y2="44" gradientUnits="userSpaceOnUse">
        <stop offset="0" stop-color="#ffc857" style="stop-color: var(--accent-2)" />
        <stop offset="1" stop-color="#ff9642" style="stop-color: var(--accent)" />
      </linearGradient>
      <linearGradient :id="uid + '-note'" x1="18" y1="14" x2="30" y2="34" gradientUnits="userSpaceOnUse">
        <stop offset="0" stop-color="#fff" stop-opacity="0.98" />
        <stop offset="1" stop-color="#fff" stop-opacity="0.82" />
      </linearGradient>
    </defs>
    <circle cx="24" cy="24" r="22" :fill="`url(#${uid}-coin)`" />
    <circle cx="24" cy="24" r="21" fill="none" stroke="rgba(0, 0, 0, 0.26)" stroke-width="1.4" />
    <circle cx="24" cy="24" r="17.6" fill="none" stroke="rgba(255, 255, 255, 0.65)" stroke-width="1.5" stroke-dasharray="2.6 3.2" />
    <!-- 双八分音符 -->
    <path d="M20.2 12.4 L31.5 9.2 L31.5 26.8 C31.5 29.4 29.4 31.2 26.9 31.2 C24.4 31.2 22.6 29.4 22.6 27.1 C22.6 24.8 24.4 23.1 26.8 23.1 C27.7 23.1 28.5 23.35 29.1 23.8 L29.1 13.6 L22.9 15.4 L22.9 29.4 C22.9 32 20.8 33.8 18.3 33.8 C15.8 33.8 14 32 14 29.7 C14 27.4 15.8 25.7 18.2 25.7 C19.1 25.7 19.9 25.95 20.5 26.4 Z" :fill="`url(#${uid}-note)`" transform="translate(0.6, 1.4)" />
  </svg>
</template>
