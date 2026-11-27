import type { components } from '~/utils/api'

type Aftersale = components['schemas']['Aftersale']
type RefundMethod = components['schemas']['RefundLine']['method']

// 售后单的几种叫法，看板、详情、开单弹窗共用。
export const aftersaleTypeLabels: Record<Aftersale['type'], string> = {
  repair: '修补',
  redo: '重做',
  partial_refund: '部分退款',
  full_refund: '全额退款'
}

export const aftersaleStatusLabels: Record<Aftersale['status'], string> = {
  pending: '待审批',
  approved: '已批准',
  rejected: '已拒绝'
}

export const refundMethodLabels: Record<RefundMethod, string> = {
  cash: '现金',
  unionpay: '银联',
  wechat: '微信',
  alipay: '支付宝',
  card_principal: '退回卡本金'
}

export function isRefundType(type: Aftersale['type']) {
  return type === 'partial_refund' || type === 'full_refund'
}

// 一张售后单要退的合计（按分加，免得小数误差）。
export function refundTotal(row: Pick<Aftersale, 'refund_lines'>) {
  return money(row.refund_lines.reduce((sum, line) => sum + cents(line.amount), 0))
}

// 原单的收款方式（详情里「原单」一行）。
export const orderPaymentLabels: Record<string, string> = {
  cash: '现金', unionpay: '银联', wechat: '微信', alipay: '支付宝', groupbuy: '团购', debt: '挂账', card: '储值卡', count: '扣次', red_packet: '红包'
}
