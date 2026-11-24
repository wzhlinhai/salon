<script setup lang="ts">
import type { components } from '~/utils/api'

// 收银扫到券码后的团购面板：验券 → 核销 → 立即开单 / 关联挂单 / 暂不开单，核销结果未知时查询状态。
// 平台下拉在页面的扫码框旁边，这一张券处理期间锁住（locked），免得查询、核销换了平台。
type Order = components['schemas']['Order']
type Deal = components['schemas']['Deal']
type Redemption = components['schemas']['Redemption']

const NONE = '__none__'
const props = defineProps<{
  platform: string
  openOrders: Order[]
  customerName: (id: string) => string
}>()
const emit = defineEmits<{
  'open-order': [deal: Deal, redemption: Redemption]
  'linked': [orderId: string]
  'unlinked': []
  'changed': []
  'cleared': []
}>()
const api = useApi()
const toast = useToast()
const { can } = useMe()
const canGroupbuy = computed(() => can('groupbuy.redeem'))

const couponPlatform = ref('')
const couponCode = ref('')
const couponKey = ref('')
const couponBusy = ref('')
const couponGuard = useRequestGuard()
const verifyResult = ref<components['schemas']['CouponResult'] | null>(null)
const redemption = ref<Redemption | null>(null)
const couponUnknown = ref(false)
const deals = ref<Deal[]>([])
const dealsGuard = useRequestGuard()
const selectedDealID = ref(NONE)
const linkOrderID = ref(NONE)
const actionSaving = ref(false)
const actionGuard = useRequestGuard()
const activeDeals = computed(() => deals.value.filter(d => d.active))
const dealItems = computed(() => activeDeals.value.map(d => ({ label: `${d.name}（购买价 ${d.purchase_price}）`, value: d.id })))
const linkOrderItems = computed(() => props.openOrders.map(o => openOrderOption(o, props.customerName)))
const shown = computed(() => !!verifyResult.value || !!redemption.value || couponUnknown.value)
const locked = computed(() => couponBusy.value !== '' || !!verifyResult.value || !!redemption.value || couponUnknown.value)

function startCouponAttempt(couponPlatformValue: string) {
  couponKey.value = crypto.randomUUID()
  couponPlatform.value = couponPlatformValue
  couponUnknown.value = false
}

async function verify(code: string) {
  if (!canGroupbuy.value) {
    toast.add({ title: '没有团购核销权限', color: 'error' })
    return
  }
  couponCode.value = code.trim()
  if (!couponCode.value) return
  const current = couponGuard.next()
  startCouponAttempt(props.platform)
  redemption.value = null
  verifyResult.value = null
  couponBusy.value = 'verify'
  try {
    const { data, error } = await api.POST('/api/ops/verify_coupon', {
      body: { platform: couponPlatform.value, coupon_code: couponCode.value, key: couponKey.value }
    })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    verifyResult.value = data
  } finally {
    if (current()) couponBusy.value = ''
  }
}

async function redeemCoupon() {
  if (!canGroupbuy.value || !verifyResult.value || couponBusy.value) return
  const current = couponGuard.hold()
  couponBusy.value = 'redeem'
  try {
    const { data, error } = await api.POST('/api/ops/redeem_coupon', {
      body: { platform: couponPlatform.value, coupon_code: couponCode.value, key: couponKey.value }
    })
    if (!current()) return
    if (!data) {
      couponUnknown.value = true
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    redemption.value = data
    verifyResult.value = { platform: data.platform, status: 'redeemed', redeemed_at: data.redeemed_at }
    await loadDeals(data.platform)
    emit('changed')
    focusScan()
  } finally {
    if (current()) couponBusy.value = ''
  }
}

async function queryRedemption() {
  if (!canGroupbuy.value || !couponKey.value || couponBusy.value) return
  const current = couponGuard.hold()
  couponBusy.value = 'query'
  try {
    const { data, error } = await api.POST('/api/ops/get_redemption', { body: { key: couponKey.value } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    redemption.value = data
    couponUnknown.value = data.status === 'unknown'
    if (data.status === 'redeemed') await loadDeals(data.platform)
    emit('changed')
    focusScan()
  } finally {
    if (current()) couponBusy.value = ''
  }
}

async function loadDeals(couponPlatform: string) {
  if (!canGroupbuy.value) return
  const current = dealsGuard.next()
  const { data, error } = await api.POST('/api/ops/list_groupbuy_deals', { body: {} })
  if (!current()) return
  if (!data) {
    toast.add({ title: errorText(error), color: 'error' })
    return
  }
  deals.value = data.items.filter(d => d.platform === couponPlatform)
  selectedDealID.value = deals.value.find(d => d.active)?.id ?? NONE
}

async function linkRedeemed() {
  if (!canGroupbuy.value || !redemption.value || selectedDealID.value === NONE || linkOrderID.value === NONE) return
  const current = couponGuard.hold()
  couponBusy.value = 'link'
  try {
    const { data, error } = await api.POST('/api/ops/link_coupon_order', {
      body: { redemption_id: redemption.value.id, deal_id: selectedDealID.value, order_id: linkOrderID.value }
    })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    toast.add({ title: '已关联挂单', color: 'success' })
    const id = linkOrderID.value
    clear()
    emit('linked', id)
  } finally {
    if (current()) couponBusy.value = ''
  }
}

function openOrderFromCoupon() {
  const deal = activeDeals.value.find(d => d.id === selectedDealID.value)
  if (!deal || !redemption.value) return
  emit('open-order', deal, redemption.value)
}

async function revokeCoupon(row: Redemption) {
  if (!canGroupbuy.value) return
  const current = actionGuard.next()
  actionSaving.value = true
  try {
    const { data, error } = await api.POST('/api/ops/revoke_coupon', { body: { key: row.key } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    toast.add({ title: '已撤销核销', color: 'success' })
    clear()
    emit('changed')
  } finally {
    if (current()) actionSaving.value = false
  }
}

async function unlinkCoupon(redemptionID: string) {
  if (!canGroupbuy.value) return
  const current = actionGuard.next()
  actionSaving.value = true
  try {
    const { data, error } = await api.POST('/api/ops/unlink_coupon_order', { body: { redemption_id: redemptionID } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    toast.add({ title: '已解除关联', color: 'success' })
    emit('unlinked')
  } finally {
    if (current()) actionSaving.value = false
  }
}

function clear() {
  couponGuard.invalidate()
  verifyResult.value = null
  redemption.value = null
  couponUnknown.value = false
  couponCode.value = ''
  couponKey.value = ''
  couponPlatform.value = ''
  selectedDealID.value = NONE
  linkOrderID.value = NONE
  couponBusy.value = ''
  emit('cleared')
}

// 未关联列表里撤销了同一张券：面板跟着关掉。
function forget(redemptionID: string) {
  if (redemption.value?.id === redemptionID) clear()
}

defineExpose({ verify, clear, forget, locked })
</script>

<template>
  <div v-if="canGroupbuy && shown" class="box space-y-3" data-testid="coupon-panel">
    <div class="flex flex-wrap items-center gap-2">
      <span class="tag bg-tag-gray">{{ platformLabel(couponPlatform || platform) }}</span>
      <span class="font-medium tabular-nums">券码 {{ couponCode }}</span>
      <span v-if="redemption" data-testid="coupon-status" class="tag" :class="redemption.status === 'redeemed' ? 'bg-tag-green' : 'bg-tag-yellow'">{{ redemption.status }}</span>
      <span v-else-if="verifyResult" class="tag" :class="verifyResult.status === 'unused' ? 'bg-tag-green' : 'bg-tag-yellow'">{{ verifyResult.status }}</span>
    </div>

    <div v-if="verifyResult && !redemption && verifyResult.status === 'unused' && !couponUnknown" class="flex flex-wrap items-center gap-2">
      <span class="text-sm">券未使用，可以核销。</span>
      <UButton size="sm" color="neutral" :loading="couponBusy === 'redeem'" @click="redeemCoupon">核销</UButton>
    </div>

    <div v-if="couponUnknown || (redemption && redemption.status === 'unknown')" class="flex flex-wrap items-center gap-2">
      <span class="text-sm text-ink-orange">核销结果未知，请先查询状态，不要再次核销。</span>
      <UButton size="sm" color="neutral" variant="outline" :loading="couponBusy === 'query'" @click="queryRedemption">查询状态</UButton>
    </div>

    <template v-if="redemption && redemption.status === 'redeemed' && !redemption.order_id">
      <div class="grid gap-2 md:grid-cols-2">
        <UFormField label="团购商品">
          <USelect v-model="selectedDealID" :items="dealItems" class="w-full" aria-label="团购商品" />
        </UFormField>
        <UFormField label="关联挂单">
          <USelect v-model="linkOrderID" :items="[{ label: '选择挂单', value: NONE }, ...linkOrderItems]" class="w-full" aria-label="关联挂单" />
        </UFormField>
      </div>
      <p v-if="!activeDeals.length" class="text-sm text-ink-orange">这个平台还没有启用的团购商品，请先到“团购商品”登记。</p>
      <div class="flex flex-wrap gap-2">
        <UButton size="sm" color="neutral" :disabled="!activeDeals.length" @click="openOrderFromCoupon">立即开单</UButton>
        <UButton size="sm" color="neutral" variant="outline" :disabled="!activeDeals.length || linkOrderID === NONE" :loading="couponBusy === 'link'" @click="linkRedeemed">关联挂单</UButton>
        <UButton size="sm" color="neutral" variant="ghost" @click="clear">暂不开单</UButton>
        <UButton size="sm" color="error" variant="ghost" class="ms-auto" :loading="actionSaving" @click="revokeCoupon(redemption)">撤销核销</UButton>
      </div>
    </template>

    <div v-if="redemption && redemption.order_id" class="flex flex-wrap items-center gap-2">
      <span class="tag bg-tag-green">已关联挂单</span>
      <UButton size="sm" color="neutral" variant="outline" :loading="actionSaving" @click="unlinkCoupon(redemption.id)">解除关联</UButton>
      <UButton size="sm" color="neutral" variant="ghost" @click="clear">完成</UButton>
    </div>

    <div v-if="redemption && redemption.status === 'revoked'" class="flex items-center gap-2">
      <span class="tag bg-tag-gray">已撤销</span>
      <UButton size="sm" color="neutral" variant="ghost" @click="clear">关闭</UButton>
    </div>
  </div>
</template>
