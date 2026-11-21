// 导航项。permission 为空表示登录即可见；group 为空表示不显示组名。
export const navItems = [
  { label: '首页', icon: 'i-lucide-house', to: '/', permission: '', group: '' },
  { label: '收银', icon: 'i-lucide-scan-line', to: '/cashier', permission: 'order.create', group: '' },
  { label: '预约', icon: 'i-lucide-calendar-days', to: '/bookings', permission: 'booking.manage', group: '' },
  { label: '顾客', icon: 'i-lucide-contact', to: '/customers', permission: 'customer.view', group: '' },
  { label: '团购对账', icon: 'i-lucide-clipboard-check', to: '/groupbuy-month', permission: 'report.view', group: '经营' },
  { label: '员工', icon: 'i-lucide-users', to: '/staff', permission: 'staff.manage', group: '员工' },
  { label: '考勤', icon: 'i-lucide-clock', to: '/attendance', permission: 'attendance.manage', group: '员工' },
  { label: '工资表', icon: 'i-lucide-wallet', to: '/payroll', permission: 'payroll.manage', group: '员工' },
  { label: '提成规则', icon: 'i-lucide-percent', to: '/commission', permission: 'settings.manage', group: '员工' },
  { label: '级别与岗位', icon: 'i-lucide-layers', to: '/options', permission: 'staff.manage', group: '员工' },
  { label: '项目与价格', icon: 'i-lucide-scissors', to: '/items', permission: 'settings.manage', group: '项目与卡' },
  { label: '卡类型', icon: 'i-lucide-credit-card', to: '/card-types', permission: 'settings.manage', group: '项目与卡' },
  { label: '团购商品', icon: 'i-lucide-ticket', to: '/groupbuy-deals', permission: 'settings.manage', group: '项目与卡' },
  { label: '门店设置', icon: 'i-lucide-settings', to: '/settings', permission: '', group: '设置' },
  { label: '角色与权限', icon: 'i-lucide-shield', to: '/roles', permission: 'role.manage', group: '设置' }
]
