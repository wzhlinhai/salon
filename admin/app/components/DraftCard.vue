<script setup lang="ts">
import type { Draft, DraftView } from '~/composables/useDrafts'

// 一张 AI 草稿：谁在什么时候说了什么、AI 理解成什么，以及确认、修改、拒绝。
// 对话面板里的草稿是回复那一刻的快照，状态和版本以收件箱里的最新一份为准。
const props = defineProps<{ draft: Draft | DraftView }>()
const emit = defineEmits<{ open: [id: string] }>()

const api = useApi()
const toast = useToast()
const { drafts, load, staffName } = useDrafts()
const { editDraft } = useAiChat()

const current = computed<Draft>(() => drafts.value.find(row => row.id === props.draft.id) ?? props.draft)
const isPending = computed(() => current.value.status === 'pending')

interface PreviewLine { item_name: string, price: string, staff: { staff_name: string }[] }

const previewLines = computed<PreviewLine[]>(() => {
  const preview = 'preview' in props.draft ? props.draft.preview as { lines?: PreviewLine[] } | null : null
  return Array.isArray(preview?.lines) ? preview.lines : []
})
const previewError = computed(() => 'preview_error' in props.draft ? props.draft.preview_error : '')

const statusBadge = computed(() => {
  switch (current.value.status) {
    case 'confirmed': return { label: '已确认', color: 'success' as const }
    case 'confirmed_modified': return { label: '改后确认', color: 'success' as const }
    case 'rejected': return { label: '已拒绝', color: 'neutral' as const }
    case 'expired': return { label: '已过期', color: 'neutral' as const }
    default: return null
  }
})

function when(value: string) {
  const date = new Date(value)
  const time = date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })
  const today = new Date()
  if (date.toDateString() === today.toDateString()) return time
  return `${date.getMonth() + 1} 月 ${date.getDate()} 日 ${time}`
}

function yuan(value: string) {
  return `¥${Number(value).toLocaleString('zh-CN', { maximumFractionDigits: 2 })}`
}

const confirming = ref(false)

async function confirm() {
  if (confirming.value) return
  confirming.value = true
  try {
    const { data, error } = await api.POST('/api/ops/confirm_draft', { body: { id: current.value.id, version: current.value.version } })
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    toast.add({ title: '已确认', color: 'success' })
  } finally {
    confirming.value = false
    // 失败也刷新：草稿可能已被别人处理或改过版本。
    void load()
  }
}

const reject = useModalForm()
const { open: rejectOpen, saving: rejecting } = reject
const rejectReason = ref('')

function beginReject() {
  reject.begin(() => {
    rejectReason.value = ''
  })
}

async function saveReject() {
  const reason = rejectReason.value.trim()
  if (!reason) return
  await reject.save(async (still) => {
    const { data, error } = await api.POST('/api/ops/reject_draft', { body: { id: current.value.id, reason } })
    if (!still()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    rejectOpen.value = false
    toast.add({ title: '已拒绝', color: 'success' })
    void load()
  })
}
</script>

<template>
  <article data-testid="draft-card" :data-draft-id="current.id" class="space-y-2 rounded-lg border border-default p-4">
    <header class="flex items-center gap-1.5 text-xs text-muted">
      <UIcon name="i-lucide-sparkles" class="size-3.5 shrink-0 text-ai" />
      <span class="font-medium text-ai">AI 起草</span>
      <span>·</span>
      <span data-testid="draft-initiator" class="truncate">{{ staffName(current.initiator) }}</span>
      <span>·</span>
      <span class="tabular-nums">{{ when(current.created_at) }}</span>
      <UBadge v-if="statusBadge" :color="statusBadge.color" variant="subtle" size="sm" class="ms-auto">
        {{ statusBadge.label }}
      </UBadge>
    </header>

    <p v-if="current.utterance" data-testid="draft-utterance" class="text-sm text-muted text-pretty">“{{ current.utterance }}”</p>

    <button
      type="button"
      data-testid="draft-summary"
      class="block w-full text-left text-sm font-medium text-pretty hover:underline"
      @click="emit('open', current.id)"
    >
      {{ current.summary }}
    </button>

    <ul v-if="previewLines.length" class="space-y-0.5 text-sm">
      <li v-for="(line, index) in previewLines" :key="index" class="flex gap-2">
        <span class="truncate">{{ line.item_name }}</span>
        <span class="truncate text-muted">{{ line.staff.map(s => s.staff_name).join('、') }}</span>
        <span class="ms-auto tabular-nums">{{ yuan(line.price) }}</span>
      </li>
    </ul>
    <p v-if="previewError" class="text-sm text-warning">{{ previewError }}</p>
    <p v-if="current.status === 'rejected' && current.reject_reason" class="text-sm text-muted">
      拒绝原因：{{ current.reject_reason }}
    </p>

    <footer v-if="isPending" class="flex items-center gap-2 pt-1">
      <UButton color="neutral" size="sm" :loading="confirming" data-testid="draft-confirm" @click="confirm">确认</UButton>
      <UButton color="neutral" variant="outline" size="sm" data-testid="draft-modify" @click="editDraft(current)">修改</UButton>
      <UButton color="neutral" variant="ghost" size="sm" data-testid="draft-reject" @click="beginReject">拒绝</UButton>
    </footer>

    <UModal v-model:open="rejectOpen" title="拒绝草稿" :description="current.summary">
      <template #body>
        <UFormField label="原因" required>
          <UTextarea v-model="rejectReason" :rows="2" autofocus class="w-full" placeholder="例如：顾客改约了" data-testid="reject-reason" />
        </UFormField>
      </template>
      <template #footer>
        <div class="flex w-full justify-end gap-2">
          <UButton color="neutral" variant="outline" @click="rejectOpen = false">取消</UButton>
          <UButton color="error" :loading="rejecting" :disabled="!rejectReason.trim()" data-testid="reject-submit" @click="saveReject">拒绝</UButton>
        </div>
      </template>
    </UModal>
  </article>
</template>
