<script setup lang="ts">
import type { components } from '~/utils/api'

type Customer = components['schemas']['Customer']

const route = useRoute()
const { me, can } = useMe()
const { active: homeSwitcherActive, options: homeSwitcherOptions, selected: homeSwitcherSelected } = useHomeSwitcher()
const isLogin = computed(() => route.path === '/login')
const pageTitle = computed(() => String(route.meta.title || '首页'))

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
  if (!can('customer.view') || !query) {
    customerGuard.invalidate()
    customerResults.value = []
    return
  }
  searchTimer = setTimeout(() => searchCustomers(query), 300)
})

onBeforeUnmount(() => clearTimeout(searchTimer))

// 换人登录时清掉上一个人的搜索词和顾客结果。
watch(() => me.value?.staff.id, () => {
  clearTimeout(searchTimer)
  customerGuard.invalidate()
  searchTerm.value = ''
  customerResults.value = []
  searchOpen.value = false
})

const searchGroups = computed(() => {
  const groups: {
    id: string
    label: string
    ignoreFilter?: boolean
    items: { label: string, icon: string, onSelect: () => void }[]
  }[] = [{
    id: 'pages',
    label: '页面',
    items: visibleNav.value.map(item => ({
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
  return groups
})
</script>

<template>
  <UApp :toaster="{ position: 'bottom-right' }">
    <NuxtPage v-if="isLogin" />
    <UDashboardGroup v-else>
      <UDashboardSidebar collapsible :collapsed-size="9" class="bg-muted">
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
            label="搜索"
            :ui="{ trailing: 'lg:hidden xl:flex' }"
            class="w-full"
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
        title="搜索"
        placeholder="搜索页面或顾客"
        :ui="{ label: 'font-medium text-muted' }"
      />
    </UDashboardGroup>
  </UApp>
</template>
