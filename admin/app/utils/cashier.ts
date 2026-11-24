import type { components } from '~/utils/api'

type Order = components['schemas']['Order']

// 收银页几块共用的小工具。金额按分算，免得小数相加出误差。
export function cents(value: string) {
  const n = Number(value || '0')
  return Number.isFinite(n) ? Math.round(n * 100) : 0
}

export function money(value: number) {
  const sign = value < 0 ? '-' : ''
  const absolute = Math.abs(value)
  return `${sign}${Math.trunc(absolute / 100)}.${String(absolute % 100).padStart(2, '0')}`
}

export function orderItemNames(order: Order) {
  return order.items.filter(i => !i.removed_at).map(i => i.item_name).join('、') || '无项目'
}

// 挂单下拉的一行：顾客 · 项目 · 应收。
export function openOrderOption(order: Order, customerName: (id: string) => string) {
  return { label: `${customerName(order.customer_id)} · ${orderItemNames(order)} · 应收 ${order.receivable}`, value: order.id }
}

// 每做完一步把光标放回扫码框，扫码枪接着就能扫下一张。
export function focusScan() {
  document.querySelector<HTMLInputElement>('input[aria-label="扫码或输入手机号、券码"]')?.focus()
}

// 开单表单的项目行转成 open_order 的 items：只带选了人的岗位。
export function orderItemsInput(rows: { item_id: string, staff: Record<number, string> }[], items: { id: string, positions: { slot: number }[] }[]) {
  return rows.filter(r => r.item_id).map(row => ({
    item_id: row.item_id,
    staff: [...(items.find(i => i.id === row.item_id)?.positions ?? [])]
      .sort((a, b) => a.slot - b.slot)
      .filter(p => row.staff[p.slot])
      .map(p => ({ slot: p.slot, staff_id: row.staff[p.slot]! }))
  }))
}
