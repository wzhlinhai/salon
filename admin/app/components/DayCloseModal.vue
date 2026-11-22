<script setup lang="ts">
import type { components } from '~/utils/api'

// 日结一个营业日：先看系统应收，再填现金清点数和银联、微信、支付宝的实际到账，差异当场算出来。
// 保存后弹窗换成完成页（DESIGN「完成页」）：一句话、几行差异摘要，可以接着封账。
type SystemFigures = components['schemas']['SystemFigures']
type DayClose = components['schemas']['DayClose']
type Previous = { cash_counted: string, unionpay_actual: string, wechat_actual: string, alipay_actual: string, note: string | null }

const props = defineProps<{ canSeal: (date: string) => boolean }>()
const emit = defineEmits<{ closed: [close: DayClose], seal: [date: string] }>()

const api = useApi()
const toast = useToast()
const form = useModalForm()
const date = ref('')
const system = ref<SystemFigures | null>(null)
const done = ref<DayClose | null>(null)
// 四个现场方式各自对应 close_day 的一个字段。
const FIELDS = { cash: 'cash_counted', unionpay: 'unionpay_actual', wechat: 'wechat_actual', alipay: 'alipay_actual' } as const
type Method = keyof typeof FIELDS
const actual = reactive<Record<Method, string>>({ cash: '', unionpay: '', wechat: '', alipay: '' })
const note = ref('')
const failed = ref(false)

function start(day: string, previous?: Previous) {
  form.begin(() => {
    date.value = day
    system.value = null
    failed.value = false
    done.value = null
    note.value = previous?.note ?? ''
    for (const m of Object.keys(FIELDS) as Method[]) actual[m] = previous ? previous[FIELDS[m]] : ''
  })
  loadPreview()
}

function loadPreview() {
  const day = date.value
  failed.value = false
  form.load(async (current) => {
    const { data, error } = await api.POST('/api/ops/preview_day_close', { body: { date: day } })
    if (!current()) return
    if (!data) {
      failed.value = true
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    system.value = data
    // 系统是 0 的方式多半当天没用，先填 0，只留有数的让人去核。
    for (const line of data.on_site) {
      const m = line.method as Method
      if (m in FIELDS && actual[m] === '' && Number(line.amount) === 0) actual[m] = '0'
    }
  })
}

defineExpose({ start })

const MONEY = /^\d{1,9}(\.\d{1,2})?$/

function difference(line: { method: string, amount: string }) {
  const value = actual[line.method as Method]
  if (value === undefined || !MONEY.test(value.trim())) return null
  return Math.round((Number(value) - Number(line.amount)) * 100) / 100
}

// 完成页的合计用保存下来的快照：弹窗开着时别人补录或撤单，差异和合计都以保存那一刻为准。
const systemTotal = computed(() => (done.value?.system.on_site ?? []).reduce((sum, l) => sum + Number(l.amount), 0))

function save() {
  const figures = system.value
  if (!figures) return
  const missing = figures.on_site.find(l => !MONEY.test((actual[l.method as Method] ?? '').trim()))
  if (missing) {
    toast.add({ title: `填一下${missing.label}的实际金额，没有就填 0`, color: 'error' })
    return
  }
  form.save(async (current) => {
    const body = {
      date: date.value,
      cash_counted: actual.cash.trim(),
      unionpay_actual: actual.unionpay.trim(),
      wechat_actual: actual.wechat.trim(),
      alipay_actual: actual.alipay.trim(),
      note: note.value.trim() || undefined
    }
    const { data, error } = await api.POST('/api/ops/close_day', { body })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    done.value = data
    emit('closed', data)
  })
}

const offLines = computed(() => (done.value?.differences ?? []).filter(d => Number(d.difference) !== 0))

function signed(value: string | number) {
  const n = Number(value)
  return `${n > 0 ? '多' : '少'} ${yuanFixed(Math.abs(n))}`
}

function sealNow() {
  form.open.value = false
  emit('seal', date.value)
}
</script>

<template>
  <UModal
    v-model:open="form.open.value"
    :title="done ? `${date} 已日结` : `日结 ${date} ${date ? weekdayOf(date) : ''}`"
    :description="done ? '快照已保存，之后账目有变可以重新日结。' : '对着系统应收，填实际清点和到账的金额。差异是实际减系统。'"
    :ui="{ content: 'sm:max-w-xl' }"
  >
    <template #body>
      <ListSkeleton v-if="form.loading.value" :rows="4" />

      <div v-else-if="done" class="flex flex-col items-center gap-4 py-2 text-center" data-testid="close-done">
        <UIcon name="i-lucide-circle-check" class="size-12 text-muted" aria-hidden="true" />
        <p class="text-lead font-semibold">{{ offLines.length ? `有 ${offLines.length} 项对不上` : '都对上了' }}</p>
        <ul class="w-full divide-y divide-default border-y border-default text-start">
          <li v-for="d in done.differences" :key="d.method" class="flex items-center justify-between px-1 py-2">
            <span>{{ d.label }}</span>
            <span class="tabular-nums" :class="Number(d.difference) === 0 ? 'text-dimmed' : 'font-medium text-ink-red'">
              {{ Number(d.difference) === 0 ? '对上了' : signed(d.difference) }}
            </span>
          </li>
        </ul>
        <p class="text-meta text-muted">第 {{ done.version }} 版 · 系统应收 {{ yuanFixed(systemTotal) }}</p>
      </div>

      <EmptyState v-else-if="failed" title="系统应收没加载出来" hint="检查网络后再试一次">
        <UButton color="neutral" variant="outline" size="sm" @click="loadPreview">重试</UButton>
      </EmptyState>

      <template v-else-if="system">
        <table class="w-full text-sm" data-testid="close-form">
          <thead>
            <tr class="border-b border-default text-meta text-muted">
              <th class="py-2 text-start font-normal">方式</th>
              <th class="py-2 text-end font-normal">系统应收</th>
              <th class="py-2 ps-4 text-end font-normal">实际</th>
              <th class="w-28 py-2 text-end font-normal">差异</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-default">
            <tr v-for="line in system.on_site" :key="line.method" :data-method="line.method">
              <td class="py-2">{{ line.method === 'cash' ? '现金清点' : line.label }}</td>
              <td class="py-2 text-end tabular-nums" :class="zeroText(line.amount) ? 'text-dimmed' : ''">{{ yuanFixed(line.amount) }}</td>
              <td class="py-2 ps-4">
                <div class="flex justify-end">
                  <UInput
                    v-model="actual[line.method as Method]"
                    inputmode="decimal"
                    :aria-label="`${line.label}实际`"
                    class="w-32"
                    :ui="{ base: 'text-end tabular-nums' }"
                  />
                </div>
              </td>
              <td class="py-2 text-end tabular-nums">
                <span v-if="difference(line) === null" class="text-dimmed" />
                <span v-else-if="difference(line) === 0" class="text-dimmed">对上了</span>
                <span v-else class="font-medium text-ink-red">{{ signed(difference(line)!) }}</span>
              </td>
            </tr>
          </tbody>
        </table>

        <div class="mt-4 space-y-1 text-meta text-muted">
          <p>
            客次 {{ Number(system.guest_visits) }} · 结账 {{ system.checkout_count }} 单<template v-if="system.void_count"> · 撤单 {{ system.void_count }} 单</template>
          </p>
          <p v-if="system.non_site.some(l => !zeroText(l.amount))">
            不进现场的：<template v-for="(l, i) in system.non_site.filter(x => !zeroText(x.amount))" :key="l.method">{{ i ? '，' : '' }}{{ l.label }} {{ yuanFixed(l.amount) }}</template>
          </p>
          <p v-if="system.unrecorded.count">未记方式 {{ system.unrecorded.count }} 笔，共 {{ yuanFixed(system.unrecorded.amount) }}，单独列出不回填</p>
        </div>

        <UFormField label="备注" class="mt-4">
          <UTextarea v-model="note" :maxlength="200" :rows="2" class="w-full" placeholder="对不上的原因，例如：找零少了 5 元" />
        </UFormField>
      </template>
    </template>

    <template #footer>
      <div v-if="done" class="flex w-full justify-end gap-2">
        <UButton v-if="props.canSeal(date)" color="neutral" variant="outline" icon="i-lucide-lock" @click="sealNow">封账到这天…</UButton>
        <UButton @click="form.open.value = false">完成</UButton>
      </div>
      <div v-else class="flex w-full justify-end gap-2">
        <UButton color="neutral" variant="ghost" @click="form.open.value = false">取消</UButton>
        <UButton :loading="form.saving.value" :disabled="form.loading.value || !system" @click="save">保存日结</UButton>
      </div>
    </template>
  </UModal>
</template>
