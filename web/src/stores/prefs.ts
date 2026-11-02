import { safeStorage } from '../utils/safeStorage'
import { defineStore } from 'pinia'
import { ref } from 'vue'

export type SpectrumStyle = 'bars' | 'wave' | 'dots'
type ThemeMode = 'dark' | 'light' | 'system'
export type AccentColor = 'blue' | 'yellow' | 'green' | 'purple' | 'red'

function lsGet(key: string, def: string): string {
  return safeStorage.getItem(key) ?? def
}

// 个性化偏好:主题(默认跟随系统) / 主题色(默认绿色) / 频谱样式 / 光盘旋转封面
export const usePrefs = defineStore('prefs', () => {
  const theme = ref<ThemeMode>(lsGet('tbmusic_theme', 'system') as ThemeMode)
  const accent = ref<AccentColor>(lsGet('tbmusic_accent', 'green') as AccentColor)
  const spectrumStyle = ref<SpectrumStyle>((() => {
    const v = lsGet('tbmusic_spectrum', 'bars')
    return v === 'wave' || v === 'dots' ? v : 'bars'
  })())
  const discSpin = ref(lsGet('tbmusic_disc', 'off') === 'on')

  // 系统深浅色侦测
  const media = window.matchMedia('(prefers-color-scheme: dark)')
  function effectiveTheme(t: string): 'dark' | 'light' {
    if (t === 'light' || t === 'dark') return t
    return media.matches ? 'dark' : 'light'
  }
  function applyTheme() {
    document.documentElement.setAttribute('data-theme', effectiveTheme(theme.value))
  }
  function setTheme(t: string) {
    theme.value = t as ThemeMode
    safeStorage.setItem('tbmusic_theme', t)
    applyTheme()
  }
  media.addEventListener('change', () => {
    if (theme.value === 'system') applyTheme()
  })
  function setAccent(a: AccentColor) {
    accent.value = a
    safeStorage.setItem('tbmusic_accent', a)
    document.documentElement.setAttribute('data-accent', a)
    updateFavicon()
  }

  // favicon 跟随主题色:动态生成 SVG data-URI 替换(静态文件作为 JS 前的兜底)
  function updateFavicon() {
    const cs = getComputedStyle(document.documentElement)
    const a1 = cs.getPropertyValue('--accent-2').trim() || '#7fe8bd'
    const a2 = cs.getPropertyValue('--accent').trim() || '#2ecf8f'
    const svg =
      `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48" fill="none">` +
      `<defs><linearGradient id="c" x1="6" y1="4" x2="42" y2="44" gradientUnits="userSpaceOnUse">` +
      `<stop offset="0" stop-color="${a1}"/><stop offset="1" stop-color="${a2}"/></linearGradient></defs>` +
      `<circle cx="24" cy="24" r="22" fill="url(#c)"/>` +
      `<circle cx="24" cy="24" r="21" fill="none" stroke="rgba(0,0,0,0.26)" stroke-width="1.4"/>` +
      `<circle cx="24" cy="24" r="17.6" fill="none" stroke="rgba(255,255,255,0.65)" stroke-width="1.5" stroke-dasharray="2.6 3.2"/>` +
      `<path d="M20.2 12.4 L31.5 9.2 L31.5 26.8 C31.5 29.4 29.4 31.2 26.9 31.2 C24.4 31.2 22.6 29.4 22.6 27.1 C22.6 24.8 24.4 23.1 26.8 23.1 C27.7 23.1 28.5 23.35 29.1 23.8 L29.1 13.6 L22.9 15.4 L22.9 29.4 C22.9 32 20.8 33.8 18.3 33.8 C15.8 33.8 14 32 14 29.7 C14 27.4 15.8 25.7 18.2 25.7 C19.1 25.7 19.9 25.95 20.5 26.4 Z" fill="#fff" fill-opacity="0.95" transform="translate(0.6, 1.4)"/>` +
      `</svg>`
    const href = 'data:image/svg+xml,' + encodeURIComponent(svg)
    let link = document.querySelector<HTMLLinkElement>('link[rel="icon"][type="image/svg+xml"]')
    if (!link) {
      link = document.createElement('link')
      link.rel = 'icon'
      link.type = 'image/svg+xml'
      document.head.appendChild(link)
    }
    link.href = href
  }
  function setSpectrumStyle(s: SpectrumStyle) {
    spectrumStyle.value = s
    safeStorage.setItem('tbmusic_spectrum', s)
  }
  function setDiscSpin(on: boolean) {
    discSpin.value = on
    safeStorage.setItem('tbmusic_disc', on ? 'on' : 'off')
  }

  // 立即应用
  applyTheme()
  document.documentElement.setAttribute('data-accent', accent.value)
  updateFavicon()

  return { theme, accent, spectrumStyle, discSpin, setTheme, setAccent, setSpectrumStyle, setDiscSpin }
})
