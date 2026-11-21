const WEEKDAYS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']

// 时间点的显示：今天只写时分，其他日子写"11 月 20 日 周五 14:30"。
export function formatStamp(value: string, now = new Date()) {
  const date = new Date(value)
  const time = date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })
  if (date.toDateString() === now.toDateString()) return time
  return `${date.getMonth() + 1} 月 ${date.getDate()} 日 ${WEEKDAYS[date.getDay()]} ${time}`
}

// 表格里的日期：门店时区的 2026-11-20（DESIGN.md 组件约定）。
export function formatDate(value: string) {
  return new Intl.DateTimeFormat('sv-SE', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(value))
}
