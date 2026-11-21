<script setup lang="ts">
import { zh_cn } from '@nuxt/ui/locale'
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

// 侧栏分组：组名只在组里有可见项时出现。
const navGroups = computed(() => {
  const groups: { label: string, items: typeof navItems }[] = []
  for (const item of visibleNav.value) {
    const last = groups.at(-1)
    if (last && last.label === item.group) last.items.push(item)
    else groups.push({ label: item.group, items: [item] })
  }
  return groups
})

// 首页切换是单选组：方向键在选项间移动并选中，整组只占一个 Tab 位。
function moveHome(event: KeyboardEvent, step: number) {
  const options = homeSwitcherOptions.value
  const index = options.findIndex(option => option.value === homeSwitcherSelected.value)
  const next = options[(index + step + options.length) % options.length]
  if (!next) return
  homeSwitcherSelected.value = next.value
  const group = (event.currentTarget as HTMLElement).parentElement
  void nextTick(() => group?.querySelector<HTMLElement>('[aria-checked="true"]')?.focus())
}

function isActive(to: string) {
  return to === '/' ? route.path === '/' : route.path.startsWith(to)
}

const pageIcon = computed(() => {
  if (route.path === '/drafts') return DRAFTS_ICON
  return navItems.find(item => isActive(item.to))?.icon ?? ''
})

// 侧栏顶上的门店名，第一个字做标志。
const storeName = ref('')

async function loadStore() {
  const { data } = await useApi().POST('/api/ops/get_store', { body: {} })
  storeName.value = data?.name ?? ''
}

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
  if (staffId) {
    void loadDrafts()
    void loadStore()
  }
}, { immediate: true })

// 别人起草的也要让我看到：页面开着时每分钟刷新一次待确认计数。
let draftTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  draftTimer = setInterval(() => {
    if (me.value && document.visibilityState === 'visible') void loadDrafts({ quiet: true })
  }, 60_000)
})
onBeforeUnmount(() => clearInterval(draftTimer))

const searchGroups = computed(() => {
  const groups: {
    id: string
    label: string
    ignoreFilter?: boolean
    items: { label: string, icon?: string, ui?: { itemLeadingIcon: string }, onSelect: () => void }[]
  }[] = [{
    id: 'pages',
    label: '页面',
    items: [{ label: '待确认', icon: DRAFTS_ICON, to: '/drafts' }, ...visibleNav.value].map(item => ({
      label: item.label,
      icon: item.icon,
      ui: { itemLeadingIcon: 'text-muted' },
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
  <UApp :locale="zh_cn" :toaster="{ position: 'bottom-right' }">
    <NuxtPage v-if="isLogin" />
    <!-- 外壳照原型 J：暖灰侧栏（门店名、搜索或吩咐 AI、待确认、分组导航、本人），一行顶栏（面包屑、首页切换）。 -->
    <UDashboardGroup v-else unit="rem" storage-key="salon-dashboard">
      <UDashboardSidebar
        collapsible
        :default-size="15"
        :min-size="13"
        :max-size="20"
        :collapsed-size="4.5"
        class="bg-side"
        :ui="{ header: 'h-auto px-2 pt-2 pb-1.5', body: 'gap-px px-2 py-0 text-muted', footer: 'px-2 pb-3 pt-2' }"
      >
        <template #header="{ collapsed }">
          <div class="group flex min-h-7 w-full min-w-0 items-center gap-2 rounded-md px-2 py-1.5 font-semibold" :class="collapsed ? 'flex-col px-0' : ''">
            <span class="grid size-[22px] shrink-0 place-items-center rounded-sm bg-inverted text-xs text-white" aria-hidden="true">{{ storeName.slice(0, 1) || '店' }}</span>
            <span v-if="!collapsed" data-testid="store-name" class="truncate">{{ storeName }}</span>
            <UDashboardSidebarCollapse
              data-testid="sidebar-collapse"
              :aria-label="collapsed ? '展开侧栏' : '收起侧栏'"
              size="xs"
              :class="collapsed ? '' : 'ms-auto opacity-0 group-hover:opacity-100 focus-visible:opacity-100 any-pointer-coarse:opacity-100'"
            />
          </div>
        </template>

        <template #default="{ collapsed }">
          <button
            type="button"
            class="side-item text-default"
            :class="collapsed ? 'justify-center' : ''"
            :aria-label="collapsed ? '搜索或吩咐 AI' : undefined"
            data-testid="search-button"
            @click="searchOpen = true"
          >
            <UIcon name="i-lucide-sparkles" class="side-icon" aria-hidden="true" />
            <template v-if="!collapsed">
              <span class="truncate">搜索或吩咐 AI</span>
              <kbd class="ms-auto font-mono text-2xs text-dimmed">Ctrl K</kbd>
            </template>
          </button>
          <NuxtLink
            to="/drafts"
            data-testid="drafts-nav"
            class="side-item"
            :class="[isActive('/drafts') ? 'side-on' : '', collapsed ? 'justify-center' : '']"
            :aria-label="collapsed ? '待确认' : undefined"
          >
            <UIcon :name="DRAFTS_ICON" class="side-icon" aria-hidden="true" />
            <template v-if="!collapsed">
              <span>待确认</span>
              <span v-if="pendingDrafts.length" class="ms-auto text-xs text-dimmed tabular-nums">{{ pendingDrafts.length }}</span>
            </template>
          </NuxtLink>

          <nav data-testid="sidebar-nav" aria-label="主导航" class="flex flex-col gap-px">
            <template v-for="group in navGroups" :key="group.label">
              <p v-if="!collapsed" class="px-2 pt-3.5 pb-1 text-xs font-medium text-dimmed">{{ group.label }}</p>
              <div v-else class="mx-2 mt-2 mb-1 border-t border-default" />
              <NuxtLink
                v-for="item in group.items"
                :key="item.to"
                :to="item.to"
                class="side-item"
                :class="[isActive(item.to) ? 'side-on' : '', collapsed ? 'justify-center' : '']"
                :aria-label="collapsed ? item.label : undefined"
                :title="collapsed ? item.label : undefined"
              >
                <UIcon :name="item.icon" class="side-icon" aria-hidden="true" />
                <span v-if="!collapsed" data-nav-label>{{ item.label }}</span>
              </NuxtLink>
            </template>
          </nav>
        </template>

        <template #footer="{ collapsed }">
          <PersonalMenu :collapsed="collapsed" />
        </template>
      </UDashboardSidebar>

      <UDashboardPanel data-testid="main-panel" class="bg-page" :ui="{ body: 'p-6' }">
        <template #header>
          <UDashboardNavbar :ui="{ root: 'h-[45px] border-b-0 px-3 sm:px-3' }">
            <template #left>
              <h1 class="flex min-w-0 items-center gap-1 rounded px-1.5 py-0.5 text-sm font-normal">
                <UIcon v-if="pageIcon" :name="pageIcon" class="size-4 shrink-0 text-muted" aria-hidden="true" />
                <span class="truncate">{{ pageTitle }}</span>
              </h1>
            </template>
            <template #right>
              <div id="navbar-actions" class="flex items-center gap-1 text-muted">
                <div
                  v-if="homeSwitcherActive"
                  role="radiogroup"
                  aria-label="选择首页"
                  data-testid="home-switcher"
                  class="inline-flex rounded-md bg-elevated p-0.5"
                >
                  <button
                    v-for="option in homeSwitcherOptions"
                    :key="option.value"
                    type="button"
                    role="radio"
                    :aria-checked="homeSwitcherSelected === option.value"
                    :aria-label="option.label"
                    :tabindex="homeSwitcherSelected === option.value ? 0 : -1"
                    class="inline-flex min-h-6 items-center rounded px-2.5 text-sm"
                    :class="homeSwitcherSelected === option.value ? 'bg-white text-default shadow-raised' : 'text-muted'"
                    @click="homeSwitcherSelected = option.value"
                    @keydown.left.prevent="moveHome($event, -1)"
                    @keydown.right.prevent="moveHome($event, 1)"
                    @keydown.up.prevent="moveHome($event, -1)"
                    @keydown.down.prevent="moveHome($event, 1)"
                  >
                    {{ option.short }}
                  </button>
                </div>
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
        placeholder="说一句要做的事，或搜索顾客、页面…"
        :ui="{ label: 'font-medium text-muted' }"
      />

      <AiPanel />
    </UDashboardGroup>
  </UApp>
</template>

<style scoped>
.side-item {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 28px;
  padding: 4px 8px;
  border-radius: 6px;
  white-space: nowrap;
  font-size: var(--text-sm);
  cursor: pointer;
}
.side-item:hover {
  background: var(--color-hover);
}
.side-on {
  background: var(--color-selected);
  color: var(--color-ink);
  font-weight: 500;
}
.side-icon {
  width: 18px;
  height: 18px;
  flex: none;
  color: var(--ui-text-muted);
}
.side-on .side-icon {
  color: var(--ui-text);
}
@media (pointer: coarse) {
  .side-item {
    min-height: 44px;
  }
}
</style>
