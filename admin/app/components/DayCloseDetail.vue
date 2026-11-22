<script setup lang="ts">
import type { components } from '~/utils/api'

// 某个营业日的日结详情：日结过的看当前快照、差异和历次版本；没日结的看系统此刻的应收。
type DayCloseDetail = components['schemas']['DayCloseDetail']
type SystemFigures = components['schemas']['SystemFigures']

const props = defineProps<{ date: string, closed: boolean }>()

const api = useApi()
const toast = useToast()
const guard = useRequestGuard()
const loading = ref(false)
const detail = ref<DayCloseDetail | null>(null)
const preview = ref<SystemFigures | null>(null)
const failed = ref(false)

async function reload() {
  const current = guard.next()
  loading.value = true
  failed.value = false
  try {
    if (props.closed) {
      const { data, error } = await api.POST('/api/ops/get_day_close', { body: { date: props.date } })
      if (!current()) return
      if (data) detail.value = data
      else fail(error)
    } else {
      const { data, error } = await api.POST('/api/ops/preview_day_close', { body: { date: props.date } })
      if (!current()) return
      if (data) preview.value = data
      else fail(error)
    }
  } finally {
    if (current()) loading.value = false
  }
}

function fail(error: unknown) {
  failed.value = true
  toast.add({ title: errorText(error), color: 'error' })
}

onMounted(reload)

const figures = computed(() => detail.value?.current.system ?? preview.value)
const nonSite = computed(() => (figures.value?.non_site ?? []).filter(l => !zeroText(l.amount)))
const history = computed(() => [...(detail.value?.versions ?? [])].sort((a, b) => b.version - a.version))

function signed(value: string) {
  const n = Number(value)
  return `${n > 0 ? '多' : '少'} ${yuanFixed(Math.abs(n))}`
}

function offText(differences: components['schemas']['MethodDifference'][]) {
  const off = differences.filter(d => Number(d.difference) !== 0)
  return off.length ? off.map(d => `${d.label} ${signed(d.difference)}`).join('，') : '都对上了'
}
</script>

<template>
  <ListSkeleton v-if="loading && !figures" :rows="5" />
  <EmptyState v-else-if="failed && !figures" title="这天的账没加载出来" hint="检查网络后再试一次">
    <UButton color="neutral" variant="outline" size="sm" @click="reload">重试</UButton>
  </EmptyState>
  <div v-else-if="figures" class="space-y-6 text-sm">
    <section v-if="detail" class="space-y-2" data-testid="close-differences">
      <div class="sec">实际与系统</div>
      <table class="w-full">
        <thead>
          <tr class="border-b border-default text-meta text-muted">
            <th class="py-1.5 text-start font-normal">方式</th>
            <th class="py-1.5 text-end font-normal">系统</th>
            <th class="py-1.5 text-end font-normal">实际</th>
            <th class="py-1.5 text-end font-normal">差异</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-default">
          <tr v-for="d in detail.current.differences" :key="d.method">
            <td class="py-1.5">{{ d.method === 'cash' ? '现金清点' : d.label }}</td>
            <td class="py-1.5 text-end tabular-nums" :class="zeroText(d.system) ? 'text-dimmed' : ''">{{ yuanFixed(d.system) }}</td>
            <td class="py-1.5 text-end tabular-nums" :class="zeroText(d.actual) ? 'text-dimmed' : ''">{{ yuanFixed(d.actual) }}</td>
            <td class="py-1.5 text-end tabular-nums">
              <span v-if="Number(d.difference) === 0" class="text-dimmed">对上了</span>
              <span v-else class="font-medium text-ink-red">{{ signed(d.difference) }}</span>
            </td>
          </tr>
        </tbody>
      </table>
      <p v-if="detail.current.note" class="text-muted">备注：{{ detail.current.note }}</p>
    </section>

    <section v-else class="space-y-2" data-testid="close-preview">
      <div class="sec">系统应收</div>
      <ul class="divide-y divide-default border-y border-default">
        <li v-for="l in figures.on_site" :key="l.method" class="flex justify-between py-1.5">
          <span>{{ l.label }}</span>
          <span class="tabular-nums" :class="zeroText(l.amount) ? 'text-dimmed' : ''">{{ yuanFixed(l.amount) }}</span>
        </li>
      </ul>
      <p class="text-meta text-muted">还没日结，这是系统此刻的数，之后补录或撤单会变。</p>
    </section>

    <section class="space-y-1 text-muted">
      <div class="sec text-default">当天</div>
      <p>客次 {{ Number(figures.guest_visits) }} · 结账 {{ figures.checkout_count }} 单<template v-if="figures.void_count"> · 撤单 {{ figures.void_count }} 单</template></p>
      <p v-if="nonSite.length">不进现场的：<template v-for="(l, i) in nonSite" :key="l.method">{{ i ? '，' : '' }}{{ l.label }} {{ yuanFixed(l.amount) }}</template></p>
      <p v-if="figures.unrecorded.count">未记方式 {{ figures.unrecorded.count }} 笔，共 {{ yuanFixed(figures.unrecorded.amount) }}</p>
    </section>

    <section v-if="history.length" class="space-y-2" data-testid="close-versions">
      <div class="sec">日结记录</div>
      <ol class="space-y-2">
        <li v-for="v in history" :key="v.id" class="flex flex-col">
          <span><span class="font-medium">第 {{ v.version }} 版</span><span class="text-dimmed"> · {{ formatStamp(v.created_at) }}</span></span>
          <span class="text-muted">{{ offText(v.differences) }}</span>
        </li>
      </ol>
    </section>
  </div>
</template>
