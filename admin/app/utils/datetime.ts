const WEEKDAYS = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']

// 时间点的显示：今天只写时分，其他日子写"11 月 20 日 周五 14:30"。
export function formatStamp(value: string, now = new Date()) {
  const date = new Date(value)
  const time = date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })
  if (date.toDateString() === now.toDateString()) return time
  return `${date.getMonth() + 1} 月 ${date.getDate()} 日 ${WEEKDAYS[date.getDay()]} ${time}`
}
