import type { components } from '~/utils/api'

export type Draft = components['schemas']['Draft']
export type DraftView = components['schemas']['DraftView']

// AI 草稿收件箱：侧栏计数、待确认页和 AI 对话面板共用一份列表，任一处确认或拒绝后各处同时更新。
// 列表按员工记，换人登录后不会显示上一个人的草稿；员工名用于显示草稿的发起人。
interface DraftState {
  staffId: string
  drafts: Draft[]
}

export function useDrafts() {
  const api = useApi()
  const toast = useToast()
  const { me } = useMe()
  const state = useState<DraftState | null>('drafts-list', () => null)
  const names = useState<Record<string, string>>('drafts-staff-names', () => ({}))
  // 每次加载取一个新号，返回时号码已变说明有更新的请求或换了人，丢弃结果。
  const generation = useState<number>('drafts-generation', () => 0)

  const drafts = computed(() => {
    const staffId = me.value?.staff.id
    return staffId && state.value?.staffId === staffId ? state.value.drafts : []
  })
  const pending = computed(() => drafts.value.filter(draft => draft.status === 'pending'))
  // 本人的列表一次都没读到过时读失败：待确认页原地给「重试」，别写成「没有未处理的草稿」让人以为收件箱空了。
  const loaded = computed(() => !!me.value && state.value?.staffId === me.value.staff.id)
  const loadError = useState<string>('drafts-error', () => '')

  // quiet：后台定时刷新失败时不弹提示，免得断网时每分钟弹一次。
  async function load(options: { quiet?: boolean } = {}) {
    const staffId = me.value?.staff.id
    if (!staffId) return
    const current = ++generation.value
    const [list, staff] = await Promise.all([
      api.POST('/api/ops/list_drafts', { body: {} }),
      api.POST('/api/ops/list_staff_options', { body: {} })
    ])
    if (current !== generation.value || me.value?.staff.id !== staffId) return
    if (staff.data) names.value = Object.fromEntries(staff.data.staff.map(row => [row.id, row.name]))
    if (!list.data) {
      if (!loaded.value) loadError.value = errorText(list.error)
      else if (!options.quiet) toast.add({ title: errorText(list.error), color: 'error' })
      return
    }
    loadError.value = ''
    state.value = { staffId, drafts: list.data.drafts }
  }

  // 换人登录时丢掉上一个人的列表和进行中的请求。
  function reset() {
    generation.value++
    state.value = null
    loadError.value = ''
  }

  function staffName(id: string | null | undefined) {
    if (!id) return ''
    return names.value[id] ?? '已删除员工'
  }

  return { drafts, pending, loaded, loadError, load, reset, staffName }
}
