<script setup lang="ts">
import type { Draft, DraftView } from '~/composables/useDrafts'

// 一张 AI 草稿：谁在什么时候说了什么、AI 理解成什么，以及确认、修改、拒绝。
// 卡片显示的内容和确认时带的版本都取自传进来的这一份（人看到的就是要确认的）；
// 状态取收件箱里的最新一份。草稿在别处被改过时不让在这张旧卡片上确认，免得确认了没见过的内容。
// compact：放在「问 AI」对话里（样稿 24）。上面的话已经说明了是谁说的，卡片只画要确认的东西：
// 一句摘要和「草稿」签，下面一项一行（项目、做的人、价钱），主按钮写这一步要做的事（「确认开单」），旁边「改」「不要」。
const props = defineProps<{ draft: Draft | DraftView, compact?: boolean }>()
const emit = defineEmits<{ open: [id: string] }>()

const api = useApi()
const toast = useToast()
const { drafts, load, staffName } = useDrafts()
const { editDraft } = useAiChat()
// 开单、新建顾客直接弹表单改；其余草稿在 AI 面板里说怎么改。
const formEditable = computed(() => ['open_order', 'create_customer'].includes(props.draft.operation))
const editModal = useTemplateRef('editModal')

function modify() {
  if (formEditable.value) editModal.value?.begin(current.value)
  else editDraft(props.draft)
}

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
    case 'confirmed': return { label: '已确认', tone: 'bg-tag-gray' }
    case 'confirmed_modified': return { label: '改后确认', tone: 'bg-tag-gray' }
    case 'rejected': return { label: '已拒绝', tone: 'bg-tag-gray' }
    case 'expired': return { label: '已过期', tone: 'bg-tag-gray' }
    default: return { label: '待确认', tone: 'bg-tag-yellow' }
  }
})

// 主按钮写这一步要做的事，认不出的操作写「确认」。
const confirmLabels: Record<string, string> = { open_order: '确认开单', create_customer: '确认建档', create_appointment: '确认预约' }
const confirmLabel = computed(() => props.compact ? confirmLabels[props.draft.operation] ?? '确认' : '确认')

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
  <article data-testid="draft-card" :data-draft-id="current.id" class="box flex flex-col gap-2">
    <header v-if="compact" class="flex items-start gap-2 border-b border-dashed border-default pb-2">
      <button
        type="button"
        data-testid="draft-summary"
        class="min-w-0 flex-1 text-left text-sm font-medium text-pretty hover:underline"
        @click="emit('open', props.draft.id)"
      >
        {{ props.draft.summary }}
      </button>
      <span class="tag shrink-0" :class="isPending ? 'bg-tag-pink' : statusBadge.tone">{{ isPending ? '草稿' : statusBadge.label }}</span>
    </header>
    <header v-else class="flex items-center gap-2 text-xs text-dimmed">
      <span class="tag shrink-0 bg-ai-soft text-ai">AI 起草</span>
      <span class="min-w-0 truncate"><span data-testid="draft-initiator">{{ staffName(current.initiator) }}</span>说 · <span class="tabular-nums">{{ formatStamp(props.draft.created_at) }}</span></span>
      <span v-if="!isPending" class="tag ms-auto shrink-0" :class="statusBadge.tone">{{ statusBadge.label }}</span>
    </header>

    <button
      v-if="!compact"
      type="button"
      data-testid="draft-summary"
      class="block w-full text-left text-sm font-semibold text-pretty hover:underline"
      @click="emit('open', props.draft.id)"
    >
      {{ props.draft.summary }}
    </button>

    <p v-if="!compact && props.draft.utterance" data-testid="draft-utterance" class="text-xs text-muted text-pretty">“{{ props.draft.utterance }}”</p>

    <ul v-if="previewLines.length" class="space-y-1 text-meta text-muted" :class="compact ? '' : 'rounded-md bg-elevated p-2'">
      <li v-for="(line, index) in previewLines" :key="index" class="flex gap-2">
        <span class="truncate text-default" :class="compact ? 'text-sm' : 'font-medium'">{{ line.item_name }}</span>
        <span class="truncate">{{ line.staff.map(s => s.staff_name).join('、') }}</span>
        <span class="ms-auto tabular-nums text-default">{{ yuanFixed(line.price) }}</span>
      </li>
    </ul>
    <p v-if="previewError" class="rounded-md bg-tag-yellow/60 px-2 py-2 text-meta">{{ previewError }}</p>
    <p v-if="current.status === 'rejected' && current.reject_reason" class="text-meta text-muted">
      拒绝原因：{{ current.reject_reason }}
    </p>

    <p v-if="stale" class="mt-auto text-meta text-ink-yellow" data-testid="draft-stale">
      这张草稿已经改过，<ULink to="/drafts" class="underline">到待确认里看最新内容</ULink>再确认。
    </p>
    <footer v-else-if="isPending" class="mt-auto flex items-center gap-2 pt-1">
      <UButton color="neutral" :loading="confirming" data-testid="draft-confirm" class="min-h-8 px-3" :class="compact ? 'flex-1 justify-center' : ''" @click="confirm">{{ confirmLabel }}</UButton>
      <UButton color="neutral" variant="outline" data-testid="draft-modify" class="min-h-8 px-3" @click="modify">{{ compact ? '改' : '修改' }}</UButton>
      <UButton color="neutral" variant="outline" data-testid="draft-reject" class="min-h-8 px-3" @click="beginReject">{{ compact ? '不要' : '拒绝' }}</UButton>
    </footer>

    <DraftEditModal v-if="formEditable" ref="editModal" @saved="load()" />

    <UModal v-model:open="rejectOpen" title="拒绝草稿" :description="props.draft.summary">
      <template #body>
        <UFormField label="原因" required>
          <UTextarea v-model="rejectReason" :rows="2" autofocus class="w-full" placeholder="例如：顾客改约了" data-testid="reject-reason" />
        </UFormField>
      </template>
      <template #footer>
        <ModalActions @cancel="rejectOpen = false">
          <UButton color="error" :loading="rejecting" :disabled="!rejectReason.trim()" data-testid="reject-submit" @click="saveReject">拒绝</UButton>
        </ModalActions>
      </template>
    </UModal>
  </article>
</template>
