import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import { router } from './router'
import { initStorage } from './storage'
import './styles.css'

// 必须在任何 store 实例化之前:清理不符合当前 schema 的 localStorage 数据
initStorage()

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
