<script setup lang="ts">
import type { components } from '~/utils/api'

const props = defineProps<{ id: string, merged: boolean }>()
const api = useApi()
const toast = useToast()
const { can } = useMe()

type CustomerCard = components['schemas']['Card']
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

async function cardEdit(card: CustomerCard | null) {
  cardForm.begin(() => {
    cardTarget.value = card
    cardPrincipal.value = '0.00'
    cardBonus.value = '0.00'
    cardSales.value = []
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
  const countsOnly = !cardTarget.value && openingType.value?.kind === 'count'
  const body = { principal: countsOnly ? '0.00' : cardPrincipal.value, bonus: countsOnly ? '0.00' : cardBonus.value, sales_staff_ids: [...cardSales.value] }
  await cardForm.save(async (current) => {
    const { data, error } = cardTarget.value
      ? await api.POST('/api/ops/recharge_card', { body: { ...body, card_id: cardTarget.value.id } })
      : await api.POST('/api/ops/open_card', { body: { ...body, customer_id: id, card_type_id: cardCardType.value } })
    if (!current()) return
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
    if (!current()) return
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
const balanceReason = ref('')
const balanceTypes = ref<components['schemas']['CardType'][]>([])
const balanceStaff = ref<components['schemas']['StaffOption'][]>([])
const hasDifference = computed(() => Number(balancePrincipal.value) > 0 || Number(balanceBonus.value) > 0)
const transferTargets = computed(() => cardCards.value.filter(c => c.id !== balanceCard.value?.id && c.kind !== 'count'))

async function editBalance(card: CustomerCard, mode: 'upgrade' | 'transfer') {
  balanceForm.begin(() => {
    balanceCard.value = card
    balanceMode.value = mode
    balanceTo.value = ''
    balancePrincipal.value = '0.00'
    balanceBonus.value = '0.00'
    balanceSales.value = []
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
      ? await api.POST('/api/ops/upgrade_card', { body: { ...amounts, card_id: card.id, to_type_id: balanceTo.value, sales_staff_ids: hasDifference.value ? [...balanceSales.value] : [] } })
      : await api.POST('/api/ops/transfer_card_balance', { body: { ...amounts, from_card_id: card.id, to_card_id: balanceTo.value, reason: balanceReason.value.trim() } })
    if (!current()) return
    if (data) {
      balanceOpen.value = false
      await refreshCards()
    } else toast.add({ title: errorText(error), color: 'error' })
  })
}

const ledgerKinds: Record<string, string> = { open: '开卡', recharge: '充值', adjust: '调整', upgrade: '升级', transfer_out: '转出', transfer_in: '转入', consume: '消费', void: '撤单退回' }

// 所有写操作刷新同一组已展开的明细，避免某个弹窗漏掉积分或升级记录。
async function refreshCards() {
  await Promise.all([
    cardLoad(),
    ...(pointsCard.value ? [cardPoints.reload()] : []),
    ...(cardLedgerCard.value ? [cardLedger.reload()] : []),
    ...(countCard.value ? [cardCounts.reload()] : []),
    ...(adjustmentCard.value ? [adjustments.reload()] : []),
    ...(upgradeCardID.value ? [upgrades.reload()] : [])
  ])
}

onMounted(cardLoad)
</script>

<template>
  <div>
    <UCard>
      <template #header>
        <div class="flex justify-between items-center">
          <h3 class="font-medium">卡</h3>
          <UButton v-if="can('card.sell') && !props.merged" size="sm" @click="cardEdit(null)">开卡</UButton>
        </div>
      </template>
      <p v-if="cardLoading">正在读取卡…</p>
      <p v-else-if="!cardCards.length">暂无卡</p>
      <div v-for="card in cardCards" :key="card.id" :data-card="card.id" class="space-y-2 py-3 border-b border-default">
        <p>{{ card.card_type_name }} · 本金 {{ card.principal }} · 赠送金 {{ card.bonus }} · 积分 {{ card.points }} · 有效期 {{ card.expires_on || '不限' }}</p>
        <p v-for="item in card.items" :key="item.item_id" data-testid="remaining-counts">{{ item.item_name }} · 购买 {{ item.paid_remaining }} · 赠送 {{ item.gift_remaining }}</p>
        <p v-if="card.items.length">次数售价 {{ card.counts_price }}</p>
        <div class="flex gap-2">
          <UButton v-if="card.kind !== 'count' && can('card.sell') && !props.merged" size="xs" variant="outline" @click="cardEdit(card)">充值</UButton>
          <UButton v-if="can('card.adjust')" size="xs" variant="outline" @click="editAdjustment(card)">调整</UButton>
          <UButton size="xs" variant="outline" @click="cardShowLedger(card.id)">流水</UButton>
          <UButton size="xs" variant="outline" @click="showPoints(card.id)">积分流水</UButton>
          <UButton v-if="card.items.length" size="xs" variant="outline" @click="showCounts(card.id)">次数流水</UButton>
          <UButton v-if="can('card.sell') && card.kind === 'stored_value'" size="xs" variant="outline" @click="editBalance(card, 'upgrade')">升级</UButton>
          <UButton v-if="can('card.adjust')" size="xs" variant="outline" @click="editBalance(card, 'transfer')">转余额</UButton>
          <UButton size="xs" variant="outline" @click="showUpgrades(card.id)">升级记录</UButton>
          <UButton size="xs" variant="outline" @click="showAdjustments(card.id)">调整记录</UButton>
        </div>
        <div v-if="upgradeCardID === card.id" data-testid="card-upgrades" class="space-y-2 text-sm">
          <p v-if="upgrades.loading.value">正在读取升级记录…</p>
          <p v-for="entry in upgrades.items.value" :key="entry.id" data-testid="card-upgrade-entry">{{ entry.type_before.name }} → {{ entry.type_after.name }} · {{ entry.trigger === 'auto' ? '自动升级' : '手动升级' }} · {{ entry.business_date }} · {{ new Date(entry.at).toLocaleString('zh-CN') }} · {{ entry.actor.name }}</p>
          <p v-if="!upgrades.loading.value && !upgrades.items.value.length">暂无升级记录</p>
          <UButton v-if="upgrades.hasMore.value" :loading="upgrades.loading.value" variant="outline" @click="upgrades.loadMore">加载更多</UButton>
        </div>
        <div v-if="pointsCard === card.id" data-testid="card-points" class="space-y-2 text-sm">
          <p v-if="cardPoints.loading.value">正在读取积分流水…</p>
          <p v-for="entry in cardPoints.items.value" :key="entry.id" data-testid="card-points-entry">
            {{ ledgerKinds[entry.kind] || entry.kind }} · {{ entry.business_date }} · {{ new Date(entry.at).toLocaleString('zh-CN') }} · {{ entry.actor.name }}
            积分 {{ entry.before }} + {{ entry.delta }} = {{ entry.after }} <span v-if="entry.reason">· 原因：{{ entry.reason }}</span>
          </p>
          <p v-if="!cardPoints.loading.value && !cardPoints.items.value.length">暂无积分流水</p>
          <UButton v-if="cardPoints.hasMore.value" :loading="cardPoints.loading.value" variant="outline" @click="cardPoints.loadMore">加载更多</UButton>
        </div>
        <div v-if="countCard === card.id" data-testid="card-counts" class="space-y-2 text-sm">
          <p v-if="cardCounts.loading.value">正在读取次数流水…</p>
          <p v-for="entry in cardCounts.items.value" :key="entry.id" data-testid="card-count-entry">
            {{ entry.item.name }} · {{ ledgerKinds[entry.kind] || entry.kind }} · {{ entry.business_date }} · {{ new Date(entry.at).toLocaleString('zh-CN') }} · {{ entry.actor.name }}
            购买 {{ entry.paid_before }} + {{ entry.paid_delta }} = {{ entry.paid_after }}；赠送 {{ entry.gift_before }} + {{ entry.gift_delta }} = {{ entry.gift_after }}
            <span v-if="entry.reason">· 原因：{{ entry.reason }}</span>
          </p>
          <UButton v-if="cardCounts.hasMore.value" :loading="cardCounts.loading.value" variant="outline" @click="cardCounts.loadMore">加载更多</UButton>
        </div>
        <div v-if="adjustmentCard === card.id" data-testid="card-adjustments" class="space-y-2 text-sm">
          <p v-if="adjustments.loading.value">正在读取调整记录…</p>
          <p v-for="entry in adjustments.items.value" :key="entry.id" data-testid="card-adjustment-entry">
            {{ entry.business_date }} · {{ new Date(entry.at).toLocaleString('zh-CN') }} · {{ entry.actor.name }} · 原因：{{ entry.reason }}
            <span v-for="change in entry.counts" :key="change.id">{{ change.item.name }}：购买 {{ change.paid_before }} → {{ change.paid_after }}；赠送 {{ change.gift_before }} → {{ change.gift_after }}</span>
            <span v-if="entry.balance">本金 {{ entry.balance.principal_before }} + {{ entry.balance.principal_delta }} = {{ entry.balance.principal_after }}；赠送金 {{ entry.balance.bonus_before }} + {{ entry.balance.bonus_delta }} = {{ entry.balance.bonus_after }}</span>
            <span v-if="entry.expiry_before && entry.expiry_after">有效期 {{ entry.expiry_before.expires_on || '不限' }} → {{ entry.expiry_after.expires_on || '不限' }}</span>
            <span v-if="entry.card_type_before && entry.card_type_after">卡类型 {{ entry.card_type_before.name }} → {{ entry.card_type_after.name }}</span>
          </p>
          <UButton v-if="adjustments.hasMore.value" :loading="adjustments.loading.value" variant="outline" @click="adjustments.loadMore">加载更多</UButton>
        </div>
        <div v-if="cardLedgerCard === card.id" data-testid="card-ledger" class="space-y-2 text-sm">
          <p v-for="entry in cardLedger.items.value" :key="entry.id" data-testid="card-ledger-entry">
            {{ ledgerKinds[entry.kind] || entry.kind }} · {{ entry.business_date }} · {{ new Date(entry.at).toLocaleString('zh-CN') }} · {{ entry.actor.name }}
            本金 {{ entry.principal_before }} + {{ entry.principal_delta }} = {{ entry.principal_after }}；赠送金 {{ entry.bonus_before }} + {{ entry.bonus_delta }} = {{ entry.bonus_after }}
            <span v-if="entry.reason">· 原因：{{ entry.reason }}</span>
            <span v-if="entry.other_card_id">· 对方卡：{{ entry.other_card_id }}</span>
            <span v-if="entry.sales_staff.length">· 业绩归属人：{{ entry.sales_staff.map(s => s.name).join('、') }}</span>
          </p>
          <UButton v-if="cardLedger.hasMore.value" :loading="cardLedger.loading.value" variant="outline" @click="cardLedger.loadMore">加载更多</UButton>
        </div>
      </div>
    </UCard>
  </div>
  <UModal v-model:open="balanceOpen" :title="balanceMode === 'upgrade' ? '升级' : '转余额'">
    <template #body>
      <div class="space-y-3">
        <template v-if="balanceMode === 'upgrade'">
          <UFormField label="目标卡类型"><USelect v-model="balanceTo" :items="balanceTypes.map(t => ({ label: t.name, value: t.id }))" class="w-full" /></UFormField>
          <p>补差价（可为零），有效期不变。</p>
        </template>
        <UFormField v-else label="目标卡"><USelect v-model="balanceTo" :items="transferTargets.map(c => ({ label: `${c.card_type_name} · ${c.id}`, value: c.id }))" class="w-full" /></UFormField>
        <UFormField label="本金"><UInput v-model="balancePrincipal" inputmode="decimal" class="w-full" /></UFormField>
        <UFormField label="赠送金"><UInput v-model="balanceBonus" inputmode="decimal" class="w-full" /></UFormField>
        <UFormField v-if="balanceMode === 'upgrade' && hasDifference" label="业绩归属人"><USelect v-model="balanceSales" multiple :items="balanceStaff.map(s => ({ label: s.name, value: s.id }))" class="w-full" /></UFormField>
        <UFormField v-if="balanceMode === 'transfer'" label="原因" required><UTextarea v-model="balanceReason" :maxlength="200" class="w-full" /></UFormField>
      </div>
    </template>
    <template #footer><UButton :loading="balanceSaving" :disabled="balanceLoading || !balanceTo || (balanceMode === 'upgrade' && hasDifference && !balanceSales.length) || (balanceMode === 'transfer' && (!hasDifference || !balanceReason.trim()))" @click="saveBalance">保存</UButton></template>
  </UModal>
  <UModal v-model:open="adjustModal" title="调整">
    <template #body>
      <div class="space-y-3">
        <UFormField label="积分"><UInput v-model="adjustPoints" type="number" :min="0" :max="99999999" :step="1" /></UFormField>
        <UFormField label="本金"><UInput v-model="adjustPrincipal" inputmode="decimal" class="w-full" /></UFormField>
        <UFormField label="赠送金"><UInput v-model="adjustBonus" inputmode="decimal" class="w-full" /></UFormField>
        <div v-for="row in adjustCounts" :key="row.item_id" data-testid="adjust-count-item">
          <p>{{ row.name }}</p>
          <UFormField :label="`${row.name}购买次数`"><UInput v-model="row.paid" type="number" :min="0" /></UFormField>
          <UFormField :label="`${row.name}赠送次数`"><UInput v-model="row.gift" type="number" :min="0" /></UFormField>
        </div>
        <UCheckbox v-model="adjustUnlimited" label="不限" />
        <UFormField v-if="!adjustUnlimited" label="有效期"><UInput v-model="adjustExpires" type="date" class="w-full" /></UFormField>
        <UFormField label="卡类型"><USelect v-model="adjustType" :items="adjustTypes.map(t => ({ label: t.name, value: t.id }))" class="w-full" /></UFormField>
        <UFormField label="原因" required><UTextarea v-model="adjustReason" :maxlength="200" class="w-full" /></UFormField>
      </div>
    </template>
    <template #footer><UButton :loading="adjustSaving" :disabled="adjustLoading || !adjustReason.trim() || (!adjustUnlimited && !adjustExpires)" @click="saveAdjustment">保存</UButton></template>
  </UModal>
  <UModal v-model:open="cardModal" :title="cardTarget ? '充值' : '开卡'">
    <template #body>
      <div class="space-y-3">
        <UFormField v-if="!cardTarget" label="卡类型"><USelect v-model="cardCardType" :items="cardTypes.map(t => ({ label: t.name, value: t.id }))" class="w-full" /></UFormField>
        <UFormField v-if="cardTarget || openingType?.kind !== 'count'" label="本金"><UInput v-model="cardPrincipal" inputmode="decimal" class="w-full" /></UFormField>
        <UFormField v-if="cardTarget || openingType?.kind !== 'count'" label="赠送金"><UInput v-model="cardBonus" inputmode="decimal" class="w-full" /></UFormField>
        <p v-for="item in openingType?.items ?? []" :key="item.item_id">{{ item.item_name }} · 购买 {{ item.paid_count }} · 赠送 {{ item.gift_count }} · 单次金额 {{ item.unit_price }}</p>
        <UFormField label="业绩归属人"><USelect v-model="cardSales" multiple :items="cardStaff.map(s => ({ label: s.name, value: s.id }))" class="w-full" /></UFormField>
      </div>
    </template>
    <template #footer><UButton :loading="cardSaving" :disabled="cardOptionsLoading || !cardSales.length || (!cardTarget && !cardCardType)" @click="cardSave">保存</UButton></template>
  </UModal>
</template>
