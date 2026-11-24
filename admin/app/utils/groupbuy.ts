import type { components } from '~/utils/api'

type Platform = components['schemas']['CreateDealInput']['platform']

// 团购平台：收银验券、团购商品登记共用一份名字。
export const platformItems: { label: string, value: Platform }[] = [{ label: '美团', value: 'meituan' }, { label: '抖音', value: 'douyin' }]

export function platformLabel(value: string) {
  return platformItems.find(p => p.value === value)?.label ?? value
}
