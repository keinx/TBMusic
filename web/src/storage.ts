// localStorage 版本号 + schema 校验。
//
// 旧实现把偏好/队列直接写进 localStorage 且没有任何版本与类型校验:
// 一旦字段语义变化(或外部写入了脏值),读取端会拿到类型不符的数据,
// 表现为"设置不生效""队列错乱""页面白屏",而且无法区分是数据旧还是逻辑错。
//
// 这里给所有受管键加一个统一的 schema 版本:版本不一致或有脏值时,
// 按当前 schema 逐键校验,非法值直接清掉(回落到代码里的默认值)。

export const SCHEMA_VERSION = 2

const SCHEMA_KEY = 'tbmusic_schema'

/** 受管键:版本升级时只处理这些,不碰其它键 */
const MANAGED_KEYS = [
  'tbmusic_volume',
  'tbmusic_muted',
  'tbmusic_mode',
  'tbmusic_quality',
  'tbmusic_theme',
  'tbmusic_accent',
  'tbmusic_spectrum',
  'tbmusic_disc',
  'tbmusic_player_state',
]

type Validator = (raw: string) => boolean

const volume: Validator = (v) => {
  const n = Number(v)
  return v.trim() !== '' && Number.isFinite(n) && n >= 0 && n <= 1
}
const oneOf = (...values: string[]): Validator => (v) => values.includes(v)

const validators: Record<string, Validator> = {
  tbmusic_volume: volume,
  tbmusic_muted: oneOf('0', '1'),
  tbmusic_mode: oneOf('loop', 'one', 'random'),
  // 音质标识:只允许短的字母数字(避免把任意字符串当参数透传给后端)
  tbmusic_quality: (v) => /^[a-z0-9]{1,16}$/i.test(v),
  tbmusic_theme: oneOf('dark', 'light', 'system'),
  tbmusic_accent: oneOf('blue', 'yellow', 'green', 'purple', 'red'),
  tbmusic_spectrum: oneOf('bars', 'wave', 'dots'),
  tbmusic_disc: oneOf('on', 'off'),
  // 队列状态只做结构级校验(是否有数组 q),逐字段校验在 player store 里做
  tbmusic_player_state: (v) => {
    try {
      const st = JSON.parse(v)
      return !!st && typeof st === 'object' && Array.isArray((st as { q?: unknown }).q)
    } catch {
      return false
    }
  },
}

function sanitize() {
  for (const key of MANAGED_KEYS) {
    const raw = localStorage.getItem(key)
    if (raw === null) continue
    const check = validators[key]
    if (!check || !check(raw)) localStorage.removeItem(key)
  }
}

/** 应用启动时调用一次(必须在任何 store 实例化之前) */
export function initStorage() {
  try {
    const raw = localStorage.getItem(SCHEMA_KEY)
    const version = raw === null ? 0 : Number(raw)
    // 版本不一致(含首次运行/旧版本无版本号)时按当前 schema 重新校验一遍;
    // 这里不清空所有偏好,只丢弃不符合当前 schema 的值。
    if (version !== SCHEMA_VERSION) sanitize()
    localStorage.setItem(SCHEMA_KEY, String(SCHEMA_VERSION))
  } catch {
    // 隐私模式 / 配额耗尽:localStorage 不可用时不应影响启动
  }
}
