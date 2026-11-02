import { reactive } from 'vue'

interface ToastItem { id: number; text: string }

export const toasts = reactive<ToastItem[]>([])
let seq = 0

export function toast(text: string, duration = 2200) {
  const item = { id: ++seq, text }
  toasts.push(item)
  let timer: ReturnType<typeof setTimeout> | undefined
  function dismiss() {
    clearTimeout(timer)
    const i = toasts.findIndex((t) => t.id === item.id)
    if (i >= 0) toasts.splice(i, 1)
  }
  function expire(ms: number) {
    clearTimeout(timer)
    if (ms > 0) timer = setTimeout(dismiss, ms)
  }
  expire(duration)
  return {
    dismiss,
    update(text: string, duration = 2200) {
      const current = toasts.find((t) => t.id === item.id)
      if (current) current.text = text
      expire(duration)
    },
  }
}
