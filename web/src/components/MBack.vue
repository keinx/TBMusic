<script setup lang="ts">
// 手机端二级页返回按钮:回到"我的"页(PC 端不显示)
import { useRouter } from 'vue-router'
import Icon from './Icon.vue'

const router = useRouter()
</script>

<template>
  <button class="btn small m-back" @click="router.push('/my')">
    <Icon name="chevron-left" :size="14" /> 返回
  </button>
</template>

<style scoped>
.m-back { display: none; }
@media (max-width: 768px) {
  .m-back { display: inline-flex; margin-bottom: 10px; }
}
</style>
