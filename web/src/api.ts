// 统一 API 请求封装:401 时触发登录遮罩
let onUnauthorized: ((accountChanged?: boolean) => void) | null = null
let expectedUserID: number | null = null
export function setExpectedAccount(id: number | null) { expectedUserID = id }
let sessionGeneration = 0
const pendingRequests = new Set<AbortController>()

export const sessionVersion = () => sessionGeneration

export function invalidateSessionRequests() {
  sessionGeneration++
  for (const ctl of pendingRequests) ctl.abort()
  pendingRequests.clear()
}

export function setUnauthorizedHandler(fn: (accountChanged?: boolean) => void) {
  onUnauthorized = fn
}

// ApiError 携带 HTTP 状态码与业务码,便于调用方区分「限流/无资源/未登录」
export class ApiError extends Error {
  status: number
  code?: string
  constructor(message: string, status: number, code?: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

export interface RequestOptions { signal?: AbortSignal; timeoutMs?: number }

async function request(method: string, url: string, body?: any, options: RequestOptions = {}): Promise<any> {
  const generation = sessionGeneration
  const opts: RequestInit = { method, headers: {} }
  if (body !== undefined) {
    opts.headers = { 'Content-Type': 'application/json' }
    opts.body = JSON.stringify(body)
  }
  if (!/^\/api\/auth\/(me|config|login|register)(\?|$)/.test(url)) {
    ;(opts.headers as Record<string, string>)['X-TBMusic-User-ID'] = String(expectedUserID ?? 0)
  }
  // 库操作不应跟音源解析一起等 30s;超时覆盖响应体读取,不只覆盖响应头。
  const ctl = new AbortController()
  pendingRequests.add(ctl)
  let timedOut = false
  const timeout = options.timeoutMs ?? (/^\/api\/(playlists|blacklist)(\/|\?|$)/.test(url) ? 10000 : 30000)
  const timer = setTimeout(() => { timedOut = true; ctl.abort() }, timeout)
  const abort = () => ctl.abort()
  options.signal?.addEventListener('abort', abort, { once: true })
  if (options.signal?.aborted) ctl.abort()
  opts.signal = ctl.signal
  try {
    const resp = await fetch(url, opts)
    let data: any = null
    try {
      data = await resp.json()
    } catch (error) {
      if (ctl.signal.aborted) throw error
      data = null
    }
    if (generation !== sessionGeneration) throw new DOMException('账户已变更', 'AbortError')
    if (resp.status === 409 && data?.code === 'account_changed') {
      onUnauthorized?.(true)
      throw new ApiError(data.error || '账户已变更', 409, data.code)
    }
    if (resp.status === 401) {
      if (onUnauthorized) onUnauthorized()
      // 保留服务端文案(如"未登录"/"会话已过期"),不要用固定串把它盖掉
      throw new ApiError(data?.error || '未登录', 401, data?.code)
    }
    if (!resp.ok) {
      throw new ApiError(data?.error || `请求失败 (${resp.status})`, resp.status, data?.code)
    }
    return data
  } catch (error) {
    if (timedOut) throw new ApiError('请求超时，请检查网络后重试', 408)
    throw error
  } finally {
    pendingRequests.delete(ctl)
    clearTimeout(timer)
    options.signal?.removeEventListener('abort', abort)
  }
}

export const api = {
  get: (url: string) => request('GET', url),
  post: (url: string, body?: any, options?: RequestOptions) => request('POST', url, body, options),
  put: (url: string, body?: any, options?: RequestOptions) => request('PUT', url, body, options),
  patch: (url: string, body?: any) => request('PATCH', url, body),
  del: (url: string, options?: RequestOptions) => request('DELETE', url, undefined, options),
}
