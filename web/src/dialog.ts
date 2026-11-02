import { reactive } from 'vue'

// 应用内弹窗服务:替代浏览器 confirm / prompt
interface DialogState {
  visible: boolean
  title: string
  message: string
  mode: 'confirm' | 'prompt'
  input: string
  inputPlaceholder: string
  danger: boolean
  resolve: ((v: string | boolean | null) => void) | null
}

export const dialogState = reactive<DialogState>({
  visible: false,
  title: '',
  message: '',
  mode: 'confirm',
  input: '',
  inputPlaceholder: '',
  danger: false,
  resolve: null,
})

function open(opts: Partial<DialogState>, mode: 'confirm' | 'prompt'): Promise<string | boolean | null> {
  return new Promise((resolve) => {
    dialogState.visible = true
    dialogState.mode = mode
    dialogState.title = opts.title || ''
    dialogState.message = opts.message || ''
    dialogState.input = opts.input || ''
    dialogState.inputPlaceholder = opts.inputPlaceholder || ''
    dialogState.danger = !!opts.danger
    // 重复打开时先了结上一个 Promise,避免调用方永远挂起
    if (dialogState.resolve) dialogState.resolve(mode === 'confirm' ? false : null)
    dialogState.resolve = resolve
  })
}

export function confirmDialog(message: string, opts: { title?: string; danger?: boolean } = {}): Promise<boolean> {
  return open({ message, ...opts }, 'confirm') as Promise<boolean>
}

export function promptDialog(message: string, opts: { title?: string; input?: string; placeholder?: string } = {}): Promise<string | null> {
  return open({ message, inputPlaceholder: opts.placeholder || '', input: opts.input || '', ...opts }, 'prompt') as Promise<string | null>
}

export function closeDialog(result: string | boolean | null) {
  if (dialogState.resolve) dialogState.resolve(result)
  dialogState.resolve = null
  dialogState.visible = false
}
