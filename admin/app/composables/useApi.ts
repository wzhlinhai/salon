import createClient, { type Middleware } from 'openapi-fetch'
import type { paths } from '~/utils/api'

const TOKEN_KEY = 'salon_token'

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(token: string | null) {
  if (token) localStorage.setItem(TOKEN_KEY, token)
  else localStorage.removeItem(TOKEN_KEY)
}

// 所有请求都经过后端的业务操作接口层，权限以后端检查为准；页面只是按权限隐藏入口。
const auth: Middleware = {
  onRequest({ request }) {
    const token = getToken()
    if (token) request.headers.set('Authorization', `Bearer ${token}`)
    return request
  },
  async onResponse({ response }) {
    if (response.status === 401 && !response.url.endsWith('/api/auth/login')) {
      setToken(null)
      useState('me').value = null
      await navigateTo('/login')
    }
    return response
  }
}

let client: ReturnType<typeof createClient<paths>> | null = null

export function useApi() {
  if (!client) {
    client = createClient<paths>({ baseUrl: useRuntimeConfig().public.apiBase as string })
    client.use(auth)
  }
  return client
}

// 后端错误的说明文字（RFC 9457 problem details 的 detail 字段）。
export function errorText(error: unknown): string {
  if (error && typeof error === 'object') {
    const e = error as { detail?: string, errors?: { message?: string }[] }
    if (e.errors?.length) return e.errors.map(x => x.message).join('；')
    if (e.detail) return e.detail
  }
  return '操作失败'
}
