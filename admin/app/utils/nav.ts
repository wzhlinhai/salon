// 导航项。permission 为空表示登录即可见。分组照原型 J 的侧栏；图标是 Notion 式灰色线条图标（Lucide），见 DESIGN.md 图标一节。
export const navItems = [
  { label: '首页', icon: 'i-lucide-house', to: '/', permission: '', group: '门店' },
  { label: '收银', icon: 'i-lucide-receipt', to: '/cashier', permission: 'order.create', group: '门店' },
  { label: '预约', icon: 'i-lucide-calendar', to: '/bookings', permission: 'booking.manage', group: '门店' },
  { label: '轮牌', icon: 'i-lucide-list-ordered', to: '/rotation', permission: 'queue.view', group: '门店' },
  { label: '顾客', icon: 'i-lucide-user', to: '/customers', permission: 'customer.view', group: '门店' },
  { label: '员工', icon: 'i-lucide-users', to: '/staff', permission: 'staff.manage', group: '员工' },
  { label: '考勤', icon: 'i-lucide-clock', to: '/attendance', permission: 'attendance.manage', group: '员工' },
  { label: '工资表', icon: 'i-lucide-wallet', to: '/payroll', permission: 'payroll.manage', group: '员工' },
  { label: '提成规则', icon: 'i-lucide-percent', to: '/commission', permission: 'settings.manage', group: '员工' },
  { label: '级别与岗位', icon: 'i-lucide-layers', to: '/options', permission: 'staff.manage', group: '员工' },
  { label: '项目与价格', icon: 'i-lucide-scissors', to: '/items', permission: 'settings.manage', group: '项目与卡' },
  { label: '卡类型', icon: 'i-lucide-credit-card', to: '/card-types', permission: 'settings.manage', group: '项目与卡' },
  { label: '团购商品', icon: 'i-lucide-ticket', to: '/groupbuy-deals', permission: 'settings.manage', group: '项目与卡' },
  { label: '团购对账', icon: 'i-lucide-file-check', to: '/groupbuy-month', permission: 'report.view', group: '项目与卡' },
  { label: '角色与权限', icon: 'i-lucide-shield', to: '/roles', permission: 'role.manage', group: '设置' },
  { label: '门店设置', icon: 'i-lucide-settings', to: '/settings', permission: '', group: '设置' }
]

// 待确认（AI 起草等人确认）在侧栏、顶栏和区块标题里用同一个图标。
export const DRAFTS_ICON = 'i-lucide-inbox'
