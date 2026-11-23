export default defineAppConfig({
  ui: {
    colors: {
      primary: 'blue',
      neutral: 'stone',
      ai: 'neutral',
      success: 'green',
      warning: 'amber',
      error: 'red',
      info: 'sky'
    },
    // 全站表格照 DESIGN.md"表格"模式（Notion 数据库）：不包白卡，直接放在页面上，细线分行；
    // 表头小号常规灰字、行紧凑、正文深色；有数据的行悬停变浅灰（空状态那一行只有一格，不变）。
    table: {
      slots: {
        th: 'px-3 py-2 text-xs font-normal text-dimmed',
        td: 'px-3 py-2.5 text-sm text-default',
        tbody: 'border-b border-default [&>tr:has(td+td)]:hover:bg-hover [&>tr]:data-[selectable=true]:hover:bg-hover [&>tr]:data-[selectable=true]:cursor-pointer',
        separator: 'bg-(--ui-border)',
        empty: 'text-center text-sm text-muted',
        loading: 'p-0'
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
