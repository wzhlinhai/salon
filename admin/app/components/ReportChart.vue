<script setup lang="ts">
// 报表走势图（DESIGN.md 图表）：折线看走势。比大小、看构成用 ReportRank、ReportMix，不用柱状图。echarts/core 按需引入，只带折线和用到的组件。
// 规则照 dataviz：一根数值轴；系列色按染发色卡 --color-dye-1… 顺序取；两个以上系列才有图例；线 2px；
// 网格细实线；悬停一条竖线，提示框列出这一格所有系列，数在前、名在后。
// 图只是看个走势，每张图下面都有明细表，数字以表为准，读屏也读表。
import { use, init, type ECharts } from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
import { SVGRenderer } from 'echarts/renderers'

use([LineChart, GridComponent, LegendComponent, TooltipComponent, SVGRenderer])

const props = withDefaults(defineProps<{
  label: string
  categories: string[]
  series: { name: string, values: number[] }[]
  money?: boolean
}>(), { money: true })

const el = ref<HTMLDivElement | null>(null)
let chart: ECharts | null = null
let observer: ResizeObserver | null = null

function cssVar(name: string) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
}

function escapeHTML(text: string) {
  return text.replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', '\'': '&#39;' })[c]!)
}

// 坐标轴上的金额：整数，一万以上写"1.2万"。
function axisMoney(value: number) {
  if (!props.money) return value.toLocaleString('zh-CN')
  if (Math.abs(value) >= 10_000) return `¥${(value / 10_000).toLocaleString('zh-CN', { maximumFractionDigits: 1 })}万`
  return `¥${value.toLocaleString('zh-CN')}`
}

function tipValue(value: number) {
  return props.money ? yuanFixed(value) : value.toLocaleString('zh-CN', { maximumFractionDigits: 1 })
}

function option() {
  const ink = cssVar('--ui-text')
  const muted = cssVar('--ui-text-muted')
  const dimmed = cssVar('--ui-text-dimmed')
  const grid = cssVar('--chart-grid')
  const colors = [1, 2, 3, 4, 5].map(i => cssVar(`--color-dye-${i}`))
  const font = getComputedStyle(document.body).fontFamily
  const single = props.categories.length === 1
  const legend = props.series.length > 1
  const categoryAxis = {
    type: 'category' as const,
    data: props.categories,
    axisTick: { show: false },
    axisLine: { lineStyle: { color: grid } },
    axisLabel: { color: dimmed, fontSize: 12, hideOverlap: true }
  }
  const valueAxis = {
    type: 'value' as const,
    splitLine: { lineStyle: { color: grid, width: 1, type: 'solid' as const } },
    axisLabel: { color: dimmed, fontSize: 12, formatter: axisMoney }
  }
  return {
    color: colors,
    animation: false,
    textStyle: { fontFamily: font },
    grid: { left: 8, right: 16, top: legend ? 36 : 12, bottom: 4, containLabel: true },
    legend: legend
      ? { show: true, left: 0, top: 0, itemWidth: 14, itemHeight: 3, icon: 'path://M0,0H14V3H0Z', itemGap: 16, textStyle: { color: muted, fontSize: 12 } }
      : { show: false },
    tooltip: {
      trigger: 'axis' as const,
      axisPointer: { type: 'line' as const, lineStyle: { color: dimmed, width: 1 } },
      backgroundColor: '#fff',
      borderColor: grid,
      borderWidth: 1,
      padding: [8, 10],
      textStyle: { color: ink, fontSize: 12 },
      formatter: (params: unknown) => {
        const list = (Array.isArray(params) ? params : [params]) as { axisValueLabel: string, seriesName: string, value: number, color: string }[]
        const head = `<div style="color:${dimmed};margin-bottom:4px">${escapeHTML(list[0]?.axisValueLabel ?? '')}</div>`
        const rows = list.map(p => `<div style="display:flex;align-items:center;gap:6px"><span style="display:inline-block;width:10px;height:2px;background:${p.color}"></span><b style="font-weight:600;font-variant-numeric:tabular-nums">${tipValue(p.value)}</b><span style="color:${muted}">${escapeHTML(p.seriesName)}</span></div>`)
        return head + rows.join('')
      }
    },
    xAxis: categoryAxis,
    yAxis: valueAxis,
    series: props.series.map(s => ({ type: 'line' as const, name: s.name, data: s.values, lineStyle: { width: 2, cap: 'round' as const, join: 'round' as const }, showSymbol: single, symbol: 'circle', symbolSize: 8, itemStyle: { borderColor: '#fff', borderWidth: 2 }, emphasis: { focus: 'none' as const, scale: false } }))
  }
}

function render() {
  chart?.setOption(option(), true)
}

onMounted(() => {
  if (!el.value) return
  chart = init(el.value, undefined, { renderer: 'svg' })
  render()
  observer = new ResizeObserver(() => chart?.resize())
  observer.observe(el.value)
})

watch(() => [props.categories, props.series], render, { deep: true })

onBeforeUnmount(() => {
  observer?.disconnect()
  chart?.dispose()
  chart = null
})
</script>

<template>
  <div ref="el" role="img" :aria-label="label" class="h-64 w-full" data-testid="report-chart" />
</template>
