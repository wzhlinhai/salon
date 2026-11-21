<script setup lang="ts">
import type { Draft, DraftView } from '~/composables/useDrafts'

// 一张 AI 草稿：谁在什么时候说了什么、AI 理解成什么，以及确认、修改、拒绝。
// 卡片显示的内容和确认时带的版本都取自传进来的这一份（人看到的就是要确认的）；
// 状态取收件箱里的最新一份。草稿在别处被改过时不让在这张旧卡片上确认，免得确认了没见过的内容。
const props = defineProps<{ draft: Draft | DraftView }>()
const emit = defineEmits<{ open: [id: string] }>()

const api = useApi()
const toast = useToast()
const { drafts, load, staffName } = useDrafts()
const { editDraft } = useAiChat()

const current = computed<Draft>(() => drafts.value.find(row => row.id === props.draft.id) ?? props.draft)
const isPending = computed(() => current.value.status === 'pending')
const stale = computed(() => isPending.value && current.value.version !== props.draft.version)

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
    default: return { label: '待确认', color: 'warning' as const }
  }
})

function yuan(value: string) {
  return `¥${Number(value).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}

const confirming = ref(false)

async function confirm() {
  if (confirming.value || stale.value) return
  confirming.value = true
  try {
    const { data, error } = await api.POST('/api/ops/confirm_draft', { body: { id: props.draft.id, version: props.draft.version } })
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
      // 草稿可能已被别人处理，刷新后卡片显示真实状态。
      void load()
      return
    }
    rejectOpen.value = false
    toast.add({ title: '已拒绝', color: 'success' })
    void load()
  })
}
</script>

<template>
  <article data-testid="draft-card" :data-draft-id="current.id" class="space-y-2 rounded-xl border border-default p-4">
    <header class="flex items-center gap-1.5 text-xs text-muted">
      <UIcon name="i-lucide-sparkles" class="size-3.5 shrink-0 text-ai" />
      <span class="font-medium text-ai">AI 起草</span>
      <span>·</span>
      <span data-testid="draft-initiator" class="truncate">{{ staffName(current.initiator) }}</span>
      <span>·</span>
      <span class="tabular-nums">{{ formatStamp(props.draft.created_at) }}</span>
      <UBadge v-if="statusBadge" :color="statusBadge.color" variant="subtle" size="sm" class="ms-auto">
        {{ statusBadge.label }}
      </UBadge>
    </header>

    <p v-if="props.draft.utterance" data-testid="draft-utterance" class="text-sm text-muted text-pretty">“{{ props.draft.utterance }}”</p>

    <button
      type="button"
      data-testid="draft-summary"
      class="block w-full text-left text-sm font-medium text-pretty hover:underline"
      @click="emit('open', props.draft.id)"
    >
      {{ props.draft.summary }}
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

    <p v-if="stale" class="text-sm text-warning" data-testid="draft-stale">
      这张草稿已经改过，<ULink to="/drafts" class="underline">到待确认里看最新内容</ULink>再确认。
    </p>
    <footer v-else-if="isPending" class="flex items-center gap-2 pt-1">
      <UButton color="neutral" size="sm" :loading="confirming" data-testid="draft-confirm" @click="confirm">确认</UButton>
      <UButton color="neutral" variant="outline" size="sm" data-testid="draft-modify" @click="editDraft(props.draft)">修改</UButton>
      <UButton color="neutral" variant="ghost" size="sm" data-testid="draft-reject" @click="beginReject">拒绝</UButton>
    </footer>

    <UModal v-model:open="rejectOpen" title="拒绝草稿" :description="props.draft.summary">
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
