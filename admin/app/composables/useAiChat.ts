import type { DraftView } from '~/composables/useDrafts'

// 一条对话记录。content 是显示给人看的话；修改草稿时发给 AI 的话另带草稿编号，由服务端存成 sent。
// failed 的记录只在本页显示，服务端没有保存（AI 没回成什么都不存）。
// id 是服务端保存的消息编号，人说的话靠它撤回；没发成的话没有。
export interface ChatEntry {
  id?: string
  role: 'user' | 'assistant'
  content: string
  editing?: string
  drafts?: DraftView[]
  failed?: boolean
}

// 正在用一句话修改的草稿。
export interface DraftEditing {
  id: string
  summary: string
}

export const MAX_MESSAGE = 4000
const EXPANDED_KEY = 'salon-ai-expanded'

// AI 对话面板：侧栏搜索、首页指令框和草稿卡片的"修改"都往同一个右侧面板里说话，不另开页面。
// 对话由服务端按人保存：每次只发这一句，前文由服务端从这个对话里取；历史对话可以打开接着聊。
export function useAiChat() {
  const api = useApi()
  const { me } = useMe()
  const { load: loadDrafts } = useDrafts()
  const open = useState<boolean>('ai-open', () => false)
  const entries = useState<ChatEntry[]>('ai-entries', () => [])
  const sending = useState<boolean>('ai-sending', () => false)
  const editing = useState<DraftEditing | null>('ai-editing', () => null)
  // 当前对话；null 是还没说话的新对话，第一句回来后由服务端给出。
  const conversationId = useState<string | null>('ai-conversation', () => null)
  const title = useState<string>('ai-title', () => '')
  const loadingConversation = useState<boolean>('ai-loading-conversation', () => false)
  // 正在撤回：这时不发新话，免得新话接在要删的消息后面。
  const rewinding = useState<boolean>('ai-rewinding', () => false)
  // 全屏还是侧栏，记在本机。
  const expanded = useState<boolean>('ai-expanded', () => false)
  // 面板输入框里的字。AI 还在回上一句时，从别处说的话先放在这里，不丢。
  const input = useState<string>('ai-input', () => '')
  // 发出或打开时记下号码；换人登录、开新对话、换到别的对话都会换号，旧结果到达时丢弃。
  const generation = useState<number>('ai-generation', () => 0)

  async function send(text: string) {
    const content = text.trim().slice(0, MAX_MESSAGE)
    if (!content || sending.value || loadingConversation.value || rewinding.value || !me.value) return
    const target = editing.value
    editing.value = null
    // 记下这句的位置：回话回来时给它补上消息编号（发出期间不能撤回，位置不会变）。
    const at = entries.value.push({ role: 'user', content, editing: target?.summary }) - 1
    const current = generation.value
    sending.value = true
    try {
      // draft_id 让后端允许这次对话改这张（之前起草的）草稿；不带时 AI 只能改本次对话起草的。
      const { data, error } = await api.POST('/api/ai/chat', {
        body: {
          conversation_id: conversationId.value ?? undefined,
          content,
          sent: target ? `请修改草稿 ${target.id}（${target.summary}）：${content}`.slice(0, MAX_MESSAGE) : undefined,
          editing: target?.summary,
          draft_id: target?.id
        }
      })
      if (current !== generation.value) return
      if (!data) {
        entries.value.push({ role: 'assistant', content: errorText(error), failed: true })
        // 没回成的那句服务端没存，标成失败，重说一遍即可。
        const asked = entries.value[at]
        if (asked) asked.failed = true
        // 修改草稿那句没发成，重说时仍然是在改这张草稿。
        if (target && !editing.value) editing.value = target
        return
      }
      conversationId.value = data.conversation_id ?? null
      title.value = data.title ?? ''
      const saved = entries.value[at]
      if (saved) saved.id = data.message_id
      entries.value.push({ role: 'assistant', content: data.reply, drafts: data.drafts })
      if (data.drafts.length || target) void loadDrafts()
    } finally {
      if (current === generation.value) sending.value = false
    }
  }

  // 打开面板并说一句话；没有话时只打开面板。AI 还在回上一句、或旧对话还在打开时，这句话放进输入框等人再发。
  function ask(text = '') {
    open.value = true
    if (!text.trim()) return
    if (sending.value || loadingConversation.value) {
      input.value = text
      return
    }
    void send(text)
  }

  // 草稿卡片上的"修改"：打开面板，下一句话用来修改这张草稿。
  function editDraft(draft: DraftEditing) {
    editing.value = { id: draft.id, summary: draft.summary }
    open.value = true
  }

  // 清掉面板里的对话（不动服务端），之前发出还没回来的结果作废。
  function clear() {
    generation.value++
    entries.value = []
    editing.value = null
    conversationId.value = null
    title.value = ''
    sending.value = false
    loadingConversation.value = false
    rewinding.value = false
  }

  // 新对话：面板留着，输入框里的字也留着。
  function startNew() {
    clear()
  }

  // 打开一个历史对话，接着聊。
  async function openConversation(id: string) {
    clear()
    conversationId.value = id
    const current = generation.value
    loadingConversation.value = true
    try {
      const { data, error } = await api.POST('/api/ops/get_ai_conversation', { body: { id } })
      if (current !== generation.value) return
      if (!data) {
        conversationId.value = null
        entries.value = [{ role: 'assistant', content: errorText(error), failed: true }]
        return
      }
      title.value = data.title
      entries.value = data.messages.map(m => ({
        id: m.id,
        role: m.role === 'user' ? 'user' : 'assistant',
        content: m.content,
        editing: m.editing ?? undefined,
        drafts: m.drafts
      }))
    } finally {
      if (current === generation.value) loadingConversation.value = false
    }
  }

  // 撤回人说的一句话：服务端删掉这句和之后的全部消息（含 AI 的回话和别的话），草稿照旧留在待确认。
  // 一句不剩时服务端连对话一起删，面板回到新对话。成功返回 true，出错时提示原因。
  async function rewind(index: number): Promise<boolean> {
    const entry = entries.value[index]
    const id = conversationId.value
    if (!entry?.id || entry.role !== 'user' || !id || sending.value || loadingConversation.value || rewinding.value) return false
    const current = generation.value
    rewinding.value = true
    let result
    try {
      result = await api.POST('/api/ops/rewind_ai_conversation', { body: { id, message_id: entry.id } })
    } finally {
      if (current === generation.value) rewinding.value = false
    }
    if (current !== generation.value) return false
    const { data, error } = result
    if (!data) {
      useToast().add({ title: errorText(error), color: 'error' })
      return false
    }
    // 正在改的草稿不受影响：撤回不删草稿，「修改：…」照旧留着。
    entries.value = entries.value.slice(0, index)
    if (data.emptied) {
      conversationId.value = null
      title.value = ''
    }
    return true
  }

  // 修改人说的一句话：先撤回，再把原话放回输入框，人改完重发。输入框里已经打了字的，原话放前面，打的字留着。
  async function revise(index: number): Promise<boolean> {
    const content = entries.value[index]?.content ?? ''
    if (!(await rewind(index))) return false
    const typed = input.value.trim()
    input.value = typed ? `${content}\n${typed}` : content
    return true
  }

  // 本机存不了（隐私模式等）也照样切换，只是下次不记得。
  function setExpanded(value: boolean) {
    expanded.value = value
    try {
      localStorage.setItem(EXPANDED_KEY, value ? '1' : '0')
    } catch {
      // 不记也行。
    }
  }

  function restoreExpanded() {
    expanded.value = localStorage.getItem(EXPANDED_KEY) === '1'
  }

  // 换人登录或退出：清空对话、关上面板。
  function reset() {
    clear()
    input.value = ''
    open.value = false
  }

  return { open, entries, sending, editing, input, conversationId, title, loadingConversation, rewinding, expanded, send, ask, editDraft, startNew, openConversation, rewind, revise, setExpanded, restoreExpanded, reset }
}
