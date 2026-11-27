<script setup lang="ts">
import type { components } from '~/utils/api'

definePageMeta({ permission: 'settings.manage', title: 'AI 提醒', width: 'narrow', description: '规则看到店里有事，AI 写成一句话提醒该管的人。新规则先观察：只记下来给你看，不打扰任何人，你觉得有用再打开。', art: 'roller' })

type Rule = components['schemas']['ReminderRuleView']
type Summary = components['schemas']['ReminderSummary']
type Mode = Rule['mode']
type Feedback = components['schemas']['ReminderSample']['feedback']

const api = useApi()
const toast = useToast()

const MODES: { value: Mode, label: string }[] = [
  { value: 'off', label: '关' },
  { value: 'observe', label: '观察' },
  { value: 'on', label: '开' }
]

const rules = ref<Rule[]>([])
const summaries = reactive<Record<string, Summary>>({})
const loadError = ref('')
const loading = ref(true)
const summaryErrors = reactive<Record<string, string>>({})
const saving = reactive<Record<string, boolean>>({})
const rating = reactive<Record<string, boolean>>({})
const loadGuard = useRequestGuard()
const actionGuard = useRequestGuard()

async function loadSummary(ruleKey: string, alive: () => boolean = () => true) {
  const { data, error } = await api.POST('/api/ops/get_reminder_summary', { body: { rule_key: ruleKey, days: 7 } })
  if (!alive()) return
  if (data) {
    summaries[ruleKey] = data
    summaryErrors[ruleKey] = ''
  } else {
    summaryErrors[ruleKey] = errorText(error)
  }
}

async function load() {
  const request = loadGuard.next()
  loading.value = true
  loadError.value = ''
  try {
    const { data, error } = await api.POST('/api/ops/list_reminder_rules', { body: {} })
    if (!request()) return
    if (!data) {
      loadError.value = errorText(error)
      return
    }
    rules.value = data.rules
    await Promise.all(data.rules.map(r => loadSummary(r.rule_key, request)))
  } catch (e) {
    if (request()) loadError.value = errorText(e)
  } finally {
    if (request()) loading.value = false
  }
}

onMounted(load)

async function setMode(rule: Rule, mode: Mode) {
  if (rule.mode === mode || saving[rule.rule_key] || (mode === 'on' && rule.mode !== 'on')) return
  const current = actionGuard.hold()
  saving[rule.rule_key] = true
  try {
    const { data, error } = await api.POST('/api/ops/set_reminder_rule_mode', { body: { rule_key: rule.rule_key, mode } })
    if (!current()) return
    if (!data) {
      toast.add({ title: errorText(error), color: 'error' })
      return
    }
    Object.assign(rule, data)
  } finally {
    saving[rule.rule_key] = false
  }
}

async function rate(ruleKey: string, id: string, value: Exclude<Feedback, null>) {
  const sample = summaries[ruleKey]?.samples.find(s => s.id === id)
  if (!sample || rating[id]) return
  const next = sample.feedback === value ? null : value
  const before = sample.feedback
  const current = actionGuard.hold()
  rating[id] = true
  sample.feedback = next
  let ok = false
  try {
    const { data, error } = await api.POST('/api/ops/set_reminder_feedback', { body: { reminder_id: id, feedback: next } })
    ok = !!data
    if (!ok && current()) toast.add({ title: errorText(error), color: 'error' })
  } catch (e) {
    if (current()) toast.add({ title: errorText(e), color: 'error' })
  } finally {
    if (!ok && sample.feedback === next) sample.feedback = before
    rating[id] = false
  }
}

const BLOCKED: { key: keyof Summary['blocked'], label: string }[] = [
  { key: 'deduped', label: '当天已提醒过' },
  { key: 'cooldown', label: '刚提醒过' },
  { key: 'max_times', label: '提醒够次数了' },
  { key: 'expired', label: '事情过期了' },
  { key: 'daily_cap', label: '到了每人每天上限' }
]

function blockedText(s: Summary) {
  const parts = BLOCKED.filter(b => s.blocked[b.key] > 0).map(b => `${b.label} ${s.blocked[b.key]} 条`)
  return parts.length ? `拦下没发：${parts.join('，')}。` : ''
}

function summaryText(rule: Rule, s: Summary) {
  const off = rule.mode === 'off' ? '已关闭。' : ''
  if (s.ran_days === 0) return `${off}最近 ${s.days} 天还没检查过。`
  const head = `${off}最近 ${s.days} 天检查了 ${s.ran_days} 天`
  if (s.matched_days === 0 && s.emitted === 0) return `${head}，都没有要提醒的事。`
  const sent = rule.mode === 'on' ? '发出' : '本来会发'
  return `${head}，${s.matched_days} 天有事，${sent} ${s.emitted} 条，平均每人每天 ${Number(s.average_per_recipient_day.toFixed(1))} 条。${blockedText(s)}`
}

function dayText(date: string) {
  const [, m, d] = date.split('-')
  return `${Number(m)}月${Number(d)}日`
}
</script>

<template>
  <div>
    <ListSkeleton v-if="loading && !rules.length" />
    <LoadError v-else-if="loadError && !rules.length" title="AI 提醒没有读出来" :hint="loadError" @retry="load()" />
    <p v-else-if="!loading && !rules.length" class="py-6 text-sm text-muted">还没有提醒规则。</p>

    <section v-for="rule in rules" :key="rule.rule_key" :data-rule="rule.rule_key">
      <h2 class="sec">{{ rule.name }}</h2>
      <div class="flex flex-wrap items-start gap-x-6 gap-y-2 border-y border-default py-3 text-sm">
        <div class="min-w-0 flex-1 basis-full md:basis-0">
          <p class="text-pretty">{{ rule.description }}</p>
          <p class="text-meta text-muted">每天 {{ rule.check_at }} 检查<template v-if="rule.mode !== 'on'">。提醒卡放上首页和日结页之后才能开</template></p>
        </div>
        <div class="flex rounded-md bg-lane p-0.5 text-meta" role="group" :aria-label="`${rule.name}的档位`">
          <button
            v-for="m in MODES"
            :key="m.value"
            type="button"
            class="rounded-md px-3 py-1"
            :class="rule.mode === m.value ? 'bg-white shadow-line' : 'text-muted enabled:hover:text-default disabled:opacity-50'"
            :aria-pressed="rule.mode === m.value"
            :disabled="m.value === 'on' && rule.mode !== 'on'"
            :aria-disabled="saving[rule.rule_key] || undefined"
            @click="setMode(rule, m.value)"
          >
            {{ m.label }}
          </button>
        </div>
      </div>

      <p v-if="summaryErrors[rule.rule_key]" class="mt-3 text-sm text-ink-red" data-testid="reminder-summary-error">
        汇总没有读出来：{{ summaryErrors[rule.rule_key] }}
        <UButton size="xs" color="neutral" variant="ghost" @click="loadSummary(rule.rule_key)">重试</UButton>
      </p>
      <template v-else-if="summaries[rule.rule_key]">
        <p class="mt-3 text-sm text-pretty" data-testid="reminder-summary">
          {{ summaryText(rule, summaries[rule.rule_key]!) }}
          <span v-if="summaries[rule.rule_key]!.error_days" class="text-ink-red">有 {{ summaries[rule.rule_key]!.error_days }} 天没检查成<template v-if="summaries[rule.rule_key]!.last_error_date">（最近 {{ dayText(summaries[rule.rule_key]!.last_error_date!) }}）</template>。</span>
        </p>

        <div v-if="summaries[rule.rule_key]!.samples.length" class="mt-4">
          <p class="mb-1 text-meta text-muted">最近写下的几条</p>
          <ul class="divide-y divide-default border-y border-default">
            <li v-for="s in summaries[rule.rule_key]!.samples" :key="s.id" class="py-3 text-sm" :data-reminder="s.id">
              <p class="text-meta text-muted">{{ dayText(s.business_date) }} · 给{{ s.recipient_name }} · {{ s.ai_written ? 'AI 写的' : '固定文案' }}</p>
              <p class="mt-1 text-pretty">{{ s.text }}</p>
              <div class="mt-1 flex gap-1">
                <UButton size="xs" color="neutral" :variant="s.feedback === 'helpful' ? 'soft' : 'ghost'" icon="i-lucide-thumbs-up" :aria-pressed="s.feedback === 'helpful'" :aria-disabled="rating[s.id] || undefined" @click="rate(rule.rule_key, s.id, 'helpful')">有用</UButton>
                <UButton size="xs" color="neutral" :variant="s.feedback === 'not_helpful' ? 'soft' : 'ghost'" icon="i-lucide-thumbs-down" :aria-pressed="s.feedback === 'not_helpful'" :aria-disabled="rating[s.id] || undefined" @click="rate(rule.rule_key, s.id, 'not_helpful')">没用</UButton>
              </div>
            </li>
          </ul>
        </div>
      </template>
    </section>
  </div>
</template>
