<script setup lang="ts">
import { MAX_MESSAGE } from '~/composables/useAiChat'

// 右侧滑出的 AI 对话面板（side peek）。写操作只生成草稿，在这里或"待确认"里由人确认。
const { open, entries, sending, editing, input: text, send, reset } = useAiChat()
const scroller = ref<HTMLElement | null>(null)
const input = useTemplateRef<{ textareaRef?: HTMLTextAreaElement }>('input')

async function submit() {
  const value = text.value
  if (!value.trim() || sending.value) return
  text.value = ''
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
      吩咐 AI
    </template>
    <template #description>
      <span class="text-xs text-muted">AI 只起草，确认后才会生效</span>
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
        <p v-if="!entries.length" class="text-sm text-muted text-pretty">
          说一句话，例如“给陈女士约明天下午 3 点剪发”“今天实收多少”。要改数据时 AI 会先起草，由你确认。
        </p>
        <div v-for="(entry, index) in entries" :key="index" :data-role="entry.role" data-testid="ai-entry">
          <div v-if="entry.role === 'user'" class="ms-8 rounded-lg bg-elevated px-3 py-2 text-sm whitespace-pre-wrap">
            <p v-if="entry.editing" class="mb-1 text-xs text-muted">修改：{{ entry.editing }}</p>
            {{ entry.content }}
          </div>
          <div v-else class="space-y-3">
            <p class="text-sm whitespace-pre-wrap text-pretty" :class="entry.failed ? 'text-error' : ''">{{ entry.content }}</p>
            <DraftCard v-for="draft in entry.drafts ?? []" :key="draft.id" :draft="draft" />
          </div>
        </div>
        <p v-if="sending" class="flex items-center gap-1.5 text-sm text-muted" data-testid="ai-thinking">
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
        <div class="flex items-end gap-2">
          <UTextarea
            ref="input"
            v-model="text"
            :rows="1"
            autoresize
            :maxrows="6"
            :maxlength="MAX_MESSAGE"
            :placeholder="editing ? '说说要怎么改' : '说一句话'"
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
