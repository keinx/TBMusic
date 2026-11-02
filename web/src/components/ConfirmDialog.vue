<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { dialogState, closeDialog } from '../dialog'
import Icon from './Icon.vue'

const inputEl = ref<HTMLInputElement>()

watch(() => dialogState.visible, async (v) => {
  if (v) {
    await nextTick()
    if (dialogState.mode === 'prompt') inputEl.value?.focus()
  }
})
</script>

<template>
  <transition name="fade">
    <div v-if="dialogState.visible" class="mask" @click.self="closeDialog(null)">
      <div class="box">
        <div class="icon-wrap" :class="{ danger: dialogState.danger }">
          <Icon :name="dialogState.danger ? 'ban' : 'check'" :size="20" />
        </div>
        <h3 v-if="dialogState.title" class="title">{{ dialogState.title }}</h3>
        <p class="msg">{{ dialogState.message }}</p>
        <input
          v-if="dialogState.mode === 'prompt'"
          ref="inputEl"
          v-model="dialogState.input"
          class="input"
          :placeholder="dialogState.inputPlaceholder"
          @keyup.enter="closeDialog(dialogState.input)"
          @keyup.esc="closeDialog(null)"
        />
        <div class="ops">
          <button class="btn" @click="closeDialog(null)">取消</button>
          <button
            class="btn"
            :class="dialogState.danger ? 'danger-solid' : 'primary'"
            @click="closeDialog(dialogState.mode === 'prompt' ? dialogState.input : true)"
          >确定</button>
        </div>
      </div>
    </div>
  </transition>
</template>

<style scoped>
.mask {
  position: fixed;
  inset: 0;
  z-index: 2500;
  background: rgba(8, 8, 14, 0.55);
  backdrop-filter: blur(5px);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 18px;
}
.box {
  width: 360px;
  max-width: 100%;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 16px;
  padding: 22px;
  box-shadow: var(--shadow);
  text-align: center;
}
.icon-wrap {
  width: 44px;
  height: 44px;
  margin: 0 auto 10px;
  border-radius: 50%;
  background: var(--accent-soft);
  color: var(--accent);
  display: flex;
  align-items: center;
  justify-content: center;
}
.icon-wrap.danger { background: rgba(255, 90, 106, 0.12); color: var(--danger); }
.title { margin: 0 0 6px; font-size: 16px; }
.msg { margin: 0 0 14px; font-size: 13.5px; color: var(--text-dim); line-height: 1.6; white-space: pre-wrap; }
.box .input { margin-bottom: 14px; text-align: left; }
.ops { display: flex; gap: 10px; }
.ops .btn { flex: 1; justify-content: center; padding: 10px 0; }
.btn.danger-solid {
  background: var(--danger);
  color: #fff;
  border-color: transparent;
}
.fade-enter-active, .fade-leave-active { transition: opacity 0.18s; }
.fade-enter-from, .fade-leave-to { opacity: 0; }
</style>
