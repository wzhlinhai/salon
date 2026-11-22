import type { components } from '~/utils/api'

type Commission = components['schemas']['Commission']

// 提成来源和计算基数的叫法，首页提成明细和工资表共用。
export const commissionSourceNames: Record<Commission['source'], string> = { item: '项目业绩', consume: '耗卡业绩', labor: '手工费', sale: '销售业绩' }
const baseNames: Record<Commission['explanation']['base']['kind'], string> = { line_price: '项目行价格（原价）', paid: '实收（折后价）', performance: '业绩', cash_performance: '现金业绩', card_performance: '卡金业绩' }

// 一笔提成怎么算出来的：基数 · 比例（或固定额、阶梯） · 方案版本。
export function commissionExplanation(c: Commission) {
  const e = c.explanation
  const match = e.match.override ?? e.match.cell
  const value = e.tier ? `${e.tier.mode === 'whole' ? '整笔跳档' : '超额累进'}，从 ${e.tier.level.from} 元起 ${e.tier.level.percent}%` : match?.percent !== undefined ? `${match.percent}%` : match?.fixed_amount !== undefined ? `固定 ${match.fixed_amount} 元` : '不计提成'
  return `${baseNames[e.base.kind]} ${e.base.amount} · ${value} · 方案第 ${e.version.version} 版`
}
