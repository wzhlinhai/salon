<script setup lang="ts">
import { MAX_MESSAGE } from '~/composables/useAiChat'

// 右侧滑出的「问 AI」（样稿 24）：照 Notion AI，人说的话是浅灰圆气泡靠右，AI 的回话不套气泡就是正文；
// 写操作只生成草稿，画成能拿在手里的小卡片，在这里或"待确认"里由人确认。
const { open, entries, sending, editing, input: text, loadingConversation, expanded, send, startNew, setExpanded, restoreExpanded } = useAiChat()
const { can } = useMe()
const scroller = ref<HTMLElement | null>(null)
const input = useTemplateRef<{ textareaRef?: HTMLTextAreaElement }>('input')

// 还没说话时列几件常用的事（照 Notion AI 面板的入口列表），按本人权限出。
// 要起草的（开单、预约、充值）点了先把开头填进输入框，上面一行提示要说什么，人补完再发；只是问数的直接发。
interface Quick { label: string, icon: string, prefix?: string, hint?: string, ask?: string }
const quick = computed<Quick[]>(() => ([
  can('order.create') && { label: '开单', icon: 'i-lucide-receipt-text', prefix: '开单：', hint: '说顾客、项目和做的人，比如「陈女士 剪发 小王做」' },
  can('booking.manage') && { label: '约个时间', icon: 'i-lucide-calendar-plus', prefix: '预约：', hint: '说顾客、时间和项目，比如「陈女士 明天下午 3 点 剪发」' },
  can('card.sell') && { label: '会员卡充值', icon: 'i-lucide-credit-card', prefix: '充值：', hint: '说顾客和金额，比如「陈女士 充 1000」' },
  can('report.view') && { label: '今天收了多少', icon: 'i-lucide-chart-column', ask: '今天实收多少？按付款方式分开说' }
] as (Quick | false)[]).filter((q): q is Quick => !!q))
const starter = ref<Quick | null>(null)

async function pick(q: Quick) {
  if (q.ask) {
    await send(q.ask)
    return
  }
  // 已经打了字就不覆盖，把开头加在前面。
  const prefix = q.prefix ?? ''
  const typed = text.value.trim()
  text.value = typed.startsWith(prefix) ? typed : prefix + typed
  starter.value = q
  await nextTick()
  const el = input.value?.textareaRef
  el?.focus()
  el?.setSelectionRange(text.value.length, text.value.length)
}

// 开头删掉了、或者发出去了，提示就收起。
watch(text, (value) => {
  if (starter.value && !value.startsWith(starter.value.prefix ?? '')) starter.value = null
})

async function submit() {
  const value = text.value
  if (!value.trim() || sending.value || loadingConversation.value) return
  text.value = ''
  starter.value = null
  await send(value)
}

function onKeydown(event: KeyboardEvent) {
  // 回车发送，Shift+回车换行；输入法选字时的回车不发送。
  // Safari 选字上屏的回车在 compositionend 之后才到，isComposing 已是 false，要再看 keyCode 229。
  if (event.key !== 'Enter' || event.shiftKey || event.isComposing || event.keyCode === 229) return
  event.preventDefault()
  void submit()
}

// 打开时把焦点放在输入框，不让对话框自动聚焦标题栏的按钮：从指令框回车打开时，回车会误按到"新对话"。
// 滑出动画还没完人就点了面板里的东西（比如历史下拉）时不抢焦点，不然刚打开的下拉会被关掉。
function focusInput() {
  const active = document.activeElement
  if (active?.closest('[data-testid="ai-history"]') || scroller.value?.closest('[role="dialog"]')?.contains(active)) return
  input.value?.textareaRef?.focus()
}

// 侧栏还是全屏记在本机，下次打开照上次的样子。
onMounted(() => {
  try {
    restoreExpanded()
  } catch {
    // 本机存不了就用侧栏。
  }
})

function toggleExpanded() {
  setExpanded(!expanded.value)
}

// 全屏时内容区不铺满，居中一栏，读起来不累。
const column = computed(() => expanded.value ? 'mx-auto w-full max-w-3xl' : '')
// 全屏又还没说话时照 Notion AI 首页：形象、大标题和输入卡一起落在页面正中，常用的事横排在输入卡下面。
const centered = computed(() => expanded.value && !entries.value.length && !loadingConversation.value)
// 输入卡和对话之间不画线（Notion 的输入卡自己浮在页面上）；居中时下面一半留给输入卡和常用的事。
const ui = computed(() => ({
  content: `${expanded.value ? 'max-w-none sm:max-w-none' : 'max-w-md'} divide-y-0`,
  wrapper: 'flex-1 min-w-0',
  close: 'static',
  // 顶栏左右和内容区一样留 16：形象和正文首字、关闭按钮的悬停底色和输入卡右边对齐（默认 sm 是 24）。
  header: 'px-4 sm:px-4',
  body: 'p-0 sm:p-0 flex flex-col',
  footer: `ai-chat border-t-0 px-4 pt-0 pb-4 sm:px-4 ${centered.value ? 'flex-1 items-start' : ''}`
}))

// AI 回话下面的复制按钮（照 Notion AI）：复制整段话，按钮变成勾一会儿再变回来。剪贴板用不了（非安全来源等）就什么也不做。
const copied = ref<number | null>(null)

async function copy(index: number, content: string) {
  try {
    await navigator.clipboard.writeText(content)
  } catch {
    return
  }
  copied.value = index
  setTimeout(() => {
    if (copied.value === index) copied.value = null
  }, 1500)
}

watch(() => [entries.value.length, sending.value], async () => {
  await nextTick()
  scroller.value?.scrollTo({ top: scroller.value.scrollHeight })
})
</script>

<template>
  <USlideover
    v-model:open="open"
    side="right"
    :content="{ onOpenAutoFocus: (event: Event) => event.preventDefault() }"
    :ui="ui"
    data-testid="ai-panel"
    @after:enter="focusInput"
  >
    <template #title>
      <span class="flex min-w-0 items-center gap-1"><AiMark class="size-7 shrink-0" /><span class="sr-only">问 AI</span><AiHistory /></span>
    </template>
    <template #actions>
      <UButton
        color="neutral"
        variant="ghost"
        size="sm"
        icon="i-lucide-message-circle-plus"
        aria-label="新对话"
        data-testid="ai-new"
        @click="startNew"
      />
      <UButton
        color="neutral"
        variant="ghost"
        size="sm"
        :icon="expanded ? 'i-lucide-minimize-2' : 'i-lucide-maximize-2'"
        :aria-label="expanded ? '退出全屏' : '全屏'"
        :aria-pressed="expanded"
        data-testid="ai-expand"
        @click="toggleExpanded"
      />
    </template>

    <template #body>
      <div ref="scroller" class="flex-1 overflow-y-auto p-4" data-testid="ai-messages">
        <div class="flex min-h-full flex-col gap-4" :class="column">
          <ListSkeleton v-if="loadingConversation" :rows="3" />
          <!-- 全屏时形象和大标题居中，挨着下面的输入卡。 -->
          <div v-else-if="centered" class="flex flex-1 flex-col items-center justify-end gap-6 pb-4 text-center">
            <span class="grid size-20 place-items-center rounded-full bg-white shadow-lift"><AiMark class="size-14" /></span>
            <div class="space-y-2">
              <p class="text-title font-semibold text-highlighted">店里的事，跟我说。</p>
              <p class="text-sm text-muted text-pretty">要改数据时 AI 先起草，由你确认。</p>
            </div>
          </div>
          <!-- 侧栏还没说话时照 Notion 的 AI 面板：形象和一句招呼落在底部，挨着输入框。 -->
          <div v-else-if="!entries.length" class="flex flex-1 flex-col justify-end gap-3 pb-2">
            <span class="grid size-16 place-items-center rounded-full bg-white shadow-lift"><AiMark class="size-11" /></span>
            <p class="text-base font-semibold text-highlighted">店里的事，跟我说。</p>
            <p class="text-sm text-muted text-pretty">要改数据时 AI 先起草，由你确认。</p>
            <!-- 正在改一张草稿时不列：这时发的话都算在改那张草稿上。 -->
            <ul v-if="quick.length && !editing" class="-mx-2" data-testid="ai-quick">
              <li v-for="q in quick" :key="q.label">
                <button type="button" class="flex w-full items-center gap-3 rounded-md px-2 py-2 text-start text-sm text-default hover:bg-hover focus-visible:bg-hover focus-visible:outline-2 focus-visible:outline-primary" :disabled="sending" @click="pick(q)">
                  <UIcon :name="q.icon" class="size-5 shrink-0 text-muted" aria-hidden="true" />
                  {{ q.label }}
                </button>
              </li>
            </ul>
          </div>
          <div v-for="(entry, index) in entries" :key="index" :data-role="entry.role" data-testid="ai-entry">
            <div v-if="entry.role === 'user'" class="ms-auto w-fit max-w-[85%] rounded-bubble bg-elevated px-4 py-2 text-sm whitespace-pre-wrap text-highlighted">
              <p v-if="entry.editing" class="mb-1 text-xs text-muted">修改：{{ entry.editing }}</p>
              {{ entry.content }}
            </div>
            <div v-else class="space-y-3">
              <p class="text-sm leading-relaxed whitespace-pre-wrap text-pretty" :class="entry.failed ? 'text-ink-red' : 'text-highlighted'">{{ entry.content }}</p>
              <DraftCard v-for="draft in entry.drafts ?? []" :key="draft.id" :draft="draft" compact />
              <UButton
                v-if="!entry.failed"
                color="neutral"
                variant="ghost"
                size="xs"
                :icon="copied === index ? 'i-lucide-check' : 'i-lucide-copy'"
                :aria-label="copied === index ? '已复制' : '复制'"
                class="text-muted"
                data-testid="ai-copy"
                @click="copy(index, entry.content)"
              />
            </div>
          </div>
          <p v-if="sending" class="flex items-center gap-2 text-sm text-muted" data-testid="ai-thinking">
            <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" />
            正在想…
          </p>
        </div>
      </div>
    </template>

    <template #footer>
      <div class="w-full space-y-2" :class="column">
        <div v-if="editing" class="flex items-center gap-2 text-xs text-muted" data-testid="ai-editing">
          <UIcon name="i-lucide-pencil" class="size-3.5 shrink-0" />
          <span class="truncate">修改：{{ editing.summary }}</span>
          <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-x" aria-label="不改了" class="ms-auto" @click="editing = null" />
        </div>
        <p v-else-if="starter" class="flex items-center gap-2 text-xs text-muted" data-testid="ai-starter">
          <UIcon :name="starter.icon" class="size-3.5 shrink-0" />
          <span class="text-pretty">{{ starter.hint }}</span>
        </p>
        <!-- 输入卡（照 Notion AI）：一张平放的纸，字写在上面，圆形的发送按钮在右下角，没写字时是浅灰的。 -->
        <div class="rounded-card bg-default shadow-paper transition-shadow focus-within:shadow-lift" data-testid="ai-composer">
          <UTextarea
            ref="input"
            v-model="text"
            variant="none"
            :rows="centered ? 2 : 1"
            autoresize
            :maxrows="8"
            :maxlength="MAX_MESSAGE"
            :placeholder="editing ? '说说要怎么改' : '说一句，比如「明天下午谁有空」'"
            :aria-label="editing ? '说说要怎么改' : '跟 AI 说'"
            class="w-full"
            :ui="{ base: 'px-4 pt-3 pb-1 text-sm' }"
            data-testid="ai-input"
            @keydown="onKeydown"
          />
          <div class="flex justify-end px-2 pb-2">
            <UButton
              color="neutral"
              :variant="text.trim() ? 'solid' : 'soft'"
              size="sm"
              icon="i-lucide-arrow-up"
              aria-label="发送"
              class="rounded-full"
              :loading="sending"
              :disabled="!text.trim()"
              data-testid="ai-send"
              @click="submit"
            />
          </div>
        </div>
        <ul v-if="centered && quick.length && !editing" class="flex flex-wrap justify-center gap-2 pt-4" data-testid="ai-quick">
          <li v-for="q in quick" :key="q.label">
            <button type="button" class="flex items-center gap-2 rounded-md px-3 py-2 text-sm text-muted hover:bg-hover hover:text-default focus-visible:bg-hover focus-visible:outline-2 focus-visible:outline-primary" :disabled="sending" @click="pick(q)">
              <UIcon :name="q.icon" class="size-4 shrink-0" aria-hidden="true" />
              {{ q.label }}
            </button>
          </li>
        </ul>
      </div>
    </template>
  </USlideover>
</template>
