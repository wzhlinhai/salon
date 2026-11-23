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
    // 弹窗照 Fresha 的弹窗（DESIGN.md「弹窗」，参考图 picked/modal-01-fresha-time-off.png）：标题栏和按钮栏各一道细线，
    // 标题 18/600；内容区的表单控件放大到 44 高、8 圆角、灰框（main.css 的 .dialog-form）；按钮栏的按钮 44 高。
    // 默认宽 480，项目多的弹窗在页面上写 :ui="{ content: 'sm:max-w-[600px]' }"。
    modal: {
      slots: {
        content: 'divide-y divide-default sm:max-w-[480px]',
        header: 'min-h-0 px-6 py-5 sm:px-6',
        title: 'text-lg font-semibold text-highlighted',
        description: 'mt-1 text-sm text-muted',
        body: 'dialog-form px-6 py-6 sm:px-6 sm:py-6',
        footer: 'dialog-actions justify-end gap-3 px-6 py-4 sm:px-6',
        close: 'top-4 end-4 text-default focus-visible:outline-0 focus-visible:ring-0 focus-visible:bg-hover'
      },
      variants: {
        fullscreen: {
          false: { content: 'rounded-card shadow-float ring-0' }
        }
      }
    },
    // 表单字段照 Fresha：标签在上方，14px 半粗墨色字；说明和报错在下方 13px 小字；控件铺满一栏（main.css 的 .field-fill）。
    formField: {
      slots: {
        label: 'text-sm font-semibold text-highlighted',
        container: 'field-fill mt-2',
        description: 'text-meta text-muted',
        help: 'mt-1.5 text-meta text-muted',
        error: 'mt-1.5 text-meta'
      }
    },
    // 侧滑面板和弹窗同一套（Fresha）：标题栏、按钮栏各一道细线，内容区表单控件放大。
    slideover: {
      slots: {
        content: 'divide-y divide-default sm:ring-0 sm:shadow-float',
        header: 'min-h-0 px-6 py-5 sm:px-6',
        title: 'text-lg font-semibold text-highlighted',
        description: 'mt-1 text-sm text-muted',
        body: 'dialog-form px-6 py-6 sm:px-6 sm:py-6',
        footer: 'dialog-actions justify-end gap-3 px-6 py-4 sm:px-6'
      }
    },
    // 禁用的实心按钮变成灰底灰字，而不是半透明的蓝：半透明蓝看着像"能点但没反应"。
    // 描边按钮一律照 Fresha 的取消按钮：白底深色字、一圈灰框，不出现蓝框蓝字（没写 color 时默认是 primary）。
    // 实心主按钮照 Notion 的蓝按钮：一道内描边加 1px 阴影，看着压得住。
    // 行内的删除类文字按钮（去掉、撤销、删除）平时是灰字，悬停才变红，免得列表里一排红字。
    button: {
      compoundVariants: [
        { color: ['primary', 'neutral'], variant: 'outline', class: 'bg-white text-default ring-[#d6d3d1] hover:bg-hover active:bg-hover disabled:bg-white aria-disabled:bg-white' },
        { color: 'primary', variant: 'solid', class: 'shadow-[inset_0_0_0_1px_rgba(15,15,15,0.1),0_1px_2px_rgba(15,15,15,0.1)]' },
        { color: 'error', variant: 'ghost', class: 'text-muted hover:bg-hover hover:text-error active:bg-hover' },
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
