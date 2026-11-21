// 导航项。permission 为空表示登录即可见。分组和 emoji 图标照原型 J 的侧栏。
export const navItems = [
  { label: '首页', emoji: '🏠', to: '/', permission: '', group: '门店' },
  { label: '收银', emoji: '🧾', to: '/cashier', permission: 'order.create', group: '门店' },
  { label: '预约', emoji: '📅', to: '/bookings', permission: 'booking.manage', group: '门店' },
  { label: '顾客', emoji: '👤', to: '/customers', permission: 'customer.view', group: '门店' },
  { label: '员工', emoji: '🧑‍🎨', to: '/staff', permission: 'staff.manage', group: '员工' },
  { label: '考勤', emoji: '⏱', to: '/attendance', permission: 'attendance.manage', group: '员工' },
  { label: '工资表', emoji: '💰', to: '/payroll', permission: 'payroll.manage', group: '员工' },
  { label: '提成规则', emoji: '📐', to: '/commission', permission: 'settings.manage', group: '员工' },
  { label: '级别与岗位', emoji: '🪜', to: '/options', permission: 'staff.manage', group: '员工' },
  { label: '项目与价格', emoji: '✂️', to: '/items', permission: 'settings.manage', group: '项目与卡' },
  { label: '卡类型', emoji: '💳', to: '/card-types', permission: 'settings.manage', group: '项目与卡' },
  { label: '团购商品', emoji: '🎟', to: '/groupbuy-deals', permission: 'settings.manage', group: '项目与卡' },
  { label: '团购对账', emoji: '🔁', to: '/groupbuy-month', permission: 'report.view', group: '项目与卡' },
  { label: '角色与权限', emoji: '🔐', to: '/roles', permission: 'role.manage', group: '设置' },
  { label: '门店设置', emoji: '⚙️', to: '/settings', permission: '', group: '设置' }
]
