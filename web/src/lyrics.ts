// LRC 歌词解析与滚动高亮
export interface LyricLine {
  time: number // 秒
  text: string
  trans?: string
}

const timeRe = /\[(\d{1,2}):(\d{1,2})(?:[.:](\d{1,3}))?\]/g

function parseLrcToMap(lrc: string): Map<number, string> {
  const out = new Map<number, string>()
  if (!lrc) return out
  for (const line of lrc.split('\n')) {
    const text = line.replace(timeRe, '').trim()
    timeRe.lastIndex = 0
    let m: RegExpExecArray | null
    const times: number[] = []
    while ((m = timeRe.exec(line)) !== null) {
      const min = parseInt(m[1], 10)
      const sec = parseInt(m[2], 10)
      const frac = m[3] ? parseInt(m[3].padEnd(3, '0'), 10) / 1000 : 0
      times.push(min * 60 + sec + frac)
    }
    for (const t of times) out.set(t, text)
  }
  return out
}

export function parseLyrics(lyric: string, tlyric?: string): LyricLine[] {
  const main = parseLrcToMap(lyric)
  const trans = parseLrcToMap(tlyric || '')
  const times = [...main.keys()].sort((a, b) => a - b)
  return times.map((t) => ({
    time: t,
    text: main.get(t) || '',
    trans: trans.get(t) || undefined,
  })).filter((l) => l.text || l.trans)
}

// 当前应高亮的行索引(不超过 time 的最后一行)
export function activeLineIndex(lines: LyricLine[], t: number): number {
  if (!lines.length) return -1
  let lo = 0, hi = lines.length - 1, ans = -1
  while (lo <= hi) {
    const mid = (lo + hi) >> 1
    if (lines[mid].time <= t + 0.3) {
      ans = mid
      lo = mid + 1
    } else {
      hi = mid - 1
    }
  }
  return ans
}

export function fmtTime(sec: number): string {
  if (!isFinite(sec) || sec < 0) return '0:00'
  const m = Math.floor(sec / 60)
  const s = Math.floor(sec % 60)
  return `${m}:${s < 10 ? '0' : ''}${s}`
}

