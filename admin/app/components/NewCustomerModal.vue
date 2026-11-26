<script setup lang="ts">
import type { components } from '~/utils/api'

// 新顾客（样稿 L6）：只问手机号和怎么称呼，怎么来的点一下（选填），发质、喜好进了档案再补。
// 按钮直接通往下一步：能开单的人主按钮是「建好，开单」，旁边「只建档」；不能开单的只有「建档」。
// 顾客页的「新建顾客」和全局搜索里「用这个号新建」共用；手机号已有档案时打开那位，不重建。
const open = defineModel<boolean>('open', { required: true })
const props = defineProps<{ phone?: string }>()

const api = useApi()
const toast = useToast()
const { can } = useMe()
const canOrder = computed(() => can('order.create'))

const phoneInput = ref('')
const name = ref('')
const source = ref('')
const sources = ref<components['schemas']['CustomerSource'][]>([])
const saving = ref(false)
const fromSearch = computed(() => !!props.phone)

// 关了又马上打开时，上一次读来源的回包晚到也不该再弹提示、改列表。
const sourcesGuard = useRequestGuard()

watch(open, async (value) => {
  if (!value) return
  phoneInput.value = props.phone ?? ''
  name.value = ''
  source.value = ''
  const current = sourcesGuard.next()
  const res = await api.POST('/api/ops/list_customer_sources', { body: {} })
  if (!current() || !open.value) return
  if (!res.data) toast.add({ title: '来源没有读出来', description: '先不选来源，进档案再补', color: 'warning' })
  sources.value = res.data?.items.filter(s => s.active) ?? []
}, { immediate: true })

function pickSource(id: string) {
  source.value = source.value === id ? '' : id
}

async function save(thenOrder: boolean) {
  if (saving.value) return
  saving.value = true
  try {
    const res = await api.POST('/api/ops/create_customer', {
      body: {
        phone: phoneInput.value.replace(/[\s-]/g, '') || undefined,
        name: name.value.trim() || undefined,
        source_id: source.value || undefined
      }
    })
    if (!res.data) {
      toast.add({ title: errorText(res.error), color: 'error' })
      return
    }
    // 已有档案时后端不改原档案，填的称呼、来源没用上，要说清楚。
    if (res.data.existed) toast.add({ title: '这个手机号已有档案', description: '打开的是原来那位，称呼、来源没有改', color: 'info' })
    open.value = false
    const id = res.data.customer.id
    await navigateTo(thenOrder ? { path: '/cashier', query: { open: 'customer', customer: id } } : `/customers/${id}`)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <UModal v-model:open="open" title="新顾客" :description="fromSearch ? '手机号是从搜索框带过来的' : undefined">
    <template #body>
      <!-- 输完按回车就是主按钮。 -->
      <form class="space-y-4" @submit.prevent="save(canOrder)">
        <UFormField label="手机号">
          <UInput v-model="phoneInput" type="tel" class="w-full" :ui="{ base: 'h-12 font-serif text-figure! tabular-nums' }" />
        </UFormField>
        <UFormField label="怎么称呼">
          <UInput v-model="name" placeholder="比如：陈小姐" class="w-full" :autofocus="fromSearch" />
        </UFormField>
        <div v-if="sources.length">
          <p class="mb-2 text-sm text-muted">怎么来的 <span class="text-dimmed">选填</span></p>
          <div class="flex flex-wrap gap-2" role="group" aria-label="怎么来的">
            <button
              v-for="item in sources"
              :key="item.id"
              type="button"
              :aria-pressed="source === item.id"
              class="h-7 rounded-full border px-3 text-sm transition-colors"
              :class="source === item.id ? 'border-transparent bg-inverted text-inverted' : 'border-default hover:bg-hover'"
              @click="pickSource(item.id)"
            >
              {{ item.name }}
            </button>
          </div>
        </div>
        <button type="submit" class="hidden" aria-hidden="true" tabindex="-1" />
      </form>
    </template>
    <template #footer>
      <!-- 能开单时描边的那个按钮就是「只建档」：不想建就点右上角或按 Esc 关掉。 -->
      <ModalActions :cancel-label="canOrder ? '只建档' : '取消'" @cancel="canOrder ? save(false) : open = false">
        <template #leading>
          <span class="text-meta text-muted">发质、喜好进档案再记</span>
        </template>
        <UButton :loading="saving" @click="save(canOrder)">{{ canOrder ? '建好，开单' : '建档' }}</UButton>
      </ModalActions>
    </template>
  </UModal>
</template>
