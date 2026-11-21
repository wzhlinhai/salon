import type { HomeKind } from '~/utils/home'

export interface HomeSwitcherOption {
  label: string
  short: string
  value: HomeKind
}

// 首页把可选的首页放进顶栏右侧的分段切换；只在这几个首页同时存在时显示。
export function useHomeSwitcher() {
  const active = useState<boolean>('home-switcher-active', () => false)
  const options = useState<HomeSwitcherOption[]>('home-switcher-options', () => [])
  const selected = useState<HomeKind>('home-switcher-selected', () => 'personal')
  return { active, options, selected }
}
