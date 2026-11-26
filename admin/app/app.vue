<script setup lang="ts">
import { zh_cn } from '@nuxt/ui/locale'

const route = useRoute()
const { me, can } = useMe()
const isLogin = computed(() => route.path === '/login')
const sidebarCollapsed = ref(false)
// 详情页（顾客档案）用自己取到的名字做顶栏标题，换页时清掉。
const pageCrumbTitle = useState('page-crumb-title', () => '')
watch(() => route.path, () => {
  pageCrumbTitle.value = ''
})
const pageTitle = computed(() => pageCrumbTitle.value || String(route.meta.title || '首页'))

const { pending: pendingDrafts, load: loadDrafts, reset: resetDrafts } = useDrafts()
const { reset: resetAi, open: aiOpen } = useAiChat()

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

// 配置类分组默认收起（DESIGN.md 侧栏）：当前页所在的组总是展开；用户点开、收起的选择记在本机。
const FOLD_KEY = 'salon-nav-folded'
const folded = ref<string[]>(['项目与卡', '设置'])
onMounted(() => {
  try {
    const saved = JSON.parse(localStorage.getItem(FOLD_KEY) ?? 'null')
    if (Array.isArray(saved)) folded.value = saved.filter(v => typeof v === 'string')
  } catch { /* 本机记录坏了就用默认 */ }
})

// 当前页所在的分组总是展开，收不起来。
function holdsActive(group: { label: string, items: typeof navItems }) {
  return group.items.some(item => isActive(item.to))
}

function groupOpen(group: { label: string, items: typeof navItems }) {
  return !folded.value.includes(group.label) || holdsActive(group)
}

function toggleGroup(group: { label: string, items: typeof navItems }) {
  if (holdsActive(group)) return
  const rest = folded.value.filter(label => label !== group.label)
  folded.value = groupOpen(group) ? [...rest, group.label] : rest
  localStorage.setItem(FOLD_KEY, JSON.stringify(folded.value))
}

function isActive(to: string) {
  return to === '/' ? route.path === '/' : route.path.startsWith(to)
}

// 页面骨架（DESIGN.md）：首页有自己的问候，其余页面正文顶上统一一个大标题；宽度按页面元数据分两档。
const showPageHeader = computed(() => route.path !== '/' && !route.meta.bare)
// 底部留出右下角 AI 按钮的位置，页面滚到底时最后一行不被压住；工作台页不另留：收银靠面板本身的底部留白，预约格子在自己的滚动区里留。
const pageWidth = computed(() => [
  route.meta.width === 'narrow' ? 'mx-auto w-full max-w-[800px]' : 'w-full',
  route.meta.workbench ? '' : 'pb-16'
])
// 大屏两侧留白：宽表和工作台（预约日历、收银）只留 48px，把宽度让给列。
// 工作台页顶距收到 16：首屏要留给排班列和收银单。
const panelBody = computed(() => `px-4 pt-5 pb-16 sm:px-12 sm:pb-24 ${route.meta.workbench ? 'sm:pt-4' : 'sm:pt-10'} ${route.meta.workbench || route.meta.width === 'wide' ? '' : 'xl:px-24'}`)
const pageDescription = computed(() => String(route.meta.description || ''))

const pageIcon = computed(() => {
  if (route.path === '/drafts') return DRAFTS_ICON
  return navItems.find(item => isActive(item.to))?.icon ?? ''
})
// 二级页（各张报表、顾客档案、员工详情）在顶栏前面放上一级的链接，点了回入口。
const parentCrumb = computed(() => {
  if (route.path.startsWith('/reports/')) return { to: '/reports', label: '报表' }
  if (route.path.startsWith('/customers/')) return { to: '/customers', label: '顾客' }
  if (route.path.startsWith('/staff/')) return { to: '/staff', label: '员工' }
  return null
})

// 侧栏顶上的门店名，第一个字做标志；设置页改名后同一份状态跟着变。
const storeName = useState('store-name', () => '')

async function loadStore() {
  const { data } = await useApi().POST('/api/ops/get_store', { body: {} })
  storeName.value = data?.name ?? ''
}

// 全局搜索（GlobalSearch）：侧栏「搜索」和 Ctrl K 打开。
const searchOpen = ref(false)

// 换人登录时清掉上一个人的 AI 对话和草稿，再取新登录者的草稿；搜索框自己清。
watch(() => me.value?.staff.id, (staffId) => {
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
</script>

<template>
  <UApp :locale="zh_cn" :toaster="{ position: 'bottom-center' }">
    <NuxtPage v-if="isLogin" />
    <!-- 外壳照原型 J：暖灰侧栏（门店名、搜索、待确认、分组导航、本人），一行顶栏（面包屑、首页切换）。 -->
    <UDashboardGroup v-else unit="rem" storage-key="salon-dashboard">
      <UDashboardSidebar
        v-model:collapsed="sidebarCollapsed"
        collapsible
        :default-size="15"
        :min-size="13"
        :max-size="20"
        :collapsed-size="4.5"
        class="bg-side"
        :ui="{ header: 'h-auto px-2 pt-2 pb-2', body: 'side-scroll gap-px px-2 py-0 text-muted', footer: 'px-2 pb-3 pt-2' }"
      >
        <template #header="{ collapsed }">
          <div class="flex min-h-7 w-full min-w-0 items-center gap-2 rounded-md px-2 py-2 font-semibold" :class="collapsed ? 'justify-center px-0' : ''">
            <span class="grid size-[22px] shrink-0 place-items-center rounded-md bg-inverted text-xs text-white" aria-hidden="true">{{ storeName.slice(0, 1) || '店' }}</span>
            <span v-if="!collapsed" data-testid="store-name" class="truncate">{{ storeName }}</span>
          </div>
        </template>

        <template #default="{ collapsed }">
          <button
            type="button"
            class="side-item text-default"
            :class="collapsed ? 'justify-center' : ''"
            :aria-label="collapsed ? '搜索' : undefined"
            data-testid="search-button"
            @click="searchOpen = true"
          >
            <UIcon name="i-lucide-search" class="side-icon" aria-hidden="true" />
            <template v-if="!collapsed">
              <span class="truncate">搜索</span>
              <kbd class="ms-auto font-mono text-xs text-muted">Ctrl K</kbd>
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
              <span v-if="pendingDrafts.length" class="ms-auto text-xs text-muted tabular-nums">{{ pendingDrafts.length }}</span>
            </template>
          </NuxtLink>

          <nav data-testid="sidebar-nav" aria-label="主导航" class="flex flex-col gap-px">
            <template v-for="group in navGroups" :key="group.label">
              <button
                v-if="!collapsed"
                type="button"
                class="group/fold flex items-center gap-1 rounded-md px-2 pt-4 pb-1 text-start text-xs font-medium text-muted hover:text-default"
                :class="holdsActive(group) ? 'cursor-default' : ''"
                :aria-expanded="groupOpen(group)"
                :aria-disabled="holdsActive(group) || undefined"
                data-testid="nav-group"
                @click="toggleGroup(group)"
              >
                {{ group.label }}
                <UIcon
                  v-if="!holdsActive(group)"
                  name="i-lucide-chevron-right"
                  class="size-3.5 transition-transform"
                  :class="groupOpen(group) ? 'rotate-90 opacity-0 group-hover/fold:opacity-100 group-focus-visible/fold:opacity-100 any-pointer-coarse:opacity-100' : ''"
                  aria-hidden="true"
                />
              </button>
              <div v-else class="mx-2 mt-2 mb-1 border-t border-default" />
              <!-- 收成窄条时照样遵守分组的收起：收起的组只留一个按钮，点开从右边列出组里的页面。 -->
              <UDropdownMenu
                v-if="collapsed && !groupOpen(group)"
                :items="group.items.map(item => ({ label: item.label, icon: item.icon, to: item.to }))"
                :content="{ side: 'right', align: 'start' }"
              >
                <button type="button" class="side-item justify-center" :aria-label="`${group.label}（已收起）`" :title="group.label" data-testid="nav-group-rail">
                  <UIcon name="i-lucide-ellipsis" class="side-icon" aria-hidden="true" />
                </button>
              </UDropdownMenu>
              <NuxtLink
                v-for="item in group.items"
                v-show="groupOpen(group)"
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

      <UDashboardPanel data-testid="main-panel" class="bg-page" :ui="{ body: panelBody }">
        <template #header>
          <UDashboardNavbar :ui="{ root: 'h-[45px] border-b-0 px-3 sm:px-3' }">
            <template #left>
              <!-- 收起、展开侧栏的按钮固定在顶栏最左边：两种状态下位置和图标都不变（DESIGN.md 外壳）。 -->
              <UDashboardSidebarCollapse
                data-testid="sidebar-collapse"
                icon="i-lucide-panel-left"
                :aria-label="sidebarCollapsed ? '展开侧栏' : '收起侧栏'"
                size="sm"
                class="me-0.5 shrink-0 text-muted"
              />
              <p class="flex min-w-0 items-center gap-1 rounded-md px-2 py-0.5 text-sm text-muted">
                <UIcon v-if="pageIcon" :name="pageIcon" class="size-4 shrink-0" aria-hidden="true" />
                <template v-if="parentCrumb">
                  <NuxtLink :to="parentCrumb.to" class="inline-flex min-h-6 items-center rounded-md px-1 hover:text-default hover:underline" data-testid="parent-crumb">{{ parentCrumb.label }}</NuxtLink>
                  <span class="text-dimmed" aria-hidden="true">/</span>
                </template>
                <span class="truncate">{{ pageTitle }}</span>
              </p>
            </template>
            <template #right>
              <div id="navbar-actions" class="flex items-center gap-1 text-muted" />
            </template>
          </UDashboardNavbar>
        </template>
        <template #body>
          <main :class="pageWidth">
            <!-- 页头：阅读页上画下字；工作台页（收银、预约）插画和标题并排一行，省出约 80px 给下面的列。 -->
            <!-- 右边的 #title-actions 给 <PageActions at="title"> 用：主操作贴着标题行右下角，窄屏折到说明下面。 -->
            <header v-if="showPageHeader" class="mb-5 flex flex-wrap items-end gap-x-6 gap-y-3" data-testid="page-header">
              <div class="min-w-0 flex-1 basis-80">
                <div :class="route.meta.workbench ? 'flex items-center gap-2' : ''">
                  <SpotArt v-if="route.meta.art" :name="route.meta.art" class="-ml-1 w-[60px] shrink-0" :class="route.meta.workbench ? '' : 'mb-1'" data-testid="page-art" />
                  <h1 class="text-title font-semibold text-balance">{{ pageTitle }}</h1>
                </div>
                <p id="page-description" class="mt-2 text-muted text-pretty empty:hidden" data-testid="page-description"><span v-if="pageDescription">{{ pageDescription }}</span></p>
              </div>
              <div id="title-actions" class="flex items-center gap-2 empty:hidden" />
            </header>
            <NuxtPage />
          </main>
        </template>
      </UDashboardPanel>

      <GlobalSearch v-model:open="searchOpen" />

      <AiPanel />
      <!-- 问 AI 的常驻入口（照 Notion 右下角的 AI 按钮）：每一页、各种宽度都在右下角，面板打开时收起。
           页面底部都留了比它高的空白（正文 pb-16、面板 pb-16 / sm:pb-24，预约格子内部另留），滚到底时最后一个按钮能露出来。窄屏缩到 44px。 -->
      <button
        v-if="!aiOpen"
        type="button"
        aria-label="问 AI"
        title="问 AI"
        data-testid="ai-fab"
        class="fixed right-4 bottom-4 z-40 grid size-11 place-items-center rounded-full bg-white text-default shadow-lift transition-colors hover:bg-side sm:right-5 sm:bottom-5 sm:size-12"
        @click="aiOpen = true"
      >
        <AiMark class="size-7 sm:size-8" />
      </button>
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
