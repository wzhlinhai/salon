<script setup lang="ts">
import type { components } from '~/utils/api'

type Overview = components['schemas']['CustomerOverview']
type Staff = components['schemas']['Staff']
interface SearchItem {
  label: string
  description?: string
  icon?: string
  slot?: string
  hint?: string
  ui?: { itemLeadingIcon: string }
  onSelect: () => void
}

// 全局搜索（样稿 L5）：输一个字同时找顾客、员工、页面，最下面是能直接做的事。
// 顾客那行先写「上次做了什么」，不写编号；第一位顾客可以直接开单、约时间；搜不到的手机号，最后一行就是「用这个号新建」（样稿 L6）。
// 页面来自用户看得到的导航；顾客、员工、各个动作都只在有对应权限时出现。
const open = defineModel<boolean>('open', { required: true })

const api = useApi()
const toast = useToast()
const { me, can } = useMe()
const { ask: askAi } = useAiChat()
const term = ref('')
const customers = ref<Overview[]>([])
// 顾客结果是按哪个词查出来的：词换了、新结果没到之前，不算「搜不到」。
const searchedFor = ref('')
const searching = ref(false)
const staff = ref<Staff[]>([])
const guard = useRequestGuard()
let timer: ReturnType<typeof setTimeout> | undefined

const said = computed(() => term.value.trim())
const phoneDigits = computed(() => said.value.replace(/[\s-]/g, ''))
const looksLikePhone = computed(() => /^1\d{10}$/.test(phoneDigits.value))

async function searchCustomers(query: string) {
  const current = guard.next()
  searching.value = true
  const { data } = await api.POST('/api/ops/list_customer_overview', { body: { query, limit: 5 } })
  if (!current()) return
  searching.value = false
  // 没查成（断网、出错）不算「搜不到」：不出「用这个号新建」，说一声。
  if (!data) {
    toast.add({ title: '顾客没有查出来', description: '检查网络后再搜一次', color: 'error' })
    return
  }
  customers.value = data.items
  searchedFor.value = query
}

watch(term, () => {
  clearTimeout(timer)
  // 换了词就清掉上一词的顾客结果，免得快速回车选中旧结果。
  guard.invalidate()
  customers.value = []
  searchedFor.value = ''
  searching.value = false
  if (!can('customer.view') || !said.value) return
  searching.value = true
  // 手机号按数字查：粘贴进来的「138 1234 5678」也能找到人。
  const query = looksLikePhone.value ? phoneDigits.value : said.value
  timer = setTimeout(() => searchCustomers(query), 300)
})

// 员工不多，打开搜索时取一次，按名字在本机找。
watch(open, async (value) => {
  if (!value || !can('staff.manage') || staff.value.length) return
  const { data } = await api.POST('/api/ops/list_staff', { body: {} })
  staff.value = data?.staff ?? []
})

// 换人登录时清掉上一个人的搜索词、结果和员工名单。
watch(() => me.value?.staff.id, () => {
  clearTimeout(timer)
  guard.invalidate()
  term.value = ''
  customers.value = []
  searchedFor.value = ''
  staff.value = []
  open.value = false
})

onBeforeUnmount(() => clearTimeout(timer))

function go(to: Parameters<typeof navigateTo>[0]) {
  open.value = false
  term.value = ''
  void navigateTo(to)
}

function span(days: number) {
  if (days <= 1) return `${sinceText(days)}来过`
  return days < 14 ? `${days} 天前来过` : `${Math.floor(days / 7)} 周没来`
}

function customerLine(row: Overview) {
  const days = row.days_since_last_visit
  const last = row.last_visit_items.length
    ? `上次${row.last_visit_items.join('、')}${row.last_visit_staff.length ? ` ${row.last_visit_staff.map(s => s.name).join('、')}` : ''}`
    : ''
  return [row.customer.phone, days === null ? '还没来过' : span(days), last].filter(Boolean).join(' · ')
}

const customerName = (row: Overview) => row.customer.name || '未填写姓名'

// 新建顾客：手机号从搜索框带过去。
const createOpen = ref(false)
const createPhone = ref('')

const visiblePages = computed(() => [{ label: '待确认', icon: DRAFTS_ICON, to: '/drafts' }, ...navItems.filter(item => !item.permission || can(item.permission))])

const groups = computed(() => {
  const list: { id: string, label: string, ignoreFilter?: boolean, items: SearchItem[] }[] = []
  const pages = {
    id: 'pages',
    label: '页面',
    items: visiblePages.value.map(item => ({
      label: item.label,
      icon: item.icon,
      ui: { itemLeadingIcon: 'text-muted' },
      onSelect: () => go(item.to)
    }))
  }
  // 输入正好是一个页面名（「收银」）时页面排最前，回车就去那页，不被名字里带这两个字的顾客抢走。
  const pageFirst = visiblePages.value.some(item => item.label === said.value)
  if (pageFirst) list.push(pages)
  if (said.value && customers.value.length) {
    list.push({
      id: 'customers',
      label: '顾客',
      // 后端已按手机号、后四位和姓名查好，不再让组件按标签二次过滤。
      ignoreFilter: true,
      items: customers.value.map(row => ({
        label: customerName(row),
        description: customerLine(row),
        slot: 'person',
        hint: '打开档案',
        onSelect: () => go(`/customers/${row.customer.id}`)
      }))
    })
  }
  const people = said.value ? staff.value.filter(person => person.name.includes(said.value)).slice(0, 5) : []
  if (people.length) {
    list.push({
      id: 'staff',
      label: '员工',
      ignoreFilter: true,
      items: people.map(person => ({
        label: person.name,
        description: [person.position?.name, person.active ? '' : '已停用'].filter(Boolean).join(' · ') || undefined,
        slot: 'person',
        hint: '打开详情',
        onSelect: () => go(`/staff/${person.id}`)
      }))
    })
  }
  if (!pageFirst) list.push(pages)
  const actions: SearchItem[] = []
  const first = customers.value[0]
  if (said.value && first) {
    const name = customerName(first)
    if (can('order.create')) actions.push({ label: `给${name}开单`, icon: 'i-lucide-plus', onSelect: () => go({ path: '/cashier', query: { open: 'customer', customer: first.customer.id } }) })
    if (can('booking.manage')) actions.push({ label: `给${name}约时间`, icon: 'i-lucide-clock', onSelect: () => go({ path: '/bookings', query: { book: first.customer.id } }) })
  }
  const nobody = searchedFor.value === phoneDigits.value && !searching.value && !customers.value.length
  if (looksLikePhone.value && nobody && can('customer.edit')) {
    const phone = phoneDigits.value.replace(/^(\d{3})(\d{4})(\d{4})$/, '$1 $2 $3')
    actions.push({
      label: `用 ${phone} 新建顾客`,
      icon: 'i-lucide-user-plus',
      onSelect: () => {
        createPhone.value = phone
        open.value = false
        term.value = ''
        createOpen.value = true
      }
    })
  }
  if (actions.length) list.push({ id: 'actions', label: '直接做', ignoreFilter: true, items: actions })
  if (said.value) {
    // 放在最后：输入页面名或顾客名时回车仍然打开搜索结果，说一整句话时只剩这一项。
    list.push({
      id: 'ai',
      label: 'AI',
      ignoreFilter: true,
      items: [{
        label: `问 AI：${said.value}`,
        icon: 'i-lucide-corner-down-left',
        onSelect: () => {
          const words = said.value
          open.value = false
          term.value = ''
          askAi(words)
        }
      }]
    })
  }
  return list
})
</script>

<template>
  <UDashboardSearch
    v-model:open="open"
    v-model:search-term="term"
    :groups="groups"
    :color-mode="false"
    :loading="searching"
    title="搜索"
    placeholder="找顾客、员工、页面，或说一句话交给 AI"
    :ui="{ label: 'text-meta font-normal text-muted', viewport: 'divide-y-0', itemDescription: 'text-meta' }"
  >
    <template #close>
      <button type="button" class="shrink-0 rounded-md px-2 py-1 text-meta text-dimmed hover:text-muted" @click="open = false">Esc 关掉</button>
    </template>
    <template #person-leading="{ item }">
      <PersonAvatar :name="item.label ?? ''" />
    </template>
    <template #person-trailing="{ item }">
      <span class="hidden text-meta text-muted group-data-highlighted:inline">↵ {{ item.hint }}</span>
    </template>
  </UDashboardSearch>
  <NewCustomerModal v-model:open="createOpen" :phone="createPhone" />
</template>
