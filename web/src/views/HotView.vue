<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api'
import Icon from '../components/Icon.vue'
import CoverImage from '../components/CoverImage.vue'

const router = useRouter()
const lists = ref<{ id: string; name: string; pic: string; freq?: string }[]>([])
const loading = ref(true)

onMounted(async () => {
  try {
    const d = await api.get('/api/toplists')
    lists.value = d.list || []
  } catch { /* ignore */ } finally {
    loading.value = false
  }
})

function open(item: { id: string; name: string; pic: string }) {
  router.push({
    path: `/detail/songlist/wy/${encodeURIComponent(item.id)}`,
    query: { name: item.name || '', pic: item.pic || '' },
  })
}
</script>

<template>
  <div class="page">
    <div class="head-row">
      <h2 class="page-title"><Icon name="flame" :size="24" style="color: #ff7a45; vertical-align: -5px" /> 热门榜单</h2>
    </div>

    <div v-if="loading" class="loading-line">榜单加载中…</div>
    <div v-else-if="lists.length" class="hot-grid">
      <div v-for="t in lists" :key="t.id" class="hot-card" @click="open(t)">
        <div class="hot-cover">
          <CoverImage :src="t.pic" />
          <span v-if="t.freq" class="hot-freq">{{ t.freq }}</span>
        </div>
        <div class="hot-name">{{ t.name }}</div>
      </div>
    </div>
    <div v-else class="empty">榜单加载失败,请稍后重试</div>
  </div>
</template>

<style scoped>
.head-row { margin-bottom: 14px; }
.page-title { display: flex; align-items: center; gap: 8px; margin: 8px 0 12px; }
.hot-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
  gap: 18px;
}
.hot-card { cursor: pointer; }
.hot-cover {
  position: relative;
  border-radius: 13px;
  overflow: hidden;
  aspect-ratio: 1;
  background: var(--panel);
}
.hot-cover :is(img, svg) { display: block; width: 100%; height: 100%; object-fit: cover; transition: transform 0.3s; }
.hot-card:hover .hot-cover :is(img, svg) { transform: scale(1.05); }
.hot-freq {
  position: absolute;
  left: 8px;
  bottom: 8px;
  font-size: 11px;
  color: #fff;
  background: rgba(0, 0, 0, 0.55);
  backdrop-filter: blur(4px);
  padding: 2px 8px;
  border-radius: 999px;
}
.hot-name {
  font-size: 13.5px;
  margin-top: 8px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
