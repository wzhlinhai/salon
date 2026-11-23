<script setup lang="ts">
import { MAX_MESSAGE } from '~/composables/useAiChat'

// 首页里的指令框（照原型 J/K）：说一句话，在右侧面板里接着谈。
// 默认是一行的小框，推荐说法排在框里；large 是前台首页的大框，推荐说法排在框下面。
const props = defineProps<{ placeholder?: string, large?: boolean, suggestions?: string[] }>()

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
    v-if="props.large"
    class="mx-auto w-full max-w-[720px] rounded-panel bg-white py-3 pr-3 pl-4 shadow-float focus-within:ring-2 focus-within:ring-primary"
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

  <div
    v-else
    class="mt-3.5 flex items-center gap-2 rounded-lane bg-white py-1.5 pr-1.5 pl-3.5 shadow-line focus-within:ring-2 focus-within:ring-primary"
  >
    <UIcon name="i-lucide-search" class="size-4 shrink-0 text-dimmed" aria-hidden="true" />
    <input
      v-model="text"
      :maxlength="MAX_MESSAGE"
      :placeholder="placeholder ?? '搜索或吩咐 AI…'"
      aria-label="吩咐 AI"
      data-testid="ai-command"
      class="min-w-0 flex-1 bg-transparent outline-none placeholder:text-dimmed"
      @keydown.enter.prevent="submit"
    >
    <div v-if="props.suggestions?.length" class="hidden shrink-0 gap-1.5 lg:flex" data-testid="ai-suggestions">
      <button
        v-for="said in props.suggestions"
        :key="said"
        type="button"
        class="rounded-full bg-elevated px-2.5 py-1 text-meta whitespace-nowrap text-muted hover:bg-accented"
        @click="ask(said)"
      >
        {{ said }}
      </button>
    </div>
    <kbd class="hidden rounded border border-default px-1.5 font-mono text-2xs text-dimmed sm:inline">Ctrl K</kbd>
    <button
      type="button"
      aria-label="发送"
      class="grid size-8 shrink-0 place-items-center rounded-full bg-ai text-white"
      @click="send"
    >
      <UIcon name="i-lucide-arrow-up" class="size-4" />
    </button>
  </div>
</template>
