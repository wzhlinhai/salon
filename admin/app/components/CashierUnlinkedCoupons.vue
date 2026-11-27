<script setup lang="ts">
import type { components } from '~/utils/api'

// 近 90 天核销了但还没关联挂单的团购券：可以补关联到一张挂单，或撤销核销。
type Order = components['schemas']['Order']
type Deal = components['schemas']['Deal']
type Redemption = components['schemas']['Redemption']

const NONE = '__none__'
const props = defineProps<{
  businessDate: string
  openOrders: Order[]
  customerName: (id: string) => string
}>()
const emit = defineEmits<{ linked: [], revoked: [row: Redemption] }>()
const api = useApi()
const toast = useToast()
const { can } = useMe()
const canGroupbuy = computed(() => can('groupbuy.redeem'))
const unlinked = ref<Redemption[]>([])
const unlinkedGuard = useRequestGuard()
const revoking = ref(false)
const revokeGuard = useRequestGuard()
const linkOrderItems = computed(() => props.openOrders.map(o => openOrderOption(o, props.customerName)))

async function load() {
  if (!canGroupbuy.value || !props.businessDate) return
  const current = unlinkedGuard.next()
  const { data, error } = await api.POST('/api/ops/list_redemptions', {
    body: { from: addDays(props.businessDate, -90), to: props.businessDate, status: 'redeemed', unlinked_only: true }
  })
  if (!current()) return
  if (!data) {
    toast.add({ title: errorText(error), color: 'error' })
    return
  }
  unlinked.value = data.items
}

async function revokeCoupon(row: Redemption) {
  if (!canGroupbuy.value) return
  const current = revokeGuard.next()
  revoking.value = true
  try {
    const { data, error } = await api.POST('/api/ops/revoke_coupon', { body: { key: row.key } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    toast.add({ title: '已撤销核销', color: 'success' })
    emit('revoked', row)
    await load()
    focusScan()
  } finally {
    if (current()) revoking.value = false
  }
}

const linkForm = useModalForm()
const { open: linkOpen, saving: linkSaving } = linkForm
const linkTarget = ref<Redemption | null>(null)
const deals = ref<Deal[]>([])
const linkDealID = ref(NONE)
const linkTargetOrderID = ref(NONE)
const dealItems = computed(() => deals.value.filter(d => d.active).map(d => ({ label: `${d.name}（购买价 ${d.purchase_price}）`, value: d.id })))

async function linkBegin(row: Redemption) {
  if (!canGroupbuy.value) return
  linkForm.begin(() => {
    linkTarget.value = row
    linkDealID.value = NONE
    linkTargetOrderID.value = NONE
  })
  await linkForm.load(async (current) => {
    const { data, error } = await api.POST('/api/ops/list_groupbuy_deals', { body: {} })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    deals.value = data.items.filter(d => d.platform === row.platform)
    linkDealID.value = deals.value.find(d => d.active)?.id ?? NONE
  })
}

async function saveLink() {
  const row = linkTarget.value
  if (!canGroupbuy.value || !row || linkDealID.value === NONE || linkTargetOrderID.value === NONE) return
  await linkForm.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/link_coupon_order', {
      body: { redemption_id: row.id, deal_id: linkDealID.value, order_id: linkTargetOrderID.value }
    })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    linkOpen.value = false
    toast.add({ title: '已关联挂单', color: 'success' })
    emit('linked')
    await load()
    focusScan()
  })
}

defineExpose({ load })
</script>

<template>
  <!-- 没有未关联的券就整块不出现（DESIGN.md 取舍：不写「没有」的区块），平时收银台少一块。
       单根加 v-show：原来区块 v-if 和弹窗并列成两个根，区块从无到有时 Vue 偶发 nextSibling 空引用；弹窗本来就传送到 body。 -->
  <section v-show="canGroupbuy && unlinked.length" data-testid="unlinked-coupons">
    <h2 class="sec">未关联团购券<span class="ms-1 font-normal text-dimmed">{{ unlinked.length }}</span><span class="ms-auto text-meta font-normal text-dimmed">近 90 天</span></h2>
    <div class="border-y border-default">
      <div v-for="row in unlinked" :key="row.id" class="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-default px-3 py-2 text-sm first:border-t-0 hover:bg-hover">
        <span class="tag bg-tag-gray">{{ platformLabel(row.platform) }}</span>
        <span class="font-medium tabular-nums">{{ row.coupon_code }}</span>
        <span class="text-meta text-muted">{{ row.redeemed_at ? new Date(row.redeemed_at).toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai' }) : '' }}</span>
        <UButton size="sm" color="neutral" variant="outline" class="ms-auto" @click="linkBegin(row)">关联挂单</UButton>
        <UButton size="sm" color="error" variant="ghost" :loading="revoking" @click="revokeCoupon(row)">撤销核销</UButton>
      </div>
    </div>
    <UModal v-model:open="linkOpen" title="关联团购券到挂单" :description="`券码 ${linkTarget?.coupon_code ?? ''}（${linkTarget ? platformLabel(linkTarget.platform) : ''}）`">
      <template #body>
        <div class="space-y-5">
          <UFormField label="团购商品"><USelect v-model="linkDealID" :items="dealItems" class="w-full" aria-label="关联团购商品" /></UFormField>
          <UFormField label="挂单"><USelect v-model="linkTargetOrderID" :items="[{ label: '选择挂单', value: NONE }, ...linkOrderItems]" class="w-full" aria-label="关联到挂单" /></UFormField>
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="linkOpen = false">
          <UButton :loading="linkSaving" @click="saveLink">关联</UButton>
        </ModalActions>
      </template>
    </UModal>
  </section>
</template>
