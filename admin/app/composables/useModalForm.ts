// 弹窗表单的一次打开拥有独立请求有效期；关闭、重开或卸载后，旧请求不能写回。
export function useModalForm() {
  const guard = useRequestGuard()
  const open = ref(false)
  const loading = ref(false)
  const saving = ref(false)

  function invalidate() {
    guard.invalidate()
    loading.value = false
    saving.value = false
  }

  watch(open, (value) => {
    if (!value) invalidate()
  }, { flush: 'sync' })

  function begin(reset: () => void) {
    invalidate()
    reset()
    open.value = true
  }

  async function load(run: (current: () => boolean) => Promise<void>) {
    const current = guard.hold()
    loading.value = true
    try {
      await run(current)
    } finally {
      if (current()) loading.value = false
    }
  }

  async function save(run: (current: () => boolean) => Promise<void>) {
    if (!open.value || loading.value || saving.value) return
    const current = guard.hold()
    saving.value = true
    try {
      await run(current)
    } finally {
      if (current()) saving.value = false
    }
  }

  return { open, loading, saving, begin, load, save }
}
