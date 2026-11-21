<script setup lang="ts">
import type { components } from '~/utils/api'

type Customer = components['schemas']['Customer']

const route = useRoute()
const { me, can } = useMe()
const { active: homeSwitcherActive, options: homeSwitcherOptions, selected: homeSwitcherSelected } = useHomeSwitcher()
const isLogin = computed(() => route.path === '/login')
const pageTitle = computed(() => String(route.meta.title || '首页'))

const { pending: pendingDrafts, load: loadDrafts, reset: resetDrafts } = useDrafts()
const { ask: askAi, reset: resetAi } = useAiChat()

const visibleNav = computed(() => navItems.filter(item => !item.permission || can(item.permission)))

const menuItems = computed(() => {
  const items: ({ type: 'label', label: string } | { label: string, icon: string, to: string })[] = []
  let currentGroup = ''
  for (const item of visibleNav.value) {
    if (item.group && item.group !== currentGroup) {
      items.push({ type: 'label', label: item.group })
      currentGroup = item.group
    } else if (!item.group) {
      currentGroup = ''
    }
    items.push({ label: item.label, icon: item.icon, to: item.to })
  }
  return items
})

// 全局搜索：页面直接来自用户可见的导航；顾客只在有查看权限时按输入异步查询。
const searchOpen = ref(false)
const searchTerm = ref('')
const customerResults = ref<Customer[]>([])
const customerGuard = useRequestGuard()
let searchTimer: ReturnType<typeof setTimeout> | undefined

async function searchCustomers(query: string) {
  const current = customerGuard.next()
  const { data } = await useApi().POST('/api/ops/search_customers', {
    body: { query, include_unidentified: false, limit: 10 }
  })
  if (!current()) return
  customerResults.value = data?.items ?? []
}

watch(searchTerm, (value) => {
  clearTimeout(searchTimer)
  const query = value.trim()
  // 换了词就清掉上一词的顾客结果，免得快速回车选中旧结果。
  customerGuard.invalidate()
  customerResults.value = []
  if (!can('customer.view') || !query) return
  searchTimer = setTimeout(() => searchCustomers(query), 300)
})

onBeforeUnmount(() => clearTimeout(searchTimer))

// 换人登录时清掉上一个人的搜索词、顾客结果、AI 对话和草稿，再取新登录者的草稿。
watch(() => me.value?.staff.id, (staffId) => {
  clearTimeout(searchTimer)
  customerGuard.invalidate()
  searchTerm.value = ''
  customerResults.value = []
  searchOpen.value = false
  resetAi()
  resetDrafts()
  if (staffId) void loadDrafts()
}, { immediate: true })

// 别人起草的也要让我看到：页面开着时每分钟刷新一次待确认计数。
let draftTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  draftTimer = setInterval(() => {
    if (me.value && document.visibilityState === 'visible') void loadDrafts({ quiet: true })
  }, 60_000)
})
onBeforeUnmount(() => clearInterval(draftTimer))

const draftsNav = computed(() => [{
  label: '待确认',
  icon: 'i-lucide-inbox',
  to: '/drafts',
  badge: pendingDrafts.value.length
    ? { label: String(pendingDrafts.value.length), color: 'warning' as const, variant: 'subtle' as const }
    : undefined
}])

const searchGroups = computed(() => {
  const groups: {
    id: string
    label: string
    ignoreFilter?: boolean
    items: { label: string, icon: string, ui?: { itemLeadingIcon: string }, onSelect: () => void }[]
  }[] = [{
    id: 'pages',
    label: '页面',
    items: [{ label: '待确认', icon: 'i-lucide-inbox', to: '/drafts' }, ...visibleNav.value].map(item => ({
      label: item.label,
      icon: item.icon,
      onSelect: () => navigateTo(item.to)
    }))
  }]
  if (can('customer.view') && searchTerm.value.trim()) {
    groups.push({
      id: 'customers',
      label: '顾客',
      // 后端已按手机号、后四位和姓名查好，不再让组件按标签二次过滤。
      ignoreFilter: true,
      items: customerResults.value.map(customer => ({
        label: `${customer.name || '未识别顾客'}${customer.phone ? ` · ${customer.phone}` : ''}`,
        icon: 'i-lucide-contact',
        onSelect: () => navigateTo({ path: '/customers', query: { id: customer.id } })
      }))
    })
  }
  const said = searchTerm.value.trim()
  if (said) {
    // 放在最后：输入页面名或顾客名时回车仍然打开搜索结果，说一整句话时只剩这一项。
    groups.push({
      id: 'ai',
      label: 'AI',
      ignoreFilter: true,
      items: [{
        label: `吩咐 AI：${said}`,
        icon: 'i-lucide-sparkles',
        ui: { itemLeadingIcon: 'text-ai!' },
        onSelect: () => {
          searchOpen.value = false
          searchTerm.value = ''
          askAi(said)
        }
      }]
    })
  }
  return groups
})
</script>

<template>
  <UApp :toaster="{ position: 'bottom-right' }">
    <NuxtPage v-if="isLogin" />
    <!-- 侧栏按 rem 固定宽度，窄屏不被按比例挤窄；换存储键，丢掉旧的百分比宽度。 -->
    <UDashboardGroup v-else unit="rem" storage-key="salon-dashboard">
      <UDashboardSidebar collapsible :default-size="15" :min-size="13" :max-size="20" :collapsed-size="6" class="bg-muted">
        <template #header="{ collapsed }">
          <div class="flex w-full min-w-0 items-center" :class="collapsed ? 'justify-center gap-1' : 'gap-2'">
            <UIcon name="i-lucide-store" class="size-5 shrink-0" />
            <span v-if="!collapsed" class="truncate font-semibold">门店</span>
            <UDashboardSidebarCollapse
              data-testid="sidebar-collapse"
              :aria-label="collapsed ? '展开侧栏' : '收起侧栏'"
              :class="collapsed ? '' : 'ms-auto'"
            />
          </div>
        </template>

        <template #default="{ collapsed }">
          <UDashboardSearchButton
            :collapsed="collapsed"
            label="搜索或吩咐 AI"
            :ui="{ trailing: 'lg:hidden 2xl:flex' }"
            class="w-full"
          />
          <UNavigationMenu
            :items="draftsNav"
            orientation="vertical"
            :collapsed="collapsed"
            tooltip
            data-testid="drafts-nav"
          />
          <UNavigationMenu
            :items="menuItems"
            orientation="vertical"
            :collapsed="collapsed"
            tooltip
            :ui="{ label: 'font-medium text-muted' }"
            data-testid="sidebar-nav"
          />
        </template>

        <template #footer="{ collapsed }">
          <PersonalMenu :collapsed="collapsed" />
        </template>
      </UDashboardSidebar>

      <UDashboardPanel data-testid="main-panel" :ui="{ body: 'p-6' }">
        <template #header>
          <UDashboardNavbar>
            <template #left>
              <UDashboardSidebarToggle />
              <div class="flex min-w-0 items-center gap-1.5 text-sm">
                <span class="truncate text-muted">门店</span>
                <span class="text-muted">/</span>
                <h1 class="truncate text-sm font-medium">{{ pageTitle }}</h1>
              </div>
            </template>
            <template #right>
              <div id="navbar-actions" class="flex items-center gap-1.5">
                <USelect
                  v-if="homeSwitcherActive"
                  v-model="homeSwitcherSelected"
                  :items="homeSwitcherOptions"
                  class="w-32"
                  aria-label="选择首页"
                  data-testid="home-switcher"
                />
              </div>
            </template>
          </UDashboardNavbar>
        </template>
        <template #body>
          <NuxtPage />
        </template>
      </UDashboardPanel>

      <UDashboardSearch
        v-model:open="searchOpen"
        v-model:search-term="searchTerm"
        :groups="searchGroups"
        :color-mode="false"
        title="搜索或吩咐 AI"
        placeholder="搜索页面、顾客，或吩咐 AI"
        :ui="{ label: 'font-medium text-muted' }"
      />

      <AiPanel />
    </UDashboardGroup>
  </UApp>
</template>
