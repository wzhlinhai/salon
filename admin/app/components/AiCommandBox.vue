<script setup lang="ts">
import { MAX_MESSAGE } from '~/composables/useAiChat'

// 前台首页正中的指令框（照原型 J）：说一句话，在右侧面板里接着谈；推荐说法排在框下面，点一下直接交给 AI。
// 其他页面吩咐 AI 用右下角的 AI 按钮。
const props = defineProps<{ placeholder?: string, suggestions?: string[] }>()

const { ask } = useAiChat()
const text = ref('')

function send() {
  if (!text.value.trim()) return
  ask(text.value)
  text.value = ''
}

function submit(event: KeyboardEvent) {
  // Safari 选字上屏的回车 isComposing 为 false，要再看 keyCode 229。
  if (event.isComposing || event.keyCode === 229) return
  send()
}
</script>

<template>
  <div
    class="mx-auto w-full max-w-[720px] rounded-card bg-white py-3 pr-3 pl-4 shadow-float focus-within:ring-2 focus-within:ring-primary"
  >
    <div class="flex min-h-10 items-center gap-2.5">
      <UIcon name="i-lucide-search" class="size-5 shrink-0 text-dimmed" aria-hidden="true" />
      <input
        v-model="text"
        :maxlength="MAX_MESSAGE"
        :placeholder="placeholder ?? '说一句要做的事，或搜索顾客、预约…'"
        aria-label="吩咐 AI"
        data-testid="ai-command"
        class="min-w-0 flex-1 bg-transparent text-lead outline-none placeholder:text-dimmed"
        @keydown.enter.prevent="submit"
      >
      <kbd class="hidden rounded border border-default px-1.5 font-mono text-2xs text-dimmed sm:inline">Ctrl K</kbd>
      <button
        type="button"
        aria-label="发送"
        class="grid size-10 shrink-0 place-items-center rounded-full bg-ai text-white"
        @click="send"
      >
        <UIcon name="i-lucide-arrow-up" class="size-5" />
      </button>
    </div>
    <div v-if="props.suggestions?.length" class="mt-2 flex flex-wrap gap-1.5" data-testid="ai-suggestions">
      <button
        v-for="said in props.suggestions"
        :key="said"
        type="button"
        class="min-h-6 rounded-full bg-elevated px-2.5 py-1 text-meta text-muted hover:bg-accented"
        @click="ask(said)"
      >
        {{ said }}
      </button>
    </div>
  </div>
</template>
