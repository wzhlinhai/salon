<script setup lang="ts">
import type { components } from '~/utils/api'

// 日结（样稿 14）：一页一天，默认昨天（今天还没营业完，后端也只收昨天及以前）。页头「‹ 日期 ›」换日子。
// 顶上一句结论，下面按收款方式对：系统记的、你点的、差，格子就在表里填；对不上的那行标出来，旁边「写个原因」跳到备注。
// 主按钮「对完了，封账」先保存日结，能封账时接着弹封账确认；不能封（没权限、前面还有日子没封）就只日结。
definePageMeta({ permission: 'closing.daily', title: '日结', art: 'receipt' })

type SystemFigures = components['schemas']['SystemFigures']
type DayCloseDetail = components['schemas']['DayCloseDetail']
type Order = components['schemas']['Order']
type Line = components['schemas']['MethodAmount']

const RECENT = 31
const route = useRoute()
const router = useRouter()
const api = useApi()
const toast = useToast()
const { can } = useMe()
const crumbTitle = useState('page-crumb-title', () => '')
const guard = useRequestGuard()

const today = ref('')
const date = ref('')
const sealedThrough = ref<string | null>(null)
const pendingDays = ref<string[]>([])
const business = ref(false)
const preview = ref<SystemFigures | null>(null)
const detail = ref<DayCloseDetail | null>(null)
const loading = ref(false)
const failed = ref(false)

// 四个现场方式各自对应 close_day 的一个字段。
const FIELDS = { cash: 'cash_counted', unionpay: 'unionpay_actual', wechat: 'wechat_actual', alipay: 'alipay_actual' } as const
type Method = keyof typeof FIELDS
const actual = reactive<Record<Method, string>>({ cash: '', unionpay: '', wechat: '', alipay: '' })
const note = ref('')
const noteBox = useTemplateRef('noteBox')
const MONEY = /^\d{1,9}(\.\d{1,2})?$/

const yesterday = computed(() => today.value ? addDays(today.value, -1) : '')
const sealed = computed(() => !!sealedThrough.value && !!date.value && date.value <= sealedThrough.value)
const saved = computed(() => detail.value?.current ?? null)

function dayTitle(value: string) {
  const [, m, d] = value.split('-')
  return `${Number(m)} 月 ${Number(d)} 日日结`
}

// 页头换日子那里写「11月24日 周一」。
function dayLabel(value: string) {
  const [, m, d] = value.split('-')
  return `${Number(m)}月${Number(d)}日 ${weekdayOf(value)}`
}

async function start() {
  const settings = await api.POST('/api/ops/get_settings', { body: {} })
  if (!settings.data) {
    failed.value = true
    toast.add({ title: errorText(settings.error), color: 'error' })
    return
  }
  today.value = settings.data.business_date
  const asked = typeof route.query.date === 'string' && /^\d{4}-\d{2}-\d{2}$/.test(route.query.date) ? route.query.date : ''
  date.value = asked && asked < today.value ? asked : yesterday.value
}

// 一天的账：最近 31 天谁还没日结（提醒用）、这天日结过没有、系统此刻的数。封了账的日子只看当时存下的快照。
async function load() {
  const day = date.value
  if (!day) return
  const current = guard.next()
  loading.value = true
  failed.value = false
  crumbTitle.value = dayTitle(day)
  // 单子可能因撤单、补录变了，折叠区下次点开重新取。
  ordersGuard.invalidate()
  ordersOpen.value = false
  orders.value = null
  try {
    const [recent, one, live] = await Promise.all([
      api.POST('/api/ops/list_day_closes', { body: { date_from: addDays(today.value, -RECENT), date_to: yesterday.value } }),
      api.POST('/api/ops/list_day_closes', { body: { date_from: day, date_to: day } }),
      api.POST('/api/ops/preview_day_close', { body: { date: day } })
    ])
    if (!current()) return
    if (!recent.data || !one.data || !live.data) {
      failed.value = true
      toast.add({ title: errorText(recent.error ?? one.error ?? live.error), color: 'error' })
      return
    }
    sealedThrough.value = one.data.sealed_through
    const closedRecently = new Set(recent.data.items.map(c => c.business_date))
    pendingDays.value = recent.data.business_days
      .filter(d => !closedRecently.has(d) && !(recent.data.sealed_through && d <= recent.data.sealed_through))
      .sort()
    business.value = one.data.business_days.includes(day)
    preview.value = live.data
    detail.value = null
    if (one.data.items.length) {
      const got = await api.POST('/api/ops/get_day_close', { body: { date: day } })
      if (!current()) return
      if (got.data) detail.value = got.data
      else toast.add({ title: errorText(got.error), color: 'error' })
    }
    fillForm()
  } finally {
    if (current()) loading.value = false
  }
}

// 日结过的填上次存的数；没日结的空着，系统是 0 的方式多半当天没用，先填 0。
function fillForm() {
  const c = saved.value
  note.value = c?.note ?? ''
  for (const m of Object.keys(FIELDS) as Method[]) {
    const line = preview.value?.on_site.find(l => l.method === m)
    const before = c?.differences.find(d => d.method === m)?.actual
    actual[m] = before !== undefined ? String(Number(before)) : !c && line && zeroText(line.amount) ? '0' : ''
  }
}

onMounted(async () => {
  await start()
  void load()
})

// 设置没取到时连今天是哪天都不知道，重试要从头来。
async function retry() {
  if (!date.value) await start()
  void load()
}

onBeforeUnmount(() => {
  crumbTitle.value = ''
})

function go(day: string) {
  if (!day || day === date.value) return
  date.value = day
  void router.replace({ query: day === yesterday.value ? {} : { date: day } })
  // 先清掉上一天的数，新一天没取到之前不显示也不能存。
  preview.value = null
  detail.value = null
  fillForm()
  void load()
}

// 封了账的日子看快照，其余看系统此刻的数（日结后有人补录、撤单，差会跟着变，提醒重新日结）。
const figures = computed(() => sealed.value && saved.value ? saved.value.system : preview.value)
const onSite = computed(() => figures.value?.on_site ?? [])
const nonSite = computed(() => (figures.value?.non_site ?? []).filter(l => !zeroText(l.amount)))

function filled(method: string) {
  const value = sealed.value ? saved.value?.differences.find(d => d.method === method)?.actual : actual[method as Method]
  return value !== undefined && MONEY.test(value.trim()) ? value.trim() : null
}

function difference(line: Line) {
  const value = filled(line.method)
  if (value === null) return null
  return (cents(value) - cents(line.amount)) / 100
}

const systemTotal = computed(() => onSite.value.reduce((s, l) => s + cents(l.amount), 0) / 100)
const allFilled = computed(() => onSite.value.every(l => filled(l.method) !== null))
const actualTotal = computed(() => allFilled.value ? onSite.value.reduce((s, l) => s + cents(filled(l.method)!), 0) / 100 : null)
const totalDifference = computed(() => actualTotal.value === null ? null : Math.round((actualTotal.value - systemTotal.value) * 100) / 100)

function label(line: Line) {
  return line.method === 'cash' ? '现金' : line.label
}

function signedYuan(n: number) {
  return `${n > 0 ? '+' : '−'}${yuan(Math.abs(n))}`
}

// 顶上一句话：这天收了多少、几单；对不上的那项说清应有多少、点出来多少、差多少；对上的合成一句。
const brief = computed(() => {
  const f = figures.value
  if (!f) return []
  const { parts, say, bold } = briefWriter()
  if (!business.value && !f.checkout_count && !systemTotal.value) {
    say('这天没有营业，不用日结。')
  } else {
    say(`这天现场收了 ${yuan(systemTotal.value)}，结账 ${f.checkout_count} 单${f.void_count ? `，撤单 ${f.void_count} 单` : ''}。`)
    const off = onSite.value.filter(l => (difference(l) ?? 0) !== 0)
    const matched = onSite.value.filter(l => difference(l) === 0 && !zeroText(l.amount))
    const blank = onSite.value.filter(l => difference(l) === null)
    off.forEach((l, i) => {
      const d = difference(l)!
      say(`${i ? '；' : ''}${label(l)}应有 ${yuan(l.amount)}，${l.method === 'cash' ? '你点出来' : '实际到账'} ${yuan(filled(l.method)!)}，`)
      bold(`${d > 0 ? '多了' : '少了'} ${yuan(Math.abs(d))}`, 'down')
    })
    if (matched.length) say(`${off.length ? '；' : ''}${matched.map(label).join('和')}跟后台对得上`)
    if (blank.length) say(`${off.length || matched.length ? '；' : ''}${blank.map(label).join('、')}还没填`)
    if (off.length || matched.length || blank.length) say('。')
  }
  if (sealed.value) say('已封账，账不能再改。')
  else if (saved.value && changed.value) say('日结之后账有变动或改了数，记得重新日结。')
  return parts
})

// 和上次存的日结比，有没有要重新存的：没日结过、填的数或备注改了、系统的数变了（补录、撤单，连团购这些不进钱箱的也算：封账时后端比的是整份系统数）。
const changed = computed(() => {
  const c = saved.value
  if (!c) return true
  if ((c.note ?? '') !== note.value.trim()) return true
  if (!sealed.value && JSON.stringify(c.system) !== JSON.stringify(preview.value)) return true
  return c.differences.some(d => filled(d.method) === null || cents(filled(d.method)!) !== cents(d.actual))
})

// 封账只能接着上次封到的那天往后一天；从没封过时，昨天及以前哪天都行。
function canSeal(day: string) {
  if (!can('closing.seal') || !today.value || !day || day >= today.value) return false
  if (!sealedThrough.value) return true
  return day === addDays(sealedThrough.value, 1)
}

const canUnseal = computed(() => can('closing.unseal') && !!sealedThrough.value && date.value === sealedThrough.value)
const nextSeal = computed(() => sealedThrough.value ? addDays(sealedThrough.value, 1) : '')

// 主按钮：要存日结就存，存完能封就弹封账确认。没营业的日子不用日结，只封账。
const needsClose = computed(() => (business.value || !!saved.value) && changed.value)
const primaryLabel = computed(() => {
  if (sealed.value || !figures.value) return ''
  if (needsClose.value) return canSeal(date.value) ? '对完了，封账' : '对完了，日结'
  return canSeal(date.value) ? '封账' : ''
})

const closing = ref(false)
// 取数或存日结的时候不让换日子。
const busy = computed(() => loading.value || closing.value)

async function primary() {
  const day = date.value
  if (needsClose.value && !(await saveClose())) return
  // 存日结时换了日子，就不弹封账了，免得封错天。
  if (date.value === day && canSeal(day)) openSeal()
}

async function saveClose() {
  const missing = onSite.value.find(l => filled(l.method) === null)
  if (missing) {
    toast.add({ title: `填一下${label(missing)}的实际金额，没有就填 0`, color: 'error' })
    return false
  }
  const day = date.value
  closing.value = true
  try {
    const { data, error } = await api.POST('/api/ops/close_day', {
      body: {
        date: day,
        cash_counted: actual.cash.trim(),
        unionpay_actual: actual.unionpay.trim(),
        wechat_actual: actual.wechat.trim(),
        alipay_actual: actual.alipay.trim(),
        note: note.value.trim() || undefined
      }
    })
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return false
    }
    const off = data.differences.filter(d => Number(d.difference) !== 0).length
    toast.add({ title: `已日结（${dayLabel(day)}，第 ${data.version} 版）${off ? `，有 ${off} 项对不上` : '，都对上了'}`, color: 'success' })
    if (date.value !== day) return false
    await load()
    return true
  } finally {
    closing.value = false
  }
}

function writeReason() {
  noteBox.value?.textareaRef?.focus()
}

// 这天的单：点开才取，看每单怎么付的。要开单权限。
const ordersOpen = ref(false)
const orders = ref<Order[] | null>(null)
const ordersTotal = ref(0)
const ordersGuard = useRequestGuard()
const PAY_NAMES: Record<string, string> = { cash: '现金', unionpay: '银联', wechat: '微信', alipay: '支付宝', groupbuy: '团购', debt: '挂账', card: '储值卡', count: '扣次', red_packet: '红包' }

watch(ordersOpen, async (open) => {
  if (!open || orders.value) return
  const current = ordersGuard.next()
  const { data, error } = await api.POST('/api/ops/list_orders', { body: { business_date: date.value, limit: 100 } })
  if (!current()) return
  if (data) {
    orders.value = data.items
    ordersTotal.value = data.total
  } else toast.add({ title: errorText(error), color: 'error' })
})

function clock(value: string) {
  return new Date(value).toLocaleTimeString('zh-CN', { timeZone: 'Asia/Shanghai', hour: '2-digit', minute: '2-digit', hour12: false })
}

function payText(o: Order) {
  if (!o.payments.length) return ''
  return o.payments.map(p => p.method === 'count' ? '扣次' : `${PAY_NAMES[p.method] ?? p.method} ${yuan(p.amount)}`).join(' + ')
}

const history = computed(() => [...(detail.value?.versions ?? [])].sort((a, b) => b.version - a.version))

function offText(differences: components['schemas']['MethodDifference'][]) {
  const off = differences.filter(d => Number(d.difference) !== 0)
  return off.length ? off.map(d => `${d.method === 'cash' ? '现金' : d.label} ${signedYuan(Number(d.difference))}`).join('，') : '都对上了'
}

// 封账确认。
const sealForm = useModalForm()
const sealDate = ref('')

function openSeal() {
  sealForm.begin(() => {
    sealDate.value = date.value
  })
}

function saveSeal() {
  sealForm.save(async (current) => {
    const { error } = await api.POST('/api/ops/seal_day', { body: { date: sealDate.value } })
    if (!current()) return
    if (error) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    sealForm.open.value = false
    toast.add({ title: `封好了，${sealDate.value} 及以前的账不能再改`, color: 'success' })
    await load()
  })
}

// 撤销封账：只撤最后封的那天，要写原因。
const unsealForm = useModalForm()
const unsealReason = ref('')

function openUnseal() {
  unsealForm.begin(() => {
    unsealReason.value = ''
  })
}

function saveUnseal() {
  const reason = unsealReason.value.trim()
  if (!reason) {
    toast.add({ title: '写一下为什么要撤销封账', color: 'error' })
    return
  }
  unsealForm.save(async (current) => {
    const day = sealedThrough.value
    const { error } = await api.POST('/api/ops/unseal_day', { body: { reason } })
    if (!current()) return
    if (error) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    unsealForm.open.value = false
    toast.add({ title: `已撤销 ${day} 的封账`, color: 'success' })
    await load()
  })
}
</script>

<template>
  <div class="space-y-6">
    <PageActions>
      <UButton v-if="primaryLabel" :loading="closing" :disabled="loading" @click="primary">{{ primaryLabel }}</UButton>
    </PageActions>

    <div v-if="date" class="flex flex-wrap items-center gap-2">
      <div class="flex items-center" role="group" aria-label="换一天">
        <UButton color="neutral" variant="ghost" icon="i-lucide-chevron-left" aria-label="前一天" :disabled="busy" @click="go(addDays(date, -1))" />
        <UButton color="neutral" variant="ghost" icon="i-lucide-chevron-right" aria-label="后一天" :disabled="busy || date >= yesterday" @click="go(addDays(date, 1))" />
      </div>
      <h2 class="figure" data-testid="close-date">{{ dayLabel(date) }}</h2>
    </div>

    <LoadError v-if="failed && !figures" title="这天的账没加载出来" hint="检查网络后再试一次" @retry="retry" />
    <ListSkeleton v-else-if="!figures" :rows="5" />

    <template v-else>
      <ReportBrief :parts="brief" data-testid="close-brief" />

      <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-meta text-muted">
        <span data-testid="sealed-through">{{ sealedThrough ? `已封账到 ${sealedThrough}` : '还没有封账' }}</span>
        <button v-if="sealedThrough && sealedThrough !== date && can('closing.unseal')" type="button" class="inline-flex items-center hover:underline" :disabled="busy" @click="go(sealedThrough)">去 {{ sealedThrough.slice(5) }} 撤销封账<UIcon name="i-lucide-chevron-right" class="size-4" aria-hidden="true" /></button>
        <button v-if="nextSeal && nextSeal !== date && canSeal(nextSeal)" type="button" class="inline-flex items-center hover:underline" :disabled="busy" @click="go(nextSeal)">接着封 {{ nextSeal.slice(5) }}<UIcon name="i-lucide-chevron-right" class="size-4" aria-hidden="true" /></button>
        <UButton v-if="canUnseal" size="xs" color="neutral" variant="ghost" @click="openUnseal">撤销封账…</UButton>
        <span v-if="pendingDays.filter(d => d !== date).length" data-testid="pending-days">
          · 还没日结：
          <template v-for="(d, i) in pendingDays.filter(x => x !== date)" :key="d">{{ i ? '、' : '' }}<button type="button" class="hover:underline" :disabled="busy" @click="go(d)">{{ d.slice(5) }}</button></template>
        </span>
      </div>

      <section>
        <h2 class="sec">按收款方式对<span class="font-normal text-dimmed">系统记的 · 你点的 · 差</span></h2>
        <div class="overflow-x-auto">
          <table class="w-full text-sm" data-testid="close-form">
            <thead>
              <tr class="border-b border-default text-sm whitespace-nowrap text-muted">
                <th scope="col" class="px-3 py-2 text-start font-normal">方式</th>
                <th scope="col" class="px-3 py-2 text-end font-normal">系统记的</th>
                <th scope="col" class="px-3 py-2 text-end font-normal">实际</th>
                <th scope="col" class="px-3 py-2 text-end font-normal">差</th>
                <th v-if="!sealed" scope="col" class="w-28 px-3 py-2"><span class="sr-only">说明</span></th>
              </tr>
            </thead>
            <tbody class="divide-y divide-default">
              <tr v-for="line in onSite" :key="line.method" :data-method="line.method" :class="(difference(line) ?? 0) !== 0 ? 'bg-notice' : ''">
                <td class="px-3 py-2 whitespace-nowrap">{{ label(line) }}</td>
                <td class="px-3 py-2 text-end tabular-nums" :class="zeroText(line.amount) ? 'text-dimmed' : ''">{{ yuan(line.amount) }}</td>
                <td class="px-3 py-1">
                  <div class="flex justify-end">
                    <span v-if="sealed" class="tabular-nums">{{ filled(line.method) === null ? '—' : yuan(filled(line.method)) }}</span>
                    <UInput
                      v-else
                      v-model="actual[line.method as Method]"
                      inputmode="decimal"
                      :aria-label="`${line.label}实际`"
                      class="w-28"
                      :ui="{ base: 'text-end tabular-nums' }"
                    />
                  </div>
                </td>
                <td class="px-3 py-2 text-end tabular-nums">
                  <span v-if="!difference(line)" class="text-dimmed">—</span>
                  <span v-else class="text-ink-red">{{ signedYuan(difference(line)!) }}</span>
                </td>
                <td v-if="!sealed" class="px-3 py-2">
                  <span v-if="difference(line) === 0 && !zeroText(line.amount)" class="text-ink-green">✓ 对上</span>
                  <button v-else-if="difference(line)" type="button" class="text-muted whitespace-nowrap hover:underline" @click="writeReason">写个原因</button>
                </td>
              </tr>
              <tr class="font-semibold" data-testid="close-total">
                <td class="px-3 py-2 whitespace-nowrap">现场合计</td>
                <td class="px-3 py-2 text-end tabular-nums">{{ yuan(systemTotal) }}</td>
                <td class="px-3 py-2 text-end tabular-nums">{{ actualTotal === null ? '' : yuan(actualTotal) }}</td>
                <td class="px-3 py-2 text-end tabular-nums" :class="totalDifference ? 'text-ink-red' : 'text-dimmed font-normal'">{{ totalDifference ? signedYuan(totalDifference) : '—' }}</td>
                <td v-if="!sealed" />
              </tr>
              <tr v-for="line in nonSite" :key="line.method" class="text-muted" :data-method="line.method">
                <td class="px-3 py-2">{{ line.label }}<span class="text-muted">（不进钱箱）</span></td>
                <td class="px-3 py-2 text-end tabular-nums">{{ yuan(line.amount) }}</td>
                <td class="px-3 py-2 text-end text-dimmed">—</td>
                <td class="px-3 py-2 text-end text-dimmed">—</td>
                <td v-if="!sealed" />
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="figures.unrecorded.count" class="mt-2 text-meta text-muted">另有 {{ figures.unrecorded.count }} 笔没记收款方式，共 {{ yuan(figures.unrecorded.amount) }}，单独列出不算进上面</p>
      </section>

      <UFormField v-if="!sealed" label="备注" help="对不上的原因，日结时一起存下">
        <UTextarea ref="noteBox" v-model="note" :maxlength="200" :rows="2" class="w-full" placeholder="对不上的原因，例如：找零少了 5 元" />
      </UFormField>
      <p v-else-if="saved?.note" class="text-sm text-muted">备注：{{ saved.note }}</p>

      <details v-if="can('order.create') && (figures.checkout_count || figures.void_count)" :open="ordersOpen" class="border-t border-default pt-3" data-testid="close-orders" @toggle="ordersOpen = ($event.target as HTMLDetailsElement).open">
        <summary class="cursor-pointer text-sm text-muted">这天的 {{ figures.checkout_count }} 单（点开看每单怎么付的）</summary>
        <ListSkeleton v-if="!orders" :rows="3" class="mt-2" />
        <ul v-else class="mt-2 divide-y divide-default text-sm">
          <li v-for="o in orders" :key="o.id" class="flex items-center gap-3 py-2">
            <span class="w-12 text-meta text-muted tabular-nums">{{ clock(o.opened_at) }}</span>
            <span class="min-w-0 flex-1 truncate">{{ o.items.map(i => i.item_name).join('、') || '没有项目' }}</span>
            <span v-if="o.status === 'voided'" class="tag bg-tag-gray">已撤</span>
            <span class="text-muted tabular-nums">{{ payText(o) }}</span>
          </li>
          <li v-if="ordersTotal > orders.length" class="py-2 text-meta text-muted">只列了前 {{ orders.length }} 单，共 {{ ordersTotal }} 单</li>
        </ul>
      </details>

      <details v-if="history.length" class="border-t border-default pt-3" data-testid="close-versions">
        <summary class="cursor-pointer text-sm text-muted">日结记录（{{ history.length }} 版）</summary>
        <ol class="mt-2 space-y-2 text-sm">
          <li v-for="v in history" :key="v.id" class="flex flex-col">
            <span><span class="font-medium">第 {{ v.version }} 版</span><span class="text-muted"> · {{ formatStamp(v.created_at) }}</span></span>
            <span class="text-muted">{{ offText(v.differences) }}</span>
          </li>
        </ol>
      </details>
    </template>

    <UModal v-model:open="sealForm.open.value" :title="`封账到 ${sealDate}`">
      <template #body>
        <div class="space-y-2 text-sm">
          <p>封账后 {{ sealDate }} 及以前的单不能再改、撤单、补录，要退钱只能走售后。</p>
          <p v-if="!sealedThrough" class="text-muted">这是第一次封账，{{ sealDate }} 以前的日子也一起封上。</p>
          <p class="text-muted">封错了老板可以撤销，撤销要写原因。</p>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="sealForm.open.value = false">
          <UButton :loading="sealForm.saving.value" @click="saveSeal">封账</UButton>
        </ModalActions>
      </template>
    </UModal>

    <UModal v-model:open="unsealForm.open.value" :title="`撤销 ${sealedThrough ?? ''} 的封账`" description="撤销后这天的账又能改了，改完记得重新日结、再封账。">
      <template #body>
        <UFormField label="原因">
          <UTextarea v-model="unsealReason" :maxlength="200" :rows="2" class="w-full" placeholder="例如：漏录了一单现金" />
        </UFormField>
      </template>
      <template #footer>
        <ModalActions @cancel="unsealForm.open.value = false">
          <UButton :loading="unsealForm.saving.value" color="error" @click="saveUnseal">撤销封账</UButton>
        </ModalActions>
      </template>
    </UModal>
  </div>
</template>
