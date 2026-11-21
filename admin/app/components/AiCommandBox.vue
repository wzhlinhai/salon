<script setup lang="ts">
import { MAX_MESSAGE } from '~/composables/useAiChat'

// 首页里展开的一行指令框：说一句话，在右侧面板里接着谈。
defineProps<{ placeholder?: string }>()

const { ask } = useAiChat()
const text = ref('')

function submit(event: KeyboardEvent) {
  if (event.isComposing || !text.value.trim()) return
  ask(text.value)
  text.value = ''
}
</script>

<template>
  <UInput
    v-model="text"
    icon="i-lucide-sparkles"
    :maxlength="MAX_MESSAGE"
    :placeholder="placeholder ?? '吩咐 AI，例如“给陈女士约明天下午 3 点剪发”'"
    size="lg"
    class="w-full"
    :ui="{ leadingIcon: 'text-ai' }"
    aria-label="吩咐 AI"
    data-testid="ai-command"
    @keydown.enter.prevent="submit"
  />
</template>
