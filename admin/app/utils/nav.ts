// 导航项。permission 为空表示登录即可见。
export const navItems = [
  { label: '首页', icon: 'i-lucide-house', to: '/', permission: '' },
  { label: '员工', icon: 'i-lucide-users', to: '/staff', permission: 'staff.manage' },
  { label: '级别与岗位', icon: 'i-lucide-layers', to: '/options', permission: 'staff.manage' },
  { label: '角色与权限', icon: 'i-lucide-shield', to: '/roles', permission: 'role.manage' },
  { label: '门店设置', icon: 'i-lucide-settings', to: '/settings', permission: 'settings.manage' }
]
