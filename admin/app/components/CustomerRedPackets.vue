<script setup lang="ts">
import type { components } from '~/utils/api'

const props = defineProps<{ id: string, merged: boolean }>()
const api = useApi()
const toast = useToast()
const { can } = useMe()
type RedPacket = components['schemas']['RedPacket']
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
const staff = ref<components['schemas']['StaffOption'][]>([])

async function editIssue() {
  issueForm.begin(() => {
    kind.value = 'sold'
    faceValue.value = ''
    price.value = ''
    expiresOn.value = ''
    sales.value = []
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
  const body: components['schemas']['IssueRedPacketInput'] = { customer_id: props.id, kind: kind.value, face_value: faceValue.value, expires_on: expiresOn.value }
  if (kind.value === 'sold') {
    body.price = price.value
    body.sales_staff_ids = [...sales.value]
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
  <section class="space-y-3" data-testid="red-packets">
    <div class="flex items-center justify-between">
      <h3 class="font-medium">红包</h3>
      <UButton v-if="can('card.sell') && !merged" size="sm" @click="editIssue">发放红包</UButton>
    </div>
    <p v-if="loading">正在读取红包…</p>
    <p v-else-if="!packets.length" class="text-sm text-muted">暂无红包</p>
    <UCard v-for="packet in packets" :key="packet.id" :data-red-packet="packet.id">
      <div class="space-y-2 text-sm">
        <p>{{ packet.kind === 'sold' ? '卖' : '送' }} · 面额 {{ packet.face_value }} · {{ statusNames[packet.status] }}</p>
        <p v-if="packet.price">售价 {{ packet.price }} · 业绩归属人：{{ packet.sales_staff.map(s => s.name).join('、') }}</p>
        <p>有效期至 {{ packet.expires_on }}</p>
        <p v-if="packet.order_id">已用单据 {{ packet.order_id }} · 抵扣 {{ packet.used_amount }}</p>
        <p v-if="packet.revoke_reason">撤回原因：{{ packet.revoke_reason }}</p>
        <UButton v-if="can('card.adjust') && (packet.status === 'available' || packet.status === 'expired')" size="sm" color="error" variant="outline" @click="editRevoke(packet)">撤回</UButton>
      </div>
    </UCard>
  </section>
  <UModal v-model:open="issueOpen" title="发放红包">
    <template #body>
      <div class="space-y-4">
        <UFormField label="卖或送"><USelect v-model="kind" :items="[{ label: '卖', value: 'sold' }, { label: '送', value: 'gift' }]" class="w-full" /></UFormField>
        <UFormField label="面额"><UInput v-model="faceValue" inputmode="decimal" class="w-full" /></UFormField>
        <UFormField label="有效期"><UInput v-model="expiresOn" type="date" class="w-full" /></UFormField>
        <template v-if="kind === 'sold'">
          <UFormField label="售价"><UInput v-model="price" inputmode="decimal" class="w-full" /></UFormField>
          <UFormField label="业绩归属人"><USelect v-model="sales" multiple :items="staff.map(s => ({ label: s.name, value: s.id }))" class="w-full" /></UFormField>
        </template>
      </div>
    </template>
    <template #footer><UButton :loading="issuing" :disabled="staffLoading || !faceValue || !expiresOn || (kind === 'sold' && (!price || !sales.length))" @click="issue">保存</UButton></template>
  </UModal>
  <UModal v-model:open="revokeOpen" title="撤回红包">
    <template #body><UFormField label="撤回原因" required><UTextarea v-model="reason" :maxlength="200" class="w-full" /></UFormField></template>
    <template #footer><UButton :loading="revoking" :disabled="!reason.trim()" color="error" @click="revoke">确认撤回</UButton></template>
  </UModal>
</template>
