<script setup lang="ts">
import { MAX_MESSAGE } from '~/composables/useAiChat'

// 右侧滑出的「问 AI」（样稿 24）：人说的话是墨色气泡靠右，AI 的回话是浅灰气泡靠左；
// 写操作只生成草稿，画成能拿在手里的小卡片，在这里或"待确认"里由人确认。
const { open, entries, sending, editing, input: text, send, reset } = useAiChat()
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
  if (!value.trim() || sending.value) return
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

// 打开时把焦点放在输入框，不让对话框自动聚焦标题栏的按钮：从指令框回车打开时，回车会误按到"清空对话"。
function focusInput() {
  input.value?.textareaRef?.focus()
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
    :ui="{ content: 'max-w-md', wrapper: 'flex-1 min-w-0', close: 'static', body: 'p-0 sm:p-0 flex flex-col', footer: 'ai-chat' }"
    data-testid="ai-panel"
    @after:enter="focusInput"
  >
    <template #title>
      <span class="flex items-center gap-2"><AiMark class="size-7 shrink-0" />问 AI</span>
    </template>
    <template #actions>
      <UButton
        v-if="entries.length"
        color="neutral"
        variant="ghost"
        size="sm"
        icon="i-lucide-rotate-ccw"
        aria-label="清空对话"
        @click="reset(); open = true"
      />
    </template>

    <template #body>
      <div ref="scroller" class="flex-1 space-y-4 overflow-y-auto p-4" data-testid="ai-messages">
        <!-- 还没说话时照 Notion 的 AI 面板：形象和一句招呼落在底部，挨着输入框。 -->
        <div v-if="!entries.length" class="flex min-h-full flex-col justify-end gap-3 pb-2">
          <span class="grid size-16 place-items-center rounded-full bg-white shadow-lift"><AiMark class="size-11" /></span>
          <p class="text-base font-semibold text-highlighted">店里的事，跟我说。</p>
          <p class="text-sm text-muted text-pretty">要改数据时 AI 先起草，由你确认。</p>
          <!-- 正在改一张草稿时不列：这时发的话都算在改那张草稿上。 -->
          <ul v-if="quick.length && !editing" class="-mx-2" data-testid="ai-quick">
            <li v-for="q in quick" :key="q.label">
              <button type="button" class="flex w-full items-center gap-3 rounded-md px-2 py-2 text-start text-sm text-default hover:bg-hover focus-visible:bg-hover focus-visible:outline-none" :disabled="sending" @click="pick(q)">
                <UIcon :name="q.icon" class="size-5 shrink-0 text-muted" aria-hidden="true" />
                {{ q.label }}
              </button>
            </li>
          </ul>
        </div>
        <div v-for="(entry, index) in entries" :key="index" :data-role="entry.role" data-testid="ai-entry">
          <div v-if="entry.role === 'user'" class="ms-auto w-fit max-w-[85%] rounded-card bg-inverted px-4 py-2 text-sm whitespace-pre-wrap text-inverted">
            <p v-if="entry.editing" class="mb-1 text-xs text-inverted/70">修改：{{ entry.editing }}</p>
            {{ entry.content }}
          </div>
          <div v-else class="space-y-3">
            <p class="w-fit max-w-[85%] rounded-card px-4 py-2 text-sm whitespace-pre-wrap text-pretty" :class="entry.failed ? 'bg-tag-red' : 'bg-elevated'">{{ entry.content }}</p>
            <DraftCard v-for="draft in entry.drafts ?? []" :key="draft.id" :draft="draft" compact />
          </div>
        </div>
        <p v-if="sending" class="flex items-center gap-2 text-sm text-muted" data-testid="ai-thinking">
          <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" />
          正在想…
        </p>
      </div>
    </template>

    <template #footer>
      <div class="w-full space-y-2">
        <div v-if="editing" class="flex items-center gap-2 text-xs text-muted" data-testid="ai-editing">
          <UIcon name="i-lucide-pencil" class="size-3.5 shrink-0" />
          <span class="truncate">修改：{{ editing.summary }}</span>
          <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-x" aria-label="不改了" class="ms-auto" @click="editing = null" />
        </div>
        <p v-else-if="starter" class="flex items-center gap-2 text-xs text-muted" data-testid="ai-starter">
          <UIcon :name="starter.icon" class="size-3.5 shrink-0" />
          <span class="text-pretty">{{ starter.hint }}</span>
        </p>
        <div class="flex items-end gap-2">
          <UTextarea
            ref="input"
            v-model="text"
            :rows="1"
            autoresize
            :maxrows="6"
            :maxlength="MAX_MESSAGE"
            :placeholder="editing ? '说说要怎么改' : '说一句，比如「明天下午谁有空」'"
            class="flex-1"
            data-testid="ai-input"
            @keydown="onKeydown"
          />
          <UButton
            color="neutral"
            icon="i-lucide-arrow-up"
            aria-label="发送"
            :loading="sending"
            :disabled="!text.trim()"
            data-testid="ai-send"
            @click="submit"
          />
        </div>
      </div>
    </template>
  </USlideover>
</template>
