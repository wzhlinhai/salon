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
    // 全站表格照 Notion 表格视图（DESIGN.md「Notion 实测值」）：不包白卡，直接放在页面上；表头 14px 常规灰字、高 36，
    // 行高约 37、左右内边距 8；横线竖线同一种细线，表头上方不画线；有数据的行悬停变浅（空状态那一行只有一格，不变）。
    table: {
      slots: {
        th: 'h-9 px-2 py-0 text-sm font-normal text-muted [&:not(:last-child)]:border-e [&:not(:last-child)]:border-default',
        td: 'h-[37px] px-2 py-1.5 text-sm text-default [&:not(:last-child)]:border-e [&:not(:last-child)]:border-default',
        tbody: 'border-b border-default [&>tr:has(td+td)]:hover:bg-hover [&>tr]:data-[selectable=true]:hover:bg-hover [&>tr]:data-[selectable=true]:cursor-pointer',
        separator: 'bg-(--ui-border)',
        empty: 'text-center text-sm text-muted border-e-0',
        loading: 'p-0'
      }
    },
    // 分页照 Notion 的视图页签：14px 中等字重灰字，选中的换深色字加灰底胶囊，没有下划线。
    tabs: {
      variants: {
        variant: {
          link: {
            list: 'gap-0.5 border-0',
            indicator: 'hidden',
            trigger: 'h-8 rounded-full font-medium text-muted hover:bg-hover hover:text-default data-[state=active]:bg-selected data-[state=active]:text-default'
          }
        }
      },
      // 横排 link 自带一条底线（组合变体），Notion 的视图页签下面没有线。
      compoundVariants: [{ orientation: 'horizontal', variant: 'link', class: { list: 'border-b-0 mb-0' } }]
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
