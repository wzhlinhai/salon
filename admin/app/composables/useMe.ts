import type { components } from '~/utils/api'

export type Me = components['schemas']['Me']

// 当前登录员工及其有效权限。改了权限后重新读取即可生效（后端每次请求都读当前权限）。
export function useMe() {
  const me = useState<Me | null>('me', () => null)

  async function load() {
    const token = getToken()
    const { data } = await useApi().POST('/api/ops/get_me', { body: {} })
    if (getToken() !== token) return me.value
    me.value = data ?? null
    return me.value
  }

  function can(permission: string): boolean {
    if (!me.value) return false
    return me.value.is_owner || me.value.permissions.some(p => p.permission === permission)
  }

  return { me, load, can }
}
