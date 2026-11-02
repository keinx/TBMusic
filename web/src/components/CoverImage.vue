<script setup lang="ts">
// 封面图:加载失败或为空时,显示站点 LOGO(硬币+音符,配色跟随主题色)
import { computed, ref, watch, useId } from 'vue'
import { coverURL } from '../utils/cover'
const uid = 'cv' + (useId().replace(/[^a-zA-Z0-9]/g, '') || Math.random().toString(36).slice(2, 8))

const props = defineProps<{ src?: string }>()
const failed = ref(false)
const cachedSrc = computed(() => coverURL(props.src))
watch(() => props.src, () => { failed.value = false })
</script>

<template>
  <img v-if="cachedSrc && !failed" :src="cachedSrc" loading="lazy" referrerpolicy="no-referrer" @error="failed = true" />
  <svg v-else viewBox="0 0 240 240" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="封面占位图">
    <defs>
      <linearGradient :id="uid + '-coin'" x1="0" y1="0" x2="1" y2="1">
        <stop offset="0" stop-color="#ffc857" style="stop-color: var(--accent-2)" />
        <stop offset="1" stop-color="#ff9642" style="stop-color: var(--accent)" />
      </linearGradient>
      <linearGradient :id="uid + '-note'" x1="0" y1="0" x2="0.4" y2="1">
        <stop offset="0" stop-color="#fff" stop-opacity="0.98" />
        <stop offset="1" stop-color="#fff" stop-opacity="0.82" />
      </linearGradient>
    </defs>
    <rect width="240" height="240" fill="var(--bg-soft)" />
    <!-- 站点 LOGO(AppLogo 同款硬币+音符) -->
    <g transform="translate(24, 24) scale(4)">
      <circle cx="24" cy="24" r="22" :fill="`url(#${uid}-coin)`" />
      <circle cx="24" cy="24" r="21" fill="none" stroke="rgba(0, 0, 0, 0.26)" stroke-width="1.4" />
      <circle cx="24" cy="24" r="17.6" fill="none" stroke="rgba(255, 255, 255, 0.65)" stroke-width="1.5" stroke-dasharray="2.6 3.2" />
      <path
        d="M20.2 12.4 L31.5 9.2 L31.5 26.8 C31.5 29.4 29.4 31.2 26.9 31.2 C24.4 31.2 22.6 29.4 22.6 27.1 C22.6 24.8 24.4 23.1 26.8 23.1 C27.7 23.1 28.5 23.35 29.1 23.8 L29.1 13.6 L22.9 15.4 L22.9 29.4 C22.9 32 20.8 33.8 18.3 33.8 C15.8 33.8 14 32 14 29.7 C14 27.4 15.8 25.7 18.2 25.7 C19.1 25.7 19.9 25.95 20.5 26.4 Z"
        :fill="`url(#${uid}-note)`"
        transform="translate(0.6, 1.4)"
      />
    </g>
  </svg>
</template>
