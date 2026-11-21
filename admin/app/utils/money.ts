// 指标卡上的大金额：整数不带小数，有角分才显示两位（¥1,436、¥668、¥12.50）。表格里的金额另按两位小数右对齐。
export function yuan(value: string | number | null | undefined) {
  const n = Number(value || '0')
  return `¥${n.toLocaleString('zh-CN', {
    minimumFractionDigits: Number.isInteger(n) ? 0 : 2,
    maximumFractionDigits: 2
  })}`
}
