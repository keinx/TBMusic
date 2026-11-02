<script setup lang="ts">
import { onMounted, onBeforeUnmount } from 'vue'
import { useUpdates } from '../stores/updates'
import Icon from './Icon.vue'

const updates = useUpdates()
let stop: (() => void) | undefined
onMounted(() => { stop = updates.start() })
onBeforeUnmount(() => stop?.())
</script>

<template>
  <aside v-if="updates.noticeVisible" class="update-notice" role="status" aria-live="polite">
    <Icon name="refresh" :size="20" class="update-icon" />
    <div class="update-copy"><b>系统已更新</b><span>刷新页面即可使用最新版本</span></div>
    <button class="btn primary small" @click="updates.refresh">刷新页面</button>
    <button class="update-later" title="稍后更新" aria-label="稍后更新" @click="updates.dismiss"><Icon name="x" :size="16" /></button>
  </aside>
</template>

<style scoped>
.update-notice { position: fixed; z-index: 2200; top: calc(68px + env(safe-area-inset-top, 0px)); right: 16px; width: min(440px, calc(100vw - 32px)); box-sizing: border-box; display: flex; align-items: center; gap: 12px; padding: 14px; border: 1px solid var(--accent); border-radius: 14px; background: var(--panel); color: var(--text); box-shadow: var(--shadow); }
.update-icon { color: var(--accent); flex-shrink: 0; }
.update-copy { display: flex; flex-direction: column; gap: 4px; flex: 1; min-width: 0; }
.update-copy b { font-size: 14px; }
.update-copy span { font-size: 12px; color: var(--text-dim); }
.update-notice .btn { flex-shrink: 0; min-height: 36px; }
.update-later { display: flex; align-items: center; justify-content: center; min-width: 28px; min-height: 36px; color: var(--text-dim); }
@media (max-width: 420px) { .update-notice { gap: 8px; padding: 12px; right: 12px; width: calc(100vw - 24px); } .update-icon { display: none; } }
</style>
