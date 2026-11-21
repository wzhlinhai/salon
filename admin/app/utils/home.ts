// 首页按权限并集决定，从上往下取第一个符合的；个人首页只在没有其他首页时出现。
export type HomeKind = 'manager' | 'front' | 'personal'

export const homeLabels: Record<HomeKind, string> = {
  manager: '店长首页',
  front: '前台首页',
  personal: '个人首页'
}

// 顶栏分段切换里的短名。前台首页给兼接待、收银的助理用。
export const homeShortLabels: Record<HomeKind, string> = {
  manager: '店长',
  front: '前台',
  personal: '个人'
}

export function homeMatches(can: (permission: string) => boolean): HomeKind[] {
  const matches: HomeKind[] = []
  if (can('report.view')) matches.push('manager')
  if (can('payment.collect') || can('queue.operate')) matches.push('front')
  return matches.length ? matches : ['personal']
}
