<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import Icon from '../components/Icon.vue'
import { toast } from '../toast'

const router = useRouter()
const keyword = ref('')

// 手机端搜索框提示文字更短
const mq = window.matchMedia('(max-width: 768px)')
const isMobile = ref(mq.matches)
const onMq = (e: MediaQueryListEvent) => { isMobile.value = e.matches }
onMounted(() => mq.addEventListener('change', onMq))
onBeforeUnmount(() => mq.removeEventListener('change', onMq))
const placeholder = computed(() => (isMobile.value ? '输入关键词搜索' : '搜索歌曲、歌手、专辑、歌单、歌词…'))

function doSearch() {
  const kw = keyword.value.trim()
  if (!kw) { toast('想听什么?输入关键词吧'); return }
  router.push({ path: '/search', query: { keyword: kw } })
}
</script>

<template>
  <div class="home">
    <div class="home-body">
      <h1 class="home-title">投币音乐</h1>
      <p class="home-sub">聚合全网音源 · 随心聆听</p>

      <div class="search-box">
        <Icon name="search" :size="18" class="search-icon" />
        <input
          v-model="keyword"
          class="search-input"
          :placeholder="placeholder"
          @keyup.enter="doSearch"
        />
        <button class="search-btn" @click="doSearch">搜索</button>
      </div>

      <div class="home-links">
        <router-link to="/hot"><Icon name="flame" :size="15" /> 热门榜单</router-link>
        <router-link to="/random"><Icon name="shuffle" :size="15" /> 随机歌单</router-link>
        <router-link to="/playlists"><Icon name="heart" :size="15" /> 我的收藏</router-link>
      </div>
    </div>
  </div>
</template>

<style scoped>
.home {
  position: relative;
  min-height: calc(100vh - 58px);
  display: flex;
  align-items: center;
  justify-content: center;
}
.home-body {
  position: relative;
  z-index: 2;
  width: min(620px, 92vw);
  text-align: center;
  padding: 30px 0;
}
.home-title {
  margin: 0 0 6px;
  font-size: clamp(30px, 5vw, 44px);
  letter-spacing: 4px;
  color: var(--text);
}
.home-sub { margin: 0 0 30px; color: var(--text-dim); font-size: 14.5px; }

.search-box {
  display: flex;
  align-items: center;
  gap: 8px;
  background: color-mix(in srgb, var(--panel) 80%, transparent);
  backdrop-filter: blur(16px);
  border: 1px solid var(--border);
  border-radius: 16px;
  padding: 9px;
  box-shadow: 0 16px 50px rgba(0, 0, 0, 0.28);
}
.search-icon { color: var(--text-dim); margin-left: 8px; flex-shrink: 0; }
.search-input {
  flex: 1;
  border: none;
  background: transparent;
  color: var(--text);
  font-size: 15.5px;
  padding: 10px 6px;
  min-width: 0;
}
.search-input::placeholder { color: var(--text-dim); opacity: 0.75; }
.search-btn {
  padding: 11px 26px;
  border-radius: 12px;
  font-size: 14.5px;
  font-weight: 600;
  color: var(--accent-contrast);
  background: var(--accent-grad);
  white-space: nowrap;
  transition: all 0.15s;
  box-shadow: 0 4px 16px color-mix(in srgb, var(--accent) 35%, transparent);
}
.search-btn:hover { filter: brightness(1.06); transform: translateY(-1px); }

.home-links {
  margin-top: 32px;
  display: flex;
  justify-content: center;
  gap: 12px;
  flex-wrap: wrap;
}
.home-links a {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--text-dim);
  font-size: 13.5px;
  padding: 8px 18px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--panel) 55%, transparent);
  border: 1px solid var(--border);
  backdrop-filter: blur(8px);
  transition: all 0.15s;
}
.home-links a:hover { color: var(--accent); border-color: var(--accent); }

@media (max-width: 768px) {
  .search-btn { padding: 10px 18px; }
}
</style>
