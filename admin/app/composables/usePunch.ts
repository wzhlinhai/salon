// 本人今天的上班打卡：首页按钮和个人菜单共用一份状态，任一处打卡后另一处立即知道，避免重复打卡被算成下班。
// 状态按员工记，换人登录后不会读到上一个人的打卡。
interface PunchState {
  staffId: string
  at: string | null
}

export function usePunch() {
  const api = useApi()
  const toast = useToast()
  const { me, can } = useMe()
  const state = useState<PunchState | null>('my-punch-today', () => null)
  const punching = useState<boolean>('my-punch-saving', () => false)
  const guard = useRequestGuard()

  const clockIn = computed(() => {
    const staffId = me.value?.staff.id
    return staffId && state.value?.staffId === staffId ? state.value.at : null
  })

  function remember(at: string | null) {
    const staffId = me.value?.staff.id
    if (staffId) state.value = { staffId, at }
  }

  async function punch() {
    if (punching.value || !can('attendance.clock')) return
    const current = guard.next()
    punching.value = true
    try {
      const { data, error } = await api.POST('/api/ops/clock_in', { body: {} })
      if (!current()) return
      if (!data) {
        toast.add({ title: errorText(error), color: 'error' })
        return
      }
      if (!clockIn.value) remember(data.at)
      toast.add({ title: `已打卡 ${clockTime(data.at)}`, color: 'success' })
    } finally {
      if (current()) punching.value = false
    }
  }

  return { clockIn, punching, punch, remember }
}

export function clockTime(value: string | null) {
  if (!value) return '—'
  return new Date(value).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })
}
