// 报表入口：有其中任一权限就能进，里面按每张报表各自的权限列出（发型师有 performance.view，只看得到员工业绩）。
export const REPORT_PERMISSIONS = ['report.view', 'performance.view']

// 导航项。permission 为空表示登录即可见。分组照原型 J 的侧栏；图标是 Notion 式灰色线条图标（Lucide），见 DESIGN.md 图标一节。
export const navItems: { label: string, icon: string, to: string, permission: string | string[], group: string }[] = [
  { label: '首页', icon: 'i-lucide-house', to: '/', permission: '', group: '门店' },
  { label: '收银', icon: 'i-lucide-receipt', to: '/cashier', permission: 'order.create', group: '门店' },
  { label: '预约', icon: 'i-lucide-calendar', to: '/bookings', permission: 'booking.manage', group: '门店' },
  { label: '轮牌', icon: 'i-lucide-list-ordered', to: '/rotation', permission: 'queue.view', group: '门店' },
  { label: '顾客', icon: 'i-lucide-user', to: '/customers', permission: 'customer.view', group: '门店' },
  { label: '日结', icon: 'i-lucide-calculator', to: '/closing', permission: 'closing.daily', group: '经营' },
  { label: '售后', icon: 'i-lucide-undo-2', to: '/aftersales', permission: 'aftersales.manage', group: '经营' },
  { label: '报表', icon: 'i-lucide-chart-column', to: '/reports', permission: REPORT_PERMISSIONS, group: '经营' },
  { label: '团购对账', icon: 'i-lucide-file-check', to: '/groupbuy-month', permission: 'report.view', group: '经营' },
  { label: '员工', icon: 'i-lucide-users', to: '/staff', permission: 'staff.manage', group: '员工' },
  { label: '考勤', icon: 'i-lucide-clock', to: '/attendance', permission: 'attendance.manage', group: '员工' },
  { label: '工资表', icon: 'i-lucide-wallet', to: '/payroll', permission: 'payroll.manage', group: '员工' },
  { label: '提成规则', icon: 'i-lucide-percent', to: '/commission', permission: 'settings.manage', group: '员工' },
  { label: '级别与岗位', icon: 'i-lucide-layers', to: '/options', permission: 'staff.manage', group: '员工' },
  { label: '项目与价格', icon: 'i-lucide-scissors', to: '/items', permission: 'settings.manage', group: '项目与卡' },
  { label: '卡类型', icon: 'i-lucide-credit-card', to: '/card-types', permission: 'settings.manage', group: '项目与卡' },
  { label: '团购商品', icon: 'i-lucide-ticket', to: '/groupbuy-deals', permission: 'settings.manage', group: '项目与卡' },
  { label: '角色与权限', icon: 'i-lucide-shield', to: '/roles', permission: 'role.manage', group: '设置' },
  { label: 'AI 提醒', icon: 'i-lucide-bell-ring', to: '/ai-reminders', permission: 'settings.manage', group: '设置' },
  { label: '门店设置', icon: 'i-lucide-settings', to: '/settings', permission: '', group: '设置' }
]

// 待确认（AI 起草等人确认）在侧栏、顶栏和区块标题里用同一个图标。
export const DRAFTS_ICON = 'i-lucide-inbox'

// 我的提醒（门店 AI 写给本人的话）在侧栏、顶栏和首页用同一个图标。
export const REMINDERS_ICON = 'i-lucide-bell'
