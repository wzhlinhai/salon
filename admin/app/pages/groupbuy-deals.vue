<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'settings.manage' })

type Deal = components['schemas']['Deal']
type Item = components['schemas']['Item']

const api = useApi()
const toast = useToast()
const deals = ref<Deal[]>([])
const items = ref<Item[]>([])
const loading = ref(false)
const loadGuard = useRequestGuard()
const platformItems = [{ label: '美团', value: 'meituan' }, { label: '抖音', value: 'douyin' }]
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
const platform = ref('meituan')
const name = ref('')
const purchasePrice = ref('0.00')
const itemID = ref('')
const active = ref(true)

function openEditor(deal: Deal | null) {
  editor.begin(() => {
    editing.value = deal
    platform.value = deal?.platform ?? 'meituan'
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
    <div class="flex items-center justify-between">
      <h1 class="text-xl font-semibold">团购商品</h1>
      <UButton @click="openEditor(null)">新建团购商品</UButton>
    </div>
    <p class="text-sm text-muted">平台购买价用于和平台月结账对账；门店标价取所对应项目的标准价。平台创建后不能修改。</p>
    <p v-if="loading">加载中</p>
    <UCard v-for="deal in deals" :key="deal.id" :data-deal="deal.name">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <span class="font-medium">{{ deal.name }}</span>
          <UBadge class="ml-2" color="neutral">{{ platformItems.find(p => p.value === deal.platform)?.label ?? deal.platform }}</UBadge>
          <UBadge class="ml-2" :color="deal.active ? 'success' : 'neutral'" variant="subtle">{{ deal.active ? '启用' : '停用' }}</UBadge>
          <p>平台购买价 {{ deal.purchase_price }} 元 · 对应项目 {{ deal.item_name }}</p>
        </div>
        <UButton variant="ghost" @click="openEditor(deal)">编辑</UButton>
      </div>
    </UCard>
    <p v-if="!loading && !deals.length" class="text-sm text-muted">还没有团购商品</p>

    <UModal v-model:open="open" :title="editing ? '编辑团购商品' : '新建团购商品'">
      <template #body>
        <div class="space-y-4">
          <UFormField label="平台">
            <USelect v-if="!editing" v-model="platform" :items="platformItems" class="w-full" aria-label="平台" />
            <p v-else>{{ platformItems.find(p => p.value === editing?.platform)?.label ?? editing.platform }}</p>
          </UFormField>
          <UFormField label="商品名称"><UInput v-model="name" :maxlength="200" aria-label="商品名称" /></UFormField>
          <UFormField label="平台购买价"><UInput v-model="purchasePrice" inputmode="decimal" aria-label="平台购买价" /></UFormField>
          <UFormField label="对应门店项目"><USelect v-model="itemID" :items="itemItems" class="w-full" aria-label="对应门店项目" placeholder="选择项目" /></UFormField>
          <USwitch v-if="editing" v-model="active" label="启用" />
        </div>
      </template>
      <template #footer>
        <div class="flex gap-2">
          <UButton :loading="saving" @click="save">保存</UButton>
          <UButton variant="ghost" @click="open = false">取消</UButton>
        </div>
      </template>
    </UModal>
  </div>
</template>
