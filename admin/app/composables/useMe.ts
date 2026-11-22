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

  // 传数组表示有其中任一项即可（例如报表入口：营业报表或员工业绩）。
  function can(permission: string | readonly string[]): boolean {
    if (!me.value) return false
    const wanted = typeof permission === 'string' ? [permission] : permission
    return me.value.is_owner || me.value.permissions.some(p => wanted.includes(p.permission))
  }

  // 某项权限的数据范围和时间窗口；老板不受限。没有该权限时返回 null。
  function grant(permission: string): { scope: 'self' | 'store', windowDays: number | null } | null {
    if (!me.value) return null
    if (me.value.is_owner) return { scope: 'store', windowDays: null }
    const specs = me.value.permissions.filter(p => p.permission === permission)
    if (!specs.length) return null
    // 后端已按"限制取最宽"合并，这里只取第一条。
    const spec = specs[0]!
    return { scope: spec.scope === 'self' ? 'self' : 'store', windowDays: spec.window_days ?? null }
  }

  return { me, load, can, grant }
}
