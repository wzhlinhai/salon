export default defineAppConfig({
  ui: {
    colors: {
      primary: 'blue',
      neutral: 'stone',
      ai: 'violet',
      success: 'green',
      warning: 'amber',
      error: 'red',
      info: 'sky'
    },
    // 全站表格照 DESIGN.md"表格"模式：表头小号常规字、行紧凑、正文深色。外框由页面用 box 包。
    table: {
      slots: {
        th: 'px-3 py-2 text-xs font-normal text-dimmed',
        td: 'px-3 py-2.5 text-sm text-default',
        tbody: '[&>tr]:data-[selectable=true]:cursor-pointer [&>tr]:data-[selectable=true]:hover:bg-hover',
        empty: 'text-center text-sm text-muted'
      }
    },
    card: {
      slots: {
        root: 'rounded-card'
      }
    }
  }
})
