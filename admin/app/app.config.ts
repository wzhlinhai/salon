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
    // 禁用的实心按钮变成灰底灰字，而不是半透明的蓝：半透明蓝看着像"能点但没反应"。
    button: {
      compoundVariants: [
        { color: 'primary', variant: 'solid', class: 'disabled:bg-elevated disabled:text-dimmed aria-disabled:bg-elevated aria-disabled:text-dimmed disabled:opacity-100 aria-disabled:opacity-100' },
        { color: 'ai', variant: 'solid', class: 'disabled:bg-elevated disabled:text-dimmed aria-disabled:bg-elevated aria-disabled:text-dimmed disabled:opacity-100 aria-disabled:opacity-100' }
      ]
    },
    card: {
      slots: {
        root: 'rounded-card'
      }
    }
  }
})
