<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'settings.manage', title: '团购商品', description: '美团、抖音上卖的团购，以及核销后对应店里哪个项目。' })

type Deal = components['schemas']['Deal']
type Item = components['schemas']['Item']
type Platform = components['schemas']['CreateDealInput']['platform']

const api = useApi()
const toast = useToast()
const deals = ref<Deal[]>([])
const items = ref<Item[]>([])
const loading = ref(false)
const loadGuard = useRequestGuard()
const itemItems = computed(() => items.value.filter(i => i.active).map(i => ({ label: `${i.name}（${i.price}）`, value: i.id })))

async function load() {
  const current = loadGuard.next()
  loading.value = true
  try {
    const [d, i] = await Promise.all([
      api.POST('/api/ops/list_groupbuy_deals', { body: {} }),
      api.POST('/api/ops/list_items', { body: {} })
    ])
    if (!current()) return
    if (!d.data || !i.data) {
      toast.add({ title: errorText(d.error ?? i.error), color: 'error' })
      return
    }
    deals.value = d.data.items
    items.value = i.data.items
  } finally {
    if (current()) loading.value = false
  }
}

onMounted(load)

const editor = useModalForm()
const { open, saving } = editor
const editing = ref<Deal | null>(null)
const platform = ref<Platform>('meituan')
const name = ref('')
const purchasePrice = ref('0.00')
const itemID = ref('')
const active = ref(true)

function openEditor(deal: Deal | null) {
  editor.begin(() => {
    editing.value = deal
    platform.value = (deal?.platform as Platform | undefined) ?? 'meituan'
    name.value = deal?.name ?? ''
    purchasePrice.value = deal?.purchase_price ?? '0.00'
    itemID.value = deal?.item_id ?? ''
    active.value = deal?.active ?? true
  })
}

async function save() {
  const body = { name: name.value, purchase_price: purchasePrice.value, item_id: itemID.value }
  await editor.save(async (current) => {
    const result = editing.value
      ? await api.POST('/api/ops/update_groupbuy_deal', { body: { ...body, id: editing.value.id, active: active.value } })
      : await api.POST('/api/ops/create_groupbuy_deal', { body: { ...body, platform: platform.value } })
    if (!current()) return
    if (result.data) {
      open.value = false
      toast.add({ title: '已保存', color: 'success' })
      await load()
    } else {
      toast.add({ title: errorText(result.error), color: 'error' })
    }
  })
}
</script>

<template>
  <div class="space-y-4">
    <PageActions>
      <UButton size="sm" icon="i-lucide-plus" @click="openEditor(null)">新建团购商品</UButton>
    </PageActions>
    <p class="text-sm text-muted">平台购买价用于和平台月结账对账；门店标价取所对应项目的标准价。平台创建后不能修改。</p>
    <div class="overflow-x-auto">
      <ListSkeleton v-if="loading && !deals.length" :rows="3" />
      <EmptyState v-else-if="!deals.length" art="receipt" title="还没有团购商品" hint="把美团、抖音上卖的团购商品录进来，核销和月底对账才对得上" />
      <div
        v-for="deal in deals"
        :key="deal.id"
        :data-deal="deal.name"
        class="group/row flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-default px-3 py-2.5 text-sm first:border-t-0 hover:bg-hover"
        :class="deal.active ? '' : 'text-dimmed'"
      >
        <div class="flex min-w-48 flex-1 items-center gap-1.5">
          <span class="font-medium">{{ deal.name }}</span>
          <span class="tag bg-tag-gray">{{ platformLabel(deal.platform) }}</span>
          <span v-if="!deal.active" class="tag bg-tag-gray">停用</span>
        </div>
        <span class="w-48 text-muted">对应项目 {{ deal.item_name }}</span>
        <span class="w-24 text-right font-medium tabular-nums">{{ yuanFixed(deal.purchase_price) }}</span>
        <UButton size="sm" color="neutral" variant="ghost" class="opacity-0 group-hover/row:opacity-100 focus-visible:opacity-100 any-pointer-coarse:opacity-100" @click="openEditor(deal)">编辑</UButton>
      </div>
    </div>

    <UModal v-model:open="open" :title="editing ? '编辑团购商品' : '新建团购商品'">
      <template #body>
        <div class="space-y-5">
          <div class="grid grid-cols-2 gap-4">
            <UFormField label="平台">
              <USelect v-if="!editing" v-model="platform" :items="platformItems" class="w-full" aria-label="平台" />
              <p v-else class="flex min-h-11 items-center">{{ platformLabel(editing.platform) }}</p>
            </UFormField>
            <UFormField label="平台购买价"><UInput v-model="purchasePrice" inputmode="decimal" aria-label="平台购买价" /></UFormField>
          </div>
          <UFormField label="商品名称"><UInput v-model="name" :maxlength="200" aria-label="商品名称" /></UFormField>
          <UFormField label="对应门店项目"><USelect v-model="itemID" :items="itemItems" class="w-full" aria-label="对应门店项目" placeholder="选择项目" /></UFormField>
          <USwitch v-if="editing" v-model="active" label="启用" />
        </div>
      </template>
      <template #footer>
        <ModalActions @cancel="open = false">
          <UButton :loading="saving" @click="save">保存</UButton>
        </ModalActions>
      </template>
    </UModal>
  </div>
</template>
