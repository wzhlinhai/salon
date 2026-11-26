<script setup lang="ts">
import type { CustomerGlance } from '~/composables/useCustomerGlance'

// 顾客小卡片（样稿 26）：点名字弹出一张纸，只放上次做了什么、怎么做的、卡里还剩多少；完整的去档案页。
// 打开时才取数，同一张卡片关了再开不重取。触发元素由使用方放进默认插槽（要是按钮）。
// reference：卡片贴着哪个元素弹（默认贴触发按钮）；列表里传整行，免得卡片盖住这一行。
const props = defineProps<{ customerId: string, reference?: HTMLElement | null }>()

const { load } = useCustomerGlance()
const open = ref(false)
const glance = ref<CustomerGlance | null>(null)
const loading = ref(false)
const failed = ref(false)

watch(open, async (value) => {
  if (!value || glance.value?.customer.id === props.customerId) return
  loading.value = true
  failed.value = false
  const id = props.customerId
  const data = await load(id).catch(() => null)
  if (id !== props.customerId) return
  loading.value = false
  glance.value = data
  failed.value = !data
})
</script>

<template>
  <UPopover v-model:open="open" :reference="reference ?? undefined" :content="{ side: 'right', align: 'start', sideOffset: 8 }">
    <slot />
    <template #content>
      <div class="w-80 p-4 text-sm" data-testid="customer-peek">
        <ListSkeleton v-if="loading" :rows="2" />
        <p v-else-if="failed || !glance" class="text-muted">取不到这位顾客的资料</p>
        <template v-else>
          <div class="flex items-center gap-3">
            <span class="grid size-10 shrink-0 place-items-center rounded-full bg-tag-gray text-figure" aria-hidden="true">{{ (glance.customer.name || '散').slice(0, 1) }}</span>
            <div class="min-w-0">
              <p class="truncate text-base font-semibold">{{ glance.customer.name || '未识别' }}</p>
              <p class="truncate text-meta text-muted tabular-nums">
                {{ [glance.customer.phone, glance.balance ? `储值卡余 ${yuan(money(glance.balance))}` : ''].filter(Boolean).join(' · ') || '没留手机号' }}
              </p>
            </div>
          </div>
          <div class="mt-3 space-y-1 border-t border-default pt-3">
            <template v-if="glance.last">
              <p class="text-xs text-dimmed">上次 · {{ monthDay(glance.last.date) }}<template v-if="glance.last.staff"> · {{ glance.last.staff }}</template></p>
              <p>{{ glance.last.items }}<template v-if="glance.last.result"> · {{ glance.last.result }}</template></p>
            </template>
            <p v-else class="text-muted">第一次来</p>
            <p v-if="glance.formula" class="text-meta text-muted">配方 {{ glance.formula.formula }}</p>
            <p v-if="glance.customer.preferences" class="said text-muted">{{ glance.customer.preferences }}</p>
            <p v-if="glance.counts" class="text-meta text-muted">次卡剩 <span class="text-default tabular-nums">{{ glance.counts }}</span> 次</p>
          </div>
          <div class="mt-3 flex justify-end border-t border-default pt-3">
            <NuxtLink :to="`/customers/${glance.customer.id}`" class="tap-area inline-flex items-center text-sm hover:underline" @click="open = false">打开档案<UIcon name="i-lucide-chevron-right" class="size-4" aria-hidden="true" /></NuxtLink>
          </div>
        </template>
      </div>
    </template>
  </UPopover>
</template>
