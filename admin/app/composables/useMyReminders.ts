import type { components } from '~/utils/api'

export type MyReminder = components['schemas']['MyReminder']

// 我的提醒（门店 AI 写给本人的话）：侧栏计数、首页和「我的提醒」页共用一份列表，任一处标了已读各处同时更新。
// 列表按员工记，换人登录后不会显示上一个人的提醒。
interface ReminderState {
  staffId: string
  items: MyReminder[]
}

// 一条话题的父条或任一条后续没读，这条话题就算没读。
export function reminderUnread(item: MyReminder) {
  return !item.read_at || item.replies.some(reply => !reply.read_at)
}

export function useMyReminders() {
  const api = useApi()
  const toast = useToast()
  const { me } = useMe()
  const state = useState<ReminderState | null>('my-reminders', () => null)
  const loadError = useState<string>('my-reminders-error', () => '')
  const generation = useState<number>('my-reminders-generation', () => 0)
  const marking = useState<Record<string, boolean>>('my-reminders-marking', () => ({}))

  const mine = computed(() => {
    const staffId = me.value?.staff.id
    return staffId && state.value?.staffId === staffId ? state.value : null
  })
  const items = computed(() => mine.value?.items ?? [])
  const unreadItems = computed(() => items.value.filter(reminderUnread))
  const loaded = computed(() => !!mine.value)

  // quiet：后台定时刷新失败时不弹提示。
  async function load(options: { quiet?: boolean } = {}) {
    const staffId = me.value?.staff.id
    if (!staffId) return
    const current = ++generation.value
    const { data, error } = await api.POST('/api/ops/list_my_reminders', { body: {} })
    if (current !== generation.value || me.value?.staff.id !== staffId) return
    if (!data) {
      if (!loaded.value) loadError.value = errorText(error)
      else if (!options.quiet) toast.add({ title: errorText(error), color: 'error' })
      return
    }
    loadError.value = ''
    state.value = { staffId, items: data.items }
  }

  // 标为已读：先在本地改掉，没存上就退回原样并提示。后端把父条和它的后续一起标掉。
  // 在途的读取会把本地改动盖回去，先作废它；存上后再读一次，以后端为准（打开页面之后才来的后续也一并显示成已读）。
  async function markRead(id: string) {
    const staffId = me.value?.staff.id
    const item = items.value.find(row => row.id === id)
    if (!staffId || !item || marking.value[id] || !reminderUnread(item)) return
    generation.value++
    marking.value[id] = true
    const before = { read_at: item.read_at, replies: item.replies.map(reply => reply.read_at) }
    const now = new Date().toISOString()
    item.read_at ??= now
    for (const reply of item.replies) reply.read_at ??= now
    let ok = false
    try {
      const { data, error } = await api.POST('/api/ops/mark_my_reminder_read', { body: { id } })
      ok = !!data
      if (!ok && me.value?.staff.id === staffId) toast.add({ title: errorText(error), color: 'error' })
    } catch (e) {
      if (me.value?.staff.id === staffId) toast.add({ title: errorText(e), color: 'error' })
    } finally {
      marking.value[id] = false
      if (!ok && state.value?.staffId === staffId) {
        item.read_at = before.read_at
        item.replies.forEach((reply, i) => {
          reply.read_at = before.replies[i] ?? null
        })
      }
    }
    if (ok) await load({ quiet: true })
  }

  // 换人登录时丢掉上一个人的列表和进行中的请求。
  function reset() {
    generation.value++
    state.value = null
    loadError.value = ''
  }

  return { items, unreadItems, loaded, loadError, marking, load, markRead, reset }
}
