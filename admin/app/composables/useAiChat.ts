import type { DraftView } from '~/composables/useDrafts'

// 一条对话记录。sent 是实际发给 AI 的内容（修改草稿时带上草稿编号），content 是显示给人看的话。
// failed 的记录只显示，不进入发给 AI 的历史。
export interface ChatEntry {
  role: 'user' | 'assistant'
  content: string
  sent?: string
  editing?: string
  drafts?: DraftView[]
  failed?: boolean
}

// 正在用一句话修改的草稿。
export interface DraftEditing {
  id: string
  summary: string
}

const MAX_HISTORY = 20
export const MAX_MESSAGE = 4000

// AI 对话面板：侧栏搜索、首页指令框和草稿卡片的"修改"都往同一个右侧面板里说话，不另开页面。
export function useAiChat() {
  const api = useApi()
  const { me } = useMe()
  const { load: loadDrafts } = useDrafts()
  const open = useState<boolean>('ai-open', () => false)
  const entries = useState<ChatEntry[]>('ai-entries', () => [])
  const sending = useState<boolean>('ai-sending', () => false)
  const editing = useState<DraftEditing | null>('ai-editing', () => null)
  // 发出时记下号码；换人登录或清空对话会换号，旧回复到达时丢弃。
  const generation = useState<number>('ai-generation', () => 0)

  function history(): { role: 'user' | 'assistant', content: string }[] {
    const sent = entries.value
      .filter(entry => !entry.failed)
      .map(entry => ({ role: entry.role, content: entry.sent ?? entry.content }))
      .slice(-MAX_HISTORY)
    // 截断后从用户的话开始，免得第一条是没有上文的 AI 回复。
    while (sent.length && sent[0]!.role !== 'user') sent.shift()
    return sent
  }

  async function send(text: string) {
    const content = text.trim().slice(0, MAX_MESSAGE)
    if (!content || sending.value || !me.value) return
    const target = editing.value
    editing.value = null
    entries.value.push({
      role: 'user',
      content,
      sent: target ? `请修改草稿 ${target.id}（${target.summary}）：${content}`.slice(0, MAX_MESSAGE) : undefined,
      editing: target?.summary
    })
    const current = generation.value
    sending.value = true
    try {
      const { data, error } = await api.POST('/api/ai/chat', { body: { messages: history() } })
      if (current !== generation.value) return
      if (!data) {
        entries.value.push({ role: 'assistant', content: errorText(error), failed: true })
        // 失败的那句话也不进历史，重说一遍即可。
        const asked = entries.value.at(-2)
        if (asked) asked.failed = true
        return
      }
      entries.value.push({ role: 'assistant', content: data.reply, drafts: data.drafts })
      if (data.drafts.length) void loadDrafts()
    } finally {
      if (current === generation.value) sending.value = false
    }
  }

  // 打开面板并说一句话；没有话时只打开面板。
  function ask(text = '') {
    open.value = true
    if (text.trim()) void send(text)
  }

  // 草稿卡片上的"修改"：打开面板，下一句话用来修改这张草稿。
  function editDraft(draft: DraftEditing) {
    editing.value = { id: draft.id, summary: draft.summary }
    open.value = true
  }

  function reset() {
    generation.value++
    entries.value = []
    editing.value = null
    sending.value = false
    open.value = false
  }

  return { open, entries, sending, editing, send, ask, editDraft, reset }
}
