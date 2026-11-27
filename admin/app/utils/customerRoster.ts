import { ref } from 'vue'
import type { components } from './api'

// 顾客名册：页面上一批单子、售后单要显示的顾客名，一次批量取回，称呼统一。
// 这个文件不用 Nuxt 的自动导入，单元测试（test/unit）能直接引。
type Customer = components['schemas']['Customer']

// 称呼以 GLOSSARY 为准：没认出是谁的叫「未识别顾客」（不叫散客、匿名顾客），认出了但没留姓名的叫「未留姓名的顾客」。
export const UNIDENTIFIED_CUSTOMER = '未识别顾客'
export const UNNAMED_CUSTOMER = '未留姓名的顾客'

export function customerLabel(customer: { name?: string | null, unidentified?: boolean }) {
  if (customer.unidentified) return UNIDENTIFIED_CUSTOMER
  return customer.name || UNNAMED_CUSTOMER
}

// 后端一次最多收 200 个 id。
const BATCH = 200

export type RosterEntry = { label: string, identified: boolean }

// fetch 取一批顾客，读失败返回 null；allowed 是有没有看顾客的权限，没有就不取，页面显示调用方给的默认称呼。
// 每次 resolve 都重新取这一批（一次请求），顾客改了名、补了手机号，下次刷新列表就看得到；正在取的 id 不重复取。
// 读失败不记，下次再取；问了却没返回的（看不到、已删除）从名册里去掉。
export function customerRosterState(fetch: (ids: string[]) => Promise<Customer[] | null>, allowed: () => boolean) {
  const entries = ref<Record<string, RosterEntry>>({})
  const inFlight = new Set<string>()

  async function resolve(ids: readonly string[]) {
    if (!allowed()) return
    const wanted = [...new Set(ids)].filter(id => id && !inFlight.has(id))
    if (!wanted.length) return
    for (const id of wanted) inFlight.add(id)
    try {
      const batches: string[][] = []
      for (let i = 0; i < wanted.length; i += BATCH) batches.push(wanted.slice(i, i + BATCH))
      const results = await Promise.all(batches.map(fetch))
      results.forEach((found, i) => {
        if (!found) return
        const asked = new Set(batches[i])
        const next = Object.fromEntries(Object.entries(entries.value).filter(([id]) => !asked.has(id)))
        for (const customer of found) next[customer.id] = { label: customerLabel(customer), identified: !customer.unidentified }
        entries.value = next
      })
    } finally {
      for (const id of wanted) inFlight.delete(id)
    }
  }

  // 名册里有就用名册的称呼，还没取到或看不到就用 fallback。
  function label(id: string, fallback: string) {
    return entries.value[id]?.label ?? fallback
  }

  // 认出是谁了吗：还没取到（或没有权限）时是 undefined。
  function identified(id: string) {
    return entries.value[id]?.identified
  }

  return { entries, resolve, label, identified }
}
