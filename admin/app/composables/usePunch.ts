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

  const clockIn = computed(() => {
    const staffId = me.value?.staff.id
    return staffId && state.value?.staffId === staffId ? state.value.at : null
  })

  // 读到过本人今天的打卡（打了或没打）：没读到之前，首页不出「今天还没打卡」，免得打过卡的人进首页先闪一下黄条。
  const known = computed(() => !!me.value && state.value?.staffId === me.value.staff.id)

  function remember(at: string | null) {
    const staffId = me.value?.staff.id
    if (staffId) state.value = { staffId, at }
  }

  // 打卡中的标记全站共用，不能跟着发起的组件走：请求没回来就切页，组件卸载了也要复位、也要记下打卡，
  // 不然别处一直按不了，回到首页还显示「今天还没打卡」，再点一次就被算成下班。只在换了人登录时丢掉结果。
  async function punch() {
    if (punching.value || !can('attendance.clock')) return
    const staffId = me.value?.staff.id
    punching.value = true
    try {
      const { data, error } = await api.POST('/api/ops/clock_in', { body: {} })
      if (me.value?.staff.id !== staffId) return
      if (!data) {
        toast.add({ title: errorText(error), color: 'error' })
        return
      }
      if (!clockIn.value) remember(data.at)
      toast.add({ title: `已打卡 ${clockTime(data.at)}`, color: 'success' })
    } finally {
      punching.value = false
    }
  }

  return { clockIn, known, punching, punch, remember }
}

export function clockTime(value: string | null) {
  if (!value) return '—'
  return storeTime(value)
}
