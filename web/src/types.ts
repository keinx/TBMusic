export interface Song {
  // songId/albumId/albumMid/lrcUrl 仅序列化透传给后端/LX 脚本,前端逻辑不读取
  source: string
  songmid: string
  songId: string
  albumId?: string
  albumMid?: string
  albumName?: string
  hash?: string
  copyrightId?: string
  name: string
  singer: string
  interval: string
  pic?: string
  lrcUrl?: string
  qualities?: string[]
}

export interface Album {
  id: string
  name: string
  singer: string
  pic: string
  publish?: string
}

export interface Songlist {
  id: string
  name: string
  creator: string
  pic: string
  count?: number
}

export interface Artist {
  id: string
  name: string
  pic: string
}


export interface SongEntry {
  id: number
  musicInfo: Song
  addedAt: number
}

export interface Playlist {
  id: number
  name: string
  isDefault: boolean
  count: number
  createdAt: number
}

export interface SourceInfo {
  id: string
  name: string
  searchTypes: string[]
  qualitys: string[]
  ready: boolean
}

export interface SourcesResp {
  script: {
    name?: string
    version?: string
    author?: string
    status: 'ok' | 'error' | 'loading'
    error?: string
    updateAlert?: string
  }
  sources: Record<string, SourceInfo>
}

export function songKey(s: Song): string {
  let id = s.songmid || s.hash || s.copyrightId || ''
  return `${s.source}:${id}`
}

export const SOURCE_NAMES: Record<string, string> = {
  kw: '酷我', tx: 'QQ', wy: '网易', kg: '酷狗', mg: '咪咕',
}

export const SEARCH_TYPES = [
  { id: 'music', name: '歌曲' },
  { id: 'album', name: '专辑' },
  { id: 'songlist', name: '歌单' },
  { id: 'artist', name: '歌手' },
  { id: 'lyric', name: '歌词' },
]

export function qualityLabel(q: string): string {
  switch (q) {
    case '128k': return '128K'
    case '192k': return '192K'
    case '320k': return '320K'
    case 'flac': return '无损'
    case 'flac24bit': return 'Hi-Res'
    case 'hires': return 'Hi-Res'
    default: return q.toUpperCase()
  }
}
