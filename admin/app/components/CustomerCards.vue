<script setup lang="ts">
import type { components } from '~/utils/api'

const props = defineProps<{ id: string, merged: boolean }>()
// 卡变了（开卡、充值、调整、升级、转余额、退卡）：详情顶部的卡余额小卡取自列表行，要让列表重读
const emit = defineEmits<{ changed: [] }>()
const api = useApi()
const toast = useToast()
const { can } = useMe()

type CustomerCard = components['schemas']['Card']
type Payment = components['schemas']['Payment']
const cardCards = ref<CustomerCard[]>([])
const cardLoading = ref(false)
const cardListGuard = useRequestGuard()

async function cardLoad() {
  const id = props.id
  const current = cardListGuard.next()
  cardLoading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/list_customer_cards', { body: { customer_id: id } })
    if (!current()) return
    if (data) cardCards.value = data.items
    else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current()) cardLoading.value = false
  }
}

const cardTypes = ref<components['schemas']['CardType'][]>([])
const cardStaff = ref<components['schemas']['StaffOption'][]>([])
const cardForm = useModalForm()
const { open: cardModal, loading: cardOptionsLoading, saving: cardSaving } = cardForm
const cardTarget = ref<CustomerCard | null>(null)
const cardCardType = ref('')
const openingType = computed(() => cardTypes.value.find(t => t.id === cardCardType.value))
const cardPrincipal = ref('0.00')
const cardBonus = ref('0.00')
const cardSales = ref<string[]>([])
const cardPayments = ref<Payment[]>([])
const cardPaymentsTouched = ref(false)
const openingExpected = computed(() => {
  if (cardTarget.value || !openingType.value || openingType.value.kind === 'stored_value') return cardPrincipal.value
  const counts = (openingType.value.items ?? []).reduce((sum, item) => sum + Number(item.unit_price || 0) * Number(item.paid_count || 0), 0)
  return (Number(cardPrincipal.value || 0) + counts).toFixed(2)
})

async function cardEdit(card: CustomerCard | null) {
  cardForm.begin(() => {
    cardTarget.value = card
    cardPrincipal.value = '0.00'
    cardBonus.value = '0.00'
    cardSales.value = []
    cardPayments.value = []
    cardPaymentsTouched.value = false
    cardCardType.value = ''
    cardTypes.value = []
    cardStaff.value = []
  })
  await cardForm.load(async (current) => {
    const [t, s] = await Promise.all([
      api.POST('/api/ops/list_card_types', { body: {} }),
      api.POST('/api/ops/list_staff_options', { body: {} })
    ])
    if (!current()) return
    if (t.data && s.data) {
      cardTypes.value = t.data.items.filter(t => t.active)
      cardStaff.value = s.data.staff.filter(s => s.active)
    } else toast.add({ title: errorText(t.error ?? s.error), color: 'error' })
  })
}

async function cardSave() {
  const id = props.id
  const isRecharge = !!cardTarget.value
  const countsOnly = !cardTarget.value && openingType.value?.kind === 'count'
  const body = { principal: countsOnly ? '0.00' : cardPrincipal.value, bonus: countsOnly ? '0.00' : cardBonus.value, sales_staff_ids: [...cardSales.value], payments: cardPayments.value }
  await cardForm.save(async (current) => {
    const { data, error } = cardTarget.value
      ? await api.POST('/api/ops/recharge_card', { body: { ...body, card_id: cardTarget.value.id } })
      : await api.POST('/api/ops/open_card', { body: { ...body, customer_id: id, card_type_id: cardCardType.value } })
    if (!current()) {
      if (data) {
        toast.add({ title: isRecharge ? '充值成功' : '开卡成功', color: 'success' })
        await refreshCards()
      }
      return
    }
    if (data) {
      const upgradedTo = (data as components['schemas']['RechargeResult']).upgraded_to
      if (upgradedTo) toast.add({ title: `充值成功，已自动升级为 ${upgradedTo.name}`, color: 'success' })
      else toast.add({ title: cardTarget.value ? '充值成功' : '开卡成功', color: 'success' })
      cardModal.value = false
      await refreshCards()
    } else toast.add({ title: errorText(error), color: 'error' })
  })
}

const pointsCard = ref('')
const cardPoints = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_card_points', { body: { card_id: pointsCard.value, before_id: beforeId } })
  return { data: data?.entries, error }
})

function showPoints(id: string) {
  pointsCard.value = id
  cardPoints.reload()
}

const cardLedgerCard = ref('')
const cardLedger = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_card_ledger', { body: { card_id: cardLedgerCard.value, before_id: beforeId } })
  return { data: data?.entries, error }
})

function cardShowLedger(id: string) {
  cardLedgerCard.value = id
  cardLedger.reload()
}

const countCard = ref('')
const cardCounts = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_card_counts', { body: { card_id: countCard.value, before_id: beforeId } })
  return { data: data?.entries, error }
})

function showCounts(id: string) {
  countCard.value = id
  cardCounts.reload()
}

const adjustmentCard = ref('')
const adjustments = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_card_adjustments', { body: { card_id: adjustmentCard.value, before_id: beforeId } })
  return { data: data?.entries, error }
})

function showAdjustments(id: string) {
  adjustmentCard.value = id
  adjustments.reload()
}

const adjustForm = useModalForm()
const { open: adjustModal, loading: adjustLoading, saving: adjustSaving } = adjustForm
const adjustTarget = ref<CustomerCard | null>(null)
const adjustPoints = ref(0)
const adjustPrincipal = ref('')
const adjustBonus = ref('')
const adjustExpires = ref('')
const adjustUnlimited = ref(false)
const adjustType = ref('')
const adjustReason = ref('')
const adjustCounts = ref<(components['schemas']['CardCountTarget'] & { name: string })[]>([])
const adjustTypes = ref<components['schemas']['CardType'][]>([])

async function editAdjustment(card: CustomerCard) {
  adjustForm.begin(() => {
    adjustTarget.value = card
    adjustCounts.value = card.items.map(x => ({ item_id: x.item_id, paid: x.paid_remaining, gift: x.gift_remaining, name: x.item_name }))
    adjustPoints.value = card.points
    adjustPrincipal.value = card.principal
    adjustBonus.value = card.bonus
    adjustExpires.value = card.expires_on || ''
    adjustUnlimited.value = !card.expires_on
    adjustType.value = card.card_type_id
    adjustReason.value = ''
    adjustTypes.value = []
  })
  await adjustForm.load(async (current) => {
    const { data, error } = await api.POST('/api/ops/list_card_types', { body: {} })
    if (!current()) return
    if (data) {
      const kind = data.items.find(t => t.id === card.card_type_id)?.kind
      adjustTypes.value = data.items.filter(t => t.id === card.card_type_id || (t.active && t.kind === kind))
    } else toast.add({ title: errorText(error), color: 'error' })
  })
}

async function saveAdjustment() {
  const card = adjustTarget.value
  if (!card) return
  const body: components['schemas']['AdjustCardInput'] = { card_id: card.id, reason: adjustReason.value.trim() }
  const changedCounts = adjustCounts.value.filter((x) => {
    const old = card.items.find(i => i.item_id === x.item_id)!
    return Number(x.paid) !== old.paid_remaining || Number(x.gift) !== old.gift_remaining
  }).map(x => ({ item_id: x.item_id, paid: Number(x.paid), gift: Number(x.gift) }))
  if (changedCounts.length) body.counts = changedCounts
  if (Number(adjustPoints.value) !== card.points) body.points = Number(adjustPoints.value)
  if (adjustPrincipal.value !== card.principal) body.principal = adjustPrincipal.value
  if (adjustBonus.value !== card.bonus) body.bonus = adjustBonus.value
  const expires = adjustUnlimited.value ? null : adjustExpires.value
  if (expires !== (card.expires_on || null)) body.expiry = { expires_on: expires }
  if (adjustType.value !== card.card_type_id) body.card_type_id = adjustType.value
  await adjustForm.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/adjust_card', { body })
    if (!current()) {
      if (data) await refreshCards()
      return
    }
    if (data) {
      adjustModal.value = false
      await refreshCards()
    } else toast.add({ title: errorText(error), color: 'error' })
  })
}

// 升级与转余额
const upgradeCardID = ref('')
const upgrades = useCursorList(async (beforeId) => {
  const { data, error } = await api.POST('/api/ops/list_card_upgrades', { body: { card_id: upgradeCardID.value, before_id: beforeId } })
  return { data: data?.entries, error }
})

function showUpgrades(id: string) {
  upgradeCardID.value = id
  upgrades.reload()
}

const balanceForm = useModalForm()
const { open: balanceOpen, loading: balanceLoading, saving: balanceSaving } = balanceForm
const balanceMode = ref<'upgrade' | 'transfer'>('upgrade')
const balanceCard = ref<CustomerCard | null>(null)
const balanceTo = ref('')
const balancePrincipal = ref('0.00')
const balanceBonus = ref('0.00')
const balanceSales = ref<string[]>([])
const balancePayments = ref<Payment[]>([])
const balancePaymentsTouched = ref(false)
const balanceReason = ref('')
const balanceTypes = ref<components['schemas']['CardType'][]>([])
const balanceStaff = ref<components['schemas']['StaffOption'][]>([])
const hasDifference = computed(() => Number(balancePrincipal.value) > 0 || Number(balanceBonus.value) > 0)
const transferTargets = computed(() => cardCards.value.filter(c => c.status === 'active' && c.id !== balanceCard.value?.id && c.kind !== 'count'))

async function editBalance(card: CustomerCard, mode: 'upgrade' | 'transfer') {
  balanceForm.begin(() => {
    balanceCard.value = card
    balanceMode.value = mode
    balanceTo.value = ''
    balancePrincipal.value = '0.00'
    balanceBonus.value = '0.00'
    balanceSales.value = []
    balancePayments.value = []
    balancePaymentsTouched.value = false
    balanceReason.value = ''
    balanceTypes.value = []
    balanceStaff.value = []
  })
  if (mode === 'transfer') return
  await balanceForm.load(async (current) => {
    const [types, staff] = await Promise.all([
      api.POST('/api/ops/list_card_types', { body: {} }),
      api.POST('/api/ops/list_staff_options', { body: {} })
    ])
    if (!current()) return
    if (types.data && staff.data) {
      balanceTypes.value = types.data.items.filter(t => t.active && t.kind === 'stored_value' && t.id !== card.card_type_id)
      balanceStaff.value = staff.data.staff.filter(s => s.active)
    } else toast.add({ title: errorText(types.error ?? staff.error), color: 'error' })
  })
}

async function saveBalance() {
  const card = balanceCard.value
  if (!card) return
  const amounts = { principal: balancePrincipal.value, bonus: balanceBonus.value }
  await balanceForm.save(async (current) => {
    const { data, error } = balanceMode.value === 'upgrade'
      ? await api.POST('/api/ops/upgrade_card', { body: { ...amounts, card_id: card.id, to_type_id: balanceTo.value, sales_staff_ids: hasDifference.value ? [...balanceSales.value] : [], payments: balancePayments.value } })
      : await api.POST('/api/ops/transfer_card_balance', { body: { ...amounts, from_card_id: card.id, to_card_id: balanceTo.value, reason: balanceReason.value.trim() } })
    if (!current()) {
      if (data) await refreshCards()
      return
    }
    if (data) {
      balanceOpen.value = false
      await refreshCards()
    } else toast.add({ title: errorText(error), color: 'error' })
  })
}

const refundForm = useModalForm()
const { open: refundOpen, loading: refundLoading, saving: refundSaving } = refundForm
const refundQuote = ref<components['schemas']['CardRefundQuote'] | null>(null)
const refundMethod = ref<components['schemas']['RefundCardInput']['method']>()
const refundError = ref('')
const refundMethods: { label: string, value: NonNullable<typeof refundMethod.value> }[] = [{ label: '现金', value: 'cash' }, { label: '银联', value: 'unionpay' }, { label: '微信', value: 'wechat' }, { label: '支付宝', value: 'alipay' }]

async function editRefund(card: CustomerCard) {
  refundForm.begin(() => {
    refundQuote.value = null
    refundMethod.value = undefined
    refundError.value = ''
  })
  await refundForm.load(async (current) => {
    const { data, error } = await api.POST('/api/ops/preview_card_refund', { body: { card_id: card.id } })
    if (!current()) return
    if (data) refundQuote.value = data
    else refundError.value = errorText(error)
  })
}

async function saveRefund() {
  const quote = refundQuote.value
  const method = refundMethod.value
  if (!quote || !method) return
  await refundForm.save(async (current) => {
    const { data, error, response } = await api.POST('/api/ops/refund_card', { body: { card_id: quote.card_id, amount: quote.amount, method } })
    if (!current()) {
      if (data) await refreshCards()
      return
    }
    if (data) {
      refundOpen.value = false
      await refreshCards()
    } else {
      refundError.value = errorText(error)
      if (response.status === 409) {
        refundQuote.value = null
        await refreshCards()
      }
    }
  })
}

const ledgerKinds: Record<string, string> = { open: '开卡', recharge: '充值', adjust: '调整', upgrade: '升级', transfer_out: '转出', transfer_in: '转入', consume: '消费', void: '撤单退回', refund: '退卡' }

// 所有写操作刷新同一组已展开的明细，避免某个弹窗漏掉积分或升级记录。
async function refreshCards() {
  emit('changed')
  await Promise.all([
    cardLoad(),
    ...(pointsCard.value ? [cardPoints.reload()] : []),
    ...(cardLedgerCard.value ? [cardLedger.reload()] : []),
    ...(countCard.value ? [cardCounts.reload()] : []),
    ...(adjustmentCard.value ? [adjustments.reload()] : []),
    ...(upgradeCardID.value ? [upgrades.reload()] : [])
  ])
}

// 卡的操作（DESIGN.md 行内操作）：充值常显，其余收进 ··· 菜单，分成改卡、看记录、退卡三组。
function canRecharge(card: CustomerCard) {
  return card.status === 'active' && card.kind !== 'count' && can('card.sell') && !props.merged
}

function cardMenu(card: CustomerCard) {
  const active = card.status === 'active'
  const change = [
    ...(active && can('card.adjust') ? [{ label: '调整', icon: 'i-lucide-sliders-horizontal', onSelect: () => editAdjustment(card) }] : []),
    ...(active && can('card.sell') && card.kind === 'stored_value' ? [{ label: '升级', icon: 'i-lucide-arrow-up-circle', onSelect: () => editBalance(card, 'upgrade') }] : []),
    ...(active && can('card.adjust') ? [{ label: '转余额', icon: 'i-lucide-arrow-right-left', onSelect: () => editBalance(card, 'transfer') }] : [])
  ]
  const records = [
    { label: '流水', icon: 'i-lucide-receipt-text', onSelect: () => cardShowLedger(card.id) },
    { label: '积分流水', icon: 'i-lucide-star', onSelect: () => showPoints(card.id) },
    ...(card.items.length ? [{ label: '次数流水', icon: 'i-lucide-list-ordered', onSelect: () => showCounts(card.id) }] : []),
    { label: '升级记录', icon: 'i-lucide-history', onSelect: () => showUpgrades(card.id) },
    { label: '调整记录', icon: 'i-lucide-file-clock', onSelect: () => showAdjustments(card.id) }
  ]
  const refund = active && can('card.adjust') ? [{ label: '退卡', icon: 'i-lucide-undo-2', color: 'error' as const, onSelect: () => editRefund(card) }] : []
  return [change, records, refund].filter(group => group.length)
}

onMounted(cardLoad)
</script>

<template>
  <div>
    <section>
      <div class="sec mt-0! min-h-8">
        <span v-if="cardCards.length" class="font-normal text-dimmed">{{ cardCards.length }} 张</span>
        <UButton v-if="can('card.sell') && !props.merged" size="sm" color="neutral" variant="ghost" icon="i-lucide-plus" class="ms-auto" @click="cardEdit(null)">开卡</UButton>
      </div>
      <div class="box overflow-hidden p-0">
        <p v-if="cardLoading" class="px-3 py-2 text-sm text-muted">正在读取卡…</p>
        <p v-else-if="!cardCards.length" class="px-3 py-2 text-sm text-muted">暂无卡</p>
        <div v-for="card in cardCards" :key="card.id" :data-card="card.id" class="space-y-2 border-t border-default px-3 py-2 text-sm first:border-t-0" :class="card.status === 'refunded' ? 'text-muted' : ''">
          <div class="flex items-start gap-2">
            <p class="flex min-w-0 flex-1 flex-wrap items-center gap-x-3 gap-y-1">
              <span class="font-medium">{{ card.card_type_name }}</span>
              <span v-if="card.status === 'refunded'" class="tag bg-tag-gray">已退</span>
              <span class="tabular-nums"><span class="text-muted">本金</span> {{ yuanFixed(card.principal) }}</span>
              <span class="tabular-nums"><span class="text-muted">赠送金</span> {{ yuanFixed(card.bonus) }}</span>
              <span class="tabular-nums"><span class="text-muted">积分</span> {{ card.points }}</span>
              <span class="ms-auto text-meta text-muted">有效期 {{ card.expires_on || '不限' }}</span>
            </p>
            <div class="-my-1 flex shrink-0 items-center gap-1">
              <UButton v-if="canRecharge(card)" size="sm" color="neutral" variant="outline" @click="cardEdit(card)">充值</UButton>
              <UDropdownMenu :items="cardMenu(card)" :content="{ align: 'end' }">
                <UButton size="sm" color="neutral" variant="ghost" icon="i-lucide-ellipsis" :aria-label="`${card.card_type_name} 的操作`" />
              </UDropdownMenu>
            </div>
          </div>
          <div v-if="card.items.length" class="rounded-md bg-elevated px-2 py-2 text-meta">
            <p v-for="item in card.items" :key="item.item_id" data-testid="remaining-counts" class="tabular-nums">{{ item.item_name }} · 购买 {{ item.paid_remaining }} · 赠送 {{ item.gift_remaining }}</p>
            <p class="text-muted">次数售价 {{ yuanFixed(card.counts_price) }}</p>
          </div>
          <div v-if="upgradeCardID === card.id" data-testid="card-upgrades" class="space-y-2 border-s-2 border-default ps-3 text-meta">
            <p v-if="upgrades.loading.value">正在读取升级记录…</p>
            <p v-for="entry in upgrades.items.value" :key="entry.id" data-testid="card-upgrade-entry">{{ entry.type_before.name }} → {{ entry.type_after.name }} · {{ entry.trigger === 'auto' ? '自动升级' : '手动升级' }} · {{ entry.business_date }} · {{ new Date(entry.at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' }) }} · {{ entry.actor.name }}</p>
            <p v-if="!upgrades.loading.value && !upgrades.items.value.length">暂无升级记录</p>
            <UButton v-if="upgrades.hasMore.value" :loading="upgrades.loading.value" size="sm" color="neutral" variant="ghost" @click="upgrades.loadMore">加载更多</UButton>
          </div>
          <div v-if="pointsCard === card.id" data-testid="card-points" class="space-y-2 border-s-2 border-default ps-3 text-meta">
            <p v-if="cardPoints.loading.value">正在读取积分流水…</p>
            <p v-for="entry in cardPoints.items.value" :key="entry.id" data-testid="card-points-entry">
              {{ ledgerKinds[entry.kind] || entry.kind }} · {{ entry.business_date }} · {{ new Date(entry.at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' }) }} · {{ entry.actor.name }}
              积分 {{ entry.before }} + {{ entry.delta }} = {{ entry.after }} <span v-if="entry.reason">· 原因：{{ entry.reason }}</span>
            </p>
            <p v-if="!cardPoints.loading.value && !cardPoints.items.value.length">暂无积分流水</p>
            <UButton v-if="cardPoints.hasMore.value" :loading="cardPoints.loading.value" size="sm" color="neutral" variant="ghost" @click="cardPoints.loadMore">加载更多</UButton>
          </div>
          <div v-if="countCard === card.id" data-testid="card-counts" class="space-y-2 border-s-2 border-default ps-3 text-meta">
            <p v-if="cardCounts.loading.value">正在读取次数流水…</p>
            <p v-for="entry in cardCounts.items.value" :key="entry.id" data-testid="card-count-entry">
              {{ entry.item.name }} · {{ ledgerKinds[entry.kind] || entry.kind }} · {{ entry.business_date }} · {{ new Date(entry.at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' }) }} · {{ entry.actor.name }}
              购买 {{ entry.paid_before }} + {{ entry.paid_delta }} = {{ entry.paid_after }}；赠送 {{ entry.gift_before }} + {{ entry.gift_delta }} = {{ entry.gift_after }}
              <span v-if="entry.reason">· 原因：{{ entry.reason }}</span>
            </p>
            <UButton v-if="cardCounts.hasMore.value" :loading="cardCounts.loading.value" size="sm" color="neutral" variant="ghost" @click="cardCounts.loadMore">加载更多</UButton>
          </div>
          <div v-if="adjustmentCard === card.id" data-testid="card-adjustments" class="space-y-2 border-s-2 border-default ps-3 text-meta">
            <p v-if="adjustments.loading.value">正在读取调整记录…</p>
            <p v-for="entry in adjustments.items.value" :key="entry.id" data-testid="card-adjustment-entry">
              {{ entry.business_date }} · {{ new Date(entry.at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' }) }} · {{ entry.actor.name }} · 原因：{{ entry.reason }}
              <span v-for="change in entry.counts" :key="change.id">{{ change.item.name }}：购买 {{ change.paid_before }} → {{ change.paid_after }}；赠送 {{ change.gift_before }} → {{ change.gift_after }}</span>
              <span v-if="entry.balance">本金 {{ entry.balance.principal_before }} + {{ entry.balance.principal_delta }} = {{ entry.balance.principal_after }}；赠送金 {{ entry.balance.bonus_before }} + {{ entry.balance.bonus_delta }} = {{ entry.balance.bonus_after }}</span>
              <span v-if="entry.expiry_before && entry.expiry_after">有效期 {{ entry.expiry_before.expires_on || '不限' }} → {{ entry.expiry_after.expires_on || '不限' }}</span>
              <span v-if="entry.card_type_before && entry.card_type_after">卡类型 {{ entry.card_type_before.name }} → {{ entry.card_type_after.name }}</span>
            </p>
            <UButton v-if="adjustments.hasMore.value" :loading="adjustments.loading.value" size="sm" color="neutral" variant="ghost" @click="adjustments.loadMore">加载更多</UButton>
          </div>
          <div v-if="cardLedgerCard === card.id" data-testid="card-ledger" class="space-y-2 border-s-2 border-default ps-3 text-meta">
            <p v-for="entry in cardLedger.items.value" :key="entry.id" data-testid="card-ledger-entry">
              {{ ledgerKinds[entry.kind] || entry.kind }} · {{ entry.business_date }} · {{ new Date(entry.at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' }) }} · {{ entry.actor.name }}
              本金 {{ entry.principal_before }} + {{ entry.principal_delta }} = {{ entry.principal_after }}；赠送金 {{ entry.bonus_before }} + {{ entry.bonus_delta }} = {{ entry.bonus_after }}
              <span v-if="entry.reason">· 原因：{{ entry.reason }}</span>
              <span v-if="entry.other_card_id">· 对方卡：{{ entry.other_card_id }}</span>
              <span v-if="entry.sales_staff.length">· 业绩归属人：{{ entry.sales_staff.map(s => s.name).join('、') }}</span>
            </p>
            <UButton v-if="cardLedger.hasMore.value" :loading="cardLedger.loading.value" size="sm" color="neutral" variant="ghost" @click="cardLedger.loadMore">加载更多</UButton>
          </div>
        </div>
      </div>
    </section>
  </div>
  <UModal v-model:open="refundOpen" title="退卡">
    <template #body>
      <div class="space-y-5">
        <p v-if="refundLoading" class="text-sm text-muted">正在计算退款…</p>
        <p v-if="refundError" role="alert" class="text-sm text-error">{{ refundError }}</p>
        <template v-if="refundQuote">
          <!-- 退款明细照 Fresha 的金额汇总：左名目右金额，最后一行合计加粗、上方一道线。 -->
          <div class="space-y-2 text-sm tabular-nums">
            <p class="flex justify-between gap-4"><span class="text-muted">剩余本金</span>{{ ' ' }}<span>{{ refundQuote.principal }}</span></p>
            <p class="flex justify-between gap-4"><span class="text-muted">作废的赠送金</span>{{ ' ' }}<span>{{ refundQuote.bonus }}</span></p>
            <p v-for="item in refundQuote.items" :key="item.item.id" class="flex justify-between gap-4"><span class="text-muted">{{ item.item.name }} · 购买次数 {{ item.paid_remaining }} × 单次金额 {{ item.unit_price }}</span>{{ ' ' }}<span>{{ item.amount }}</span></p>
            <p v-if="refundQuote.bonus_policy === 'deduct_used'" class="flex justify-between gap-4"><span class="text-muted">扣回赠送金</span>{{ ' ' }}<span>{{ refundQuote.bonus_deducted }}</span></p>
            <p class="flex justify-between gap-4"><span class="text-muted">手续费 {{ refundQuote.fee_percent }}%</span>{{ ' ' }}<span>{{ refundQuote.fee }}</span></p>
            <p data-testid="refund-amount" class="flex justify-between gap-4 border-t border-default pt-3 text-sm font-semibold text-highlighted"><span>退款金额</span>{{ ' ' }}<span>{{ refundQuote.amount }}</span></p>
          </div>
          <UFormField label="退款方式" required><MethodPicker v-model="refundMethod" :items="refundMethods" label="退款方式" /></UFormField>
        </template>
      </div>
    </template>
    <template #footer>
      <ModalActions @cancel="refundOpen = false">
        <UButton :loading="refundSaving" :disabled="refundLoading || !refundQuote || !refundMethod" @click="saveRefund">确认退卡</UButton>
      </ModalActions>
    </template>
  </UModal>
  <UModal v-model:open="balanceOpen" :title="balanceMode === 'upgrade' ? '升级' : '转余额'">
    <template #body>
      <div class="space-y-5">
        <template v-if="balanceMode === 'upgrade'">
          <UFormField label="目标卡类型" help="补差价（可为零），有效期不变。"><USelect v-model="balanceTo" :items="balanceTypes.map(t => ({ label: t.name, value: t.id }))" class="w-full" /></UFormField>
        </template>
        <UFormField v-else label="目标卡"><USelect v-model="balanceTo" :items="transferTargets.map(c => ({ label: `${c.card_type_name} · ${c.id}`, value: c.id }))" class="w-full" /></UFormField>
        <div class="grid grid-cols-2 gap-4">
          <UFormField label="本金"><UInput v-model="balancePrincipal" inputmode="decimal" class="w-full" /></UFormField>
          <UFormField label="赠送金"><UInput v-model="balanceBonus" inputmode="decimal" class="w-full" /></UFormField>
        </div>
        <UFormField v-if="balanceMode === 'upgrade' && hasDifference" label="业绩归属人"><USelect v-model="balanceSales" multiple :items="balanceStaff.map(s => ({ label: s.name, value: s.id }))" class="w-full" /></UFormField>
        <PaymentEditor v-if="balanceMode === 'upgrade'" v-model="balancePayments" v-model:touched="balancePaymentsTouched" :expected="balancePrincipal" />
        <UFormField v-if="balanceMode === 'transfer'" label="原因" required><UTextarea v-model="balanceReason" :maxlength="200" class="w-full" /></UFormField>
      </div>
    </template>
    <template #footer>
      <ModalActions @cancel="balanceOpen = false">
        <UButton :loading="balanceSaving" :disabled="balanceLoading || !balanceTo || (balanceMode === 'upgrade' && hasDifference && !balanceSales.length) || (balanceMode === 'transfer' && (!hasDifference || !balanceReason.trim()))" @click="saveBalance">保存</UButton>
      </ModalActions>
    </template>
  </UModal>
  <UModal v-model:open="adjustModal" title="调整">
    <template #body>
      <div class="space-y-5">
        <UFormField label="积分"><UInput v-model="adjustPoints" type="number" :min="0" :max="99999999" :step="1" /></UFormField>
        <div class="grid grid-cols-2 gap-4">
          <UFormField label="本金"><UInput v-model="adjustPrincipal" inputmode="decimal" class="w-full" /></UFormField>
          <UFormField label="赠送金"><UInput v-model="adjustBonus" inputmode="decimal" class="w-full" /></UFormField>
        </div>
        <div v-for="row in adjustCounts" :key="row.item_id" class="grid grid-cols-2 gap-4" data-testid="adjust-count-item">
          <UFormField :label="`${row.name}购买次数`"><UInput v-model="row.paid" type="number" :min="0" /></UFormField>
          <UFormField :label="`${row.name}赠送次数`"><UInput v-model="row.gift" type="number" :min="0" /></UFormField>
        </div>
        <UCheckbox v-model="adjustUnlimited" label="不限" />
        <UFormField v-if="!adjustUnlimited" label="有效期"><DateInput v-model="adjustExpires" class="w-full" /></UFormField>
        <UFormField label="卡类型"><USelect v-model="adjustType" :items="adjustTypes.map(t => ({ label: t.name, value: t.id }))" class="w-full" /></UFormField>
        <UFormField label="原因" required><UTextarea v-model="adjustReason" :maxlength="200" class="w-full" /></UFormField>
      </div>
    </template>
    <template #footer>
      <ModalActions @cancel="adjustModal = false">
        <UButton :loading="adjustSaving" :disabled="adjustLoading || !adjustReason.trim() || (!adjustUnlimited && !adjustExpires)" @click="saveAdjustment">保存</UButton>
      </ModalActions>
    </template>
  </UModal>
  <UModal v-model:open="cardModal" :title="cardTarget ? '充值' : '开卡'">
    <template #body>
      <div class="space-y-5">
        <UFormField v-if="!cardTarget" label="卡类型"><USelect v-model="cardCardType" :items="cardTypes.map(t => ({ label: t.name, value: t.id }))" class="w-full" /></UFormField>
        <div v-if="cardTarget || openingType?.kind !== 'count'" class="grid grid-cols-2 gap-4">
          <UFormField label="本金"><UInput v-model="cardPrincipal" inputmode="decimal" class="w-full" /></UFormField>
          <UFormField label="赠送金"><UInput v-model="cardBonus" inputmode="decimal" class="w-full" /></UFormField>
        </div>
        <p v-for="item in openingType?.items ?? []" :key="item.item_id" class="text-sm">{{ item.item_name }} · 购买 {{ item.paid_count }} · 赠送 {{ item.gift_count }} · 单次金额 {{ item.unit_price }}</p>
        <UFormField label="业绩归属人"><USelect v-model="cardSales" multiple :items="cardStaff.map(s => ({ label: s.name, value: s.id }))" class="w-full" /></UFormField>
        <PaymentEditor v-model="cardPayments" v-model:touched="cardPaymentsTouched" :expected="openingExpected" />
      </div>
    </template>
    <template #footer>
      <ModalActions @cancel="cardModal = false">
        <UButton :loading="cardSaving" :disabled="cardOptionsLoading || !cardSales.length || (!cardTarget && !cardCardType)" @click="cardSave">保存</UButton>
      </ModalActions>
    </template>
  </UModal>
</template>
