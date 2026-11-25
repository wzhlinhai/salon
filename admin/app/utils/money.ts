// 指标卡上的大金额：整数不带小数，有角分才显示两位（¥1,436、¥668、¥12.50）。表格里的金额另按两位小数右对齐。
export function yuan(value: string | number | null | undefined) {
  const n = Number(value || '0')
  return `¥${n.toLocaleString('zh-CN', {
    minimumFractionDigits: Number.isInteger(n) ? 0 : 2,
    maximumFractionDigits: 2
  })}`
}

// 列表和表格里右对齐的金额：一律两位小数，数位对齐（¥99.00、¥1,234.56）。
export function yuanFixed(value: string | number | null | undefined) {
  return `¥${Number(value || '0').toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}

// 指标是零时数字用浅灰（DESIGN.md 首页）：一早还没开张时，一排黑色的 ¥0 像出了问题。
export function zeroText(value: string | number | null | undefined) {
  return /^[¥-]?0(\.0+)?$/.test(String(value ?? '').trim())
}

// 报表明细表身里的金额：零写成一道长横，整数不带 .00（DESIGN.md 报表），一屏 ¥0.00 时有数的格子才跳得出来。
export function yuanCell(value: string | number | null | undefined) {
  return zeroText(value) ? '—' : yuan(value)
}
