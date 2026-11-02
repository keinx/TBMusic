import { ref } from 'vue'
import { api } from '../api'
import { songKey, Song } from '../types'
import { toast } from '../toast'
import { usePlayer } from '../stores/player'

// 只等待下载地址;音频交给浏览器下载管理器,避免 Blob 占用手机内存。
const preparing = ref(new Set<string>())
export function isDownloading(song: Song): boolean {
  return preparing.value.has(songKey(song))
}

export async function downloadSong(song: Song) {
  const key = songKey(song)
  if (preparing.value.has(key)) return
  preparing.value.add(key)
  const notice = toast(`正在准备下载「${song.name}」…`, 0)
  try {
    const d = await api.post('/api/music-url', {
      source: song.source,
      quality: usePlayer().qualityPref,
      musicInfo: song,
    })
    const url = new URL(d.stream, window.location.origin)
    if (url.origin !== window.location.origin || url.pathname !== '/api/stream') throw new Error('下载地址无效')
    url.searchParams.set('download', song.name || '未命名')
    const a = document.createElement('a')
    a.href = url.href
    a.download = '' // 服务端 Content-Disposition 提供文件名及正确扩展名
    document.body.appendChild(a)
    a.click()
    a.remove()
    notice.update(`「${song.name}」已交给浏览器下载，请查看下载列表`)
  } catch (e: any) {
    notice.update(e?.message || '下载失败，请重试')
  } finally {
    preparing.value.delete(key)
  }
}
