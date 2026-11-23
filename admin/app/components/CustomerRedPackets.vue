<script setup lang="ts">
import type { components } from '~/utils/api'

const props = defineProps<{ id: string, merged: boolean }>()
const api = useApi()
const toast = useToast()
const { can } = useMe()
type RedPacket = components['schemas']['RedPacket']
type Payment = components['schemas']['Payment']
const packets = ref<RedPacket[]>([])
const loading = ref(false)
const listGuard = useRequestGuard()
const statusNames = { available: '可用', used: '已用', revoked: '已撤回', expired: '已过期' }

async function load() {
  const current = listGuard.next()
  loading.value = true
  try {
    const { data, error } = await api.POST('/api/ops/list_customer_red_packets', { body: { customer_id: props.id } })
    if (!current()) return
    if (data) packets.value = data.items
    else toast.add({ title: errorText(error), color: 'error' })
  } finally {
    if (current()) loading.value = false
  }
}

const issueForm = useModalForm()
const { open: issueOpen, loading: staffLoading, saving: issuing } = issueForm
const kind = ref<'sold' | 'gift'>('sold')
const faceValue = ref('')
const price = ref('')
const expiresOn = ref('')
const sales = ref<string[]>([])
const payments = ref<Payment[]>([])
const paymentsTouched = ref(false)
const staff = ref<components['schemas']['StaffOption'][]>([])

async function editIssue() {
  issueForm.begin(() => {
    kind.value = 'sold'
    faceValue.value = ''
    price.value = ''
    expiresOn.value = ''
    sales.value = []
    payments.value = []
    paymentsTouched.value = false
    staff.value = []
  })
  await issueForm.load(async (current) => {
    const { data, error } = await api.POST('/api/ops/list_staff_options', { body: {} })
    if (!current()) return
    if (data) staff.value = data.staff.filter(s => s.active)
    else toast.add({ title: errorText(error), color: 'error' })
  })
}

async function issue() {
  const body: components['schemas']['IssueRedPacketInput'] = { customer_id: props.id, kind: kind.value, face_value: faceValue.value, expires_on: expiresOn.value, payments: [] }
  if (kind.value === 'sold') {
    body.price = price.value
    body.sales_staff_ids = [...sales.value]
    body.payments = payments.value
  } else {
    body.payments = []
  }
  await issueForm.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/issue_red_packet', { body })
    if (!current()) return
    if (data) {
      issueOpen.value = false
      await load()
    } else toast.add({ title: errorText(error), color: 'error' })
  })
}

const revokeForm = useModalForm()
const { open: revokeOpen, saving: revoking } = revokeForm
const target = ref<RedPacket | null>(null)
const reason = ref('')

function editRevoke(packet: RedPacket) {
  revokeForm.begin(() => {
    target.value = packet
    reason.value = ''
  })
}

async function revoke() {
  if (!target.value) return
  const body = { red_packet_id: target.value.id, reason: reason.value.trim() }
  await revokeForm.save(async (current) => {
    const { data, error } = await api.POST('/api/ops/revoke_red_packet', { body })
    if (!current()) return
    if (data) {
      revokeOpen.value = false
      await load()
    } else toast.add({ title: errorText(error), color: 'error' })
  })
}

onMounted(load)
</script>

<template>
  <section data-testid="red-packets">
    <div class="sec mt-0! min-h-8">
      <span v-if="packets.length" class="font-normal text-dimmed">{{ packets.length }} 个</span>
      <UButton v-if="can('card.sell') && !merged" size="sm" color="neutral" variant="ghost" icon="i-lucide-plus" class="ms-auto" @click="editIssue">发放红包</UButton>
    </div>
    <div class="box overflow-hidden p-0">
      <p v-if="loading" class="px-3 py-2.5 text-sm text-muted">正在读取红包…</p>
      <p v-else-if="!packets.length" class="px-3 py-2.5 text-sm text-muted">暂无红包</p>
      <div v-for="packet in packets" :key="packet.id" :data-red-packet="packet.id" class="group/row space-y-1 border-t border-default px-3 py-2.5 text-sm first:border-t-0" :class="packet.status === 'available' ? '' : 'text-muted'">
        <p class="flex flex-wrap items-center gap-x-3 gap-y-1">
          <span class="tag bg-tag-gray">{{ packet.kind === 'sold' ? '卖' : '送' }}</span>
          <span class="font-medium tabular-nums"><span class="font-normal text-muted">面额</span> {{ yuanFixed(packet.face_value) }}</span>
          <span v-if="packet.status !== 'available'" class="tag bg-tag-gray">{{ statusNames[packet.status] }}</span>
          <span class="ms-auto text-meta text-muted">有效期至 {{ packet.expires_on }}</span>
        </p>
        <p v-if="packet.price" class="text-meta text-muted">售价 {{ yuanFixed(packet.price) }} · 业绩归属人：{{ packet.sales_staff.map(s => s.name).join('、') }}</p>
        <p v-if="packet.order_id" class="text-meta text-muted">已用单据 {{ packet.order_id }} · 抵扣 {{ yuanFixed(packet.used_amount) }}</p>
        <p v-if="packet.revoke_reason" class="text-meta text-muted">撤回原因：{{ packet.revoke_reason }}</p>
        <UButton v-if="can('card.adjust') && (packet.status === 'available' || packet.status === 'expired')" size="sm" color="error" variant="ghost" class="-mx-1.5" @click="editRevoke(packet)">撤回</UButton>
      </div>
    </div>
  </section>
  <UModal v-model:open="issueOpen" title="发放红包">
    <template #body>
      <div class="space-y-4">
        <UFormField label="卖或送"><USelect v-model="kind" :items="[{ label: '卖', value: 'sold' }, { label: '送', value: 'gift' }]" class="w-full" /></UFormField>
        <UFormField label="面额"><UInput v-model="faceValue" inputmode="decimal" class="w-full" /></UFormField>
        <UFormField label="有效期"><DateInput v-model="expiresOn" class="w-full" /></UFormField>
        <template v-if="kind === 'sold'">
          <UFormField label="售价"><UInput v-model="price" inputmode="decimal" class="w-full" /></UFormField>
          <UFormField label="业绩归属人"><USelect v-model="sales" multiple :items="staff.map(s => ({ label: s.name, value: s.id }))" class="w-full" /></UFormField>
          <PaymentEditor v-model="payments" v-model:touched="paymentsTouched" :expected="price" />
        </template>
      </div>
    </template>
    <template #footer>
      <ModalActions @cancel="issueOpen = false">
        <UButton :loading="issuing" :disabled="staffLoading || !faceValue || !expiresOn || (kind === 'sold' && (!price || !sales.length))" @click="issue">保存</UButton>
      </ModalActions>
    </template>
  </UModal>
  <UModal v-model:open="revokeOpen" title="撤回红包">
    <template #body><UFormField label="撤回原因" required><UTextarea v-model="reason" :maxlength="200" class="w-full" /></UFormField></template>
    <template #footer>
      <ModalActions @cancel="revokeOpen = false">
        <UButton :loading="revoking" :disabled="!reason.trim()" color="error" @click="revoke">确认撤回</UButton>
      </ModalActions>
    </template>
  </UModal>
</template>
