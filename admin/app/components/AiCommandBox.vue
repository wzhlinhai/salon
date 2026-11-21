<script setup lang="ts">
import { MAX_MESSAGE } from '~/composables/useAiChat'

// 首页里展开的指令框：说一句话，在右侧面板里接着谈。
// 前台首页用 large：框放大，下面列几句推荐说法，点一下就交给 AI。
const props = defineProps<{ placeholder?: string, large?: boolean, suggestions?: string[] }>()

const { ask } = useAiChat()
const text = ref('')

function submit(event: KeyboardEvent) {
  // Safari 选字上屏的回车 isComposing 为 false，要再看 keyCode 229。
  if (event.isComposing || event.keyCode === 229 || !text.value.trim()) return
  ask(text.value)
  text.value = ''
}
</script>

<template>
  <div class="space-y-2">
    <UInput
      v-model="text"
      icon="i-lucide-sparkles"
      :maxlength="MAX_MESSAGE"
      :placeholder="placeholder ?? '吩咐 AI，例如“给陈女士约明天下午 3 点剪发”'"
      :size="props.large ? 'xl' : 'lg'"
      class="w-full"
      :ui="{ leadingIcon: 'text-ai' }"
      aria-label="吩咐 AI"
      data-testid="ai-command"
      @keydown.enter.prevent="submit"
    />
    <div v-if="props.suggestions?.length" class="flex flex-wrap gap-1" data-testid="ai-suggestions">
      <UButton
        v-for="said in props.suggestions"
        :key="said"
        color="neutral"
        variant="ghost"
        size="sm"
        class="text-muted"
        @click="ask(said)"
      >
        {{ said }}
      </UButton>
    </div>
  </div>
</template>
