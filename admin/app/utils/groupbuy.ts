import type { components } from '~/utils/api'

type Platform = components['schemas']['CreateDealInput']['platform']

// 团购平台：收银验券、团购商品登记共用一份名字。
export const platformItems: { label: string, value: Platform }[] = [{ label: '美团', value: 'meituan' }, { label: '抖音', value: 'douyin' }]

export function platformLabel(value: string) {
  return platformItems.find(p => p.value === value)?.label ?? value
}

// 券和核销单的状态（验券结果 unused / redeemed / revoked / invalid，核销单 pending / unknown / redeemed / revoked / failed）。
const couponStatusLabels: Record<string, string> = {
  unused: '未使用',
  redeemed: '已核销',
  revoked: '已撤销',
  invalid: '券无效',
  pending: '核销中',
  unknown: '结果未知',
  failed: '核销失败'
}

export function couponStatusLabel(value: string) {
  return couponStatusLabels[value] ?? value
}
