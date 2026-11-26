// 控件高只有三档（DESIGN.md「档位」）：xs、sm 紧凑 28，md 常规 36，lg、xl 前台 44。按钮和下拉用 min-h，内容折行时能撑高；
// 输入框单行，直接定高。
const compact = 'min-h-7 py-0.5 px-2 text-meta gap-1'
const regular = 'min-h-9 py-1 px-3 text-sm gap-2'
const front = 'min-h-11 py-2 px-4 text-base gap-2'
const sizes = { xs: { base: compact }, sm: { base: compact }, md: { base: regular }, lg: { base: front }, xl: { base: front } }
const fieldSizes = {
  xs: { base: 'h-7 py-0 px-2 text-meta gap-1' },
  sm: { base: 'h-7 py-0 px-2 text-meta gap-1' },
  md: { base: 'h-9 py-0 px-3 text-sm gap-2' },
  lg: { base: 'h-11 py-0 px-4 text-base gap-2' },
  xl: { base: 'h-11 py-0 px-4 text-base gap-2' }
}
const pickerSizes = {
  xs: { base: 'min-h-7 py-0.5 px-2 text-meta gap-1' },
  sm: { base: 'min-h-7 py-0.5 px-2 text-meta gap-1' },
  md: { base: 'min-h-9 py-1 px-3 text-sm gap-2' },
  lg: { base: 'min-h-11 py-2 px-4 text-base gap-2' },
  xl: { base: 'min-h-11 py-2 px-4 text-base gap-2' }
}

const numberSizes = Object.fromEntries(Object.entries(fieldSizes).map(([k, v]) => [k, v.base]))
// 只放图标的方形按钮：宽高相等，跟着三档走。
const squares = [
  { size: ['xs', 'sm'], square: true, class: 'size-7 min-h-0 p-0 justify-center' },
  { size: 'md', square: true, class: 'size-9 min-h-0 p-0 justify-center' },
  { size: ['lg', 'xl'], square: true, class: 'size-11 min-h-0 p-0 justify-center' }
]

const focusRing = { color: 'primary' as const, variant: ['outline' as const, 'subtle' as const], class: 'outline-primary/10 focus-visible:outline-2' }

export default defineAppConfig({
  ui: {
    // 让 tailwind-merge 认得本站的档位类名（main.css 的 @theme），否则 text-meta 会被当成文字颜色，
    // 和组件默认的 text-xs、rounded-lg、shadow-lg 并存，谁赢看字母序。
    tv: {
      twMergeConfig: {
        extend: {
          classGroups: {
            'font-size': [{ text: ['meta', 'figure', 'title'] }],
            'shadow': [{ shadow: ['line', 'paper', 'lift'] }],
            'rounded': [{ rounded: ['card'] }]
          }
        }
      }
    },
    colors: {
      // 主色实际是墨色，由 main.css 的 --ui-primary 定；这里的色板只是占位，和 ai 一样写 neutral。
      primary: 'neutral',
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
        td: 'h-[37px] px-2 py-2 text-sm text-default [&:not(:last-child)]:border-e [&:not(:last-child)]:border-default',
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
    // 弹窗和页面同一个密度（DESIGN.md「弹窗」）：控件、按钮和页面上一样大，不另放大；标题 16/600，标题栏和按钮栏各一道细线。
    // 默认宽 480，项目多的弹窗在页面上写 :ui="{ content: 'sm:max-w-[600px]' }"。
    modal: {
      slots: {
        content: 'divide-y divide-default sm:max-w-[480px]',
        header: 'min-h-0 px-6 py-4 sm:px-6',
        title: 'text-base font-semibold text-highlighted',
        description: 'mt-1 text-sm text-muted',
        body: 'px-6 py-5 sm:px-6 sm:py-5',
        footer: 'justify-end gap-2 px-6 py-3 sm:px-6',
        close: 'top-4 end-4 text-default focus-visible:ring-0 focus-visible:outline-2 focus-visible:outline-primary'
      },
      variants: {
        // 后面压一层淡墨（样稿 19、21 的 rgba(15,15,15,.32)），不用 Nuxt UI 默认的乳白雾。
        overlay: {
          true: { overlay: 'bg-scrim' }
        },
        fullscreen: {
          false: { content: 'rounded-card shadow-lift ring-0' }
        }
      }
    },
    // 表单字段照 Notion 的属性名：标签在上方，14px 灰字；说明和报错在下方 13px 小字；控件铺满一栏（main.css 的 .field-fill）。
    formField: {
      slots: {
        label: 'text-sm font-normal text-muted',
        container: 'field-fill mt-1',
        description: 'text-meta text-muted',
        help: 'mt-2 text-meta text-muted',
        error: 'mt-2 text-meta'
      }
    },
    // 侧滑面板（样稿 22）：一张拿起来的白纸，标题和正文之间不画线，后面的页面不变暗、能接着点别的行；按钮栏上方一道细线。
    slideover: {
      slots: {
        // 后面不变暗（样稿 22）：遮罩还在，点空白处关掉，只是透明。
        overlay: 'bg-transparent',
        content: 'sm:ring-0 sm:shadow-lift',
        header: 'min-h-0 px-6 pt-5 pb-0 sm:px-6',
        title: 'text-base font-semibold text-highlighted',
        description: 'mt-1 text-sm text-muted',
        body: 'px-6 py-5 sm:px-6 sm:py-5',
        footer: 'justify-end gap-2 border-t border-default px-6 py-3 sm:px-6'
      },
      // 样稿 22 的侧滑宽 480，和弹窗默认宽一样。
      variants: {
        side: {
          right: { content: 'sm:max-w-[480px]' }
        }
      }
    },
    // 菜单和弹出层（样稿 23）：白纸、拿起来的阴影、卡片圆角，分组之间一道细线；危险项红字放最后一组（各处 items 自己排）。
    // 内边距 8（样稿 .menu padding:8px）：菜单项 6 的圆角加 8 的边，和外面 12 的卡片圆角差不多同心。
    dropdownMenu: {
      slots: {
        content: 'rounded-card ring-0 shadow-lift p-2',
        group: 'p-0'
      }
    },
    popover: {
      slots: { content: 'rounded-card ring-0 shadow-lift' }
    },
    // 提示条（样稿 25）：墨色底白字一行，做完了说一声；出错是浅红底黑字，说清哪里不行。
    toast: {
      slots: {
        root: 'bg-inverted ring-0 shadow-lift rounded-card px-4 py-3',
        title: 'text-inverted',
        description: 'text-inverted/70',
        close: 'text-inverted/70 hover:text-inverted'
      },
      variants: {
        color: {
          primary: { icon: 'text-inverted' },
          neutral: { icon: 'text-inverted' },
          error: { root: 'bg-tag-red', title: 'text-highlighted', description: 'text-muted', close: 'text-muted hover:text-default' }
        }
      }
    },
    // 禁用的实心按钮变成灰底灰字，而不是半透明的主色：半透明看着像"能点但没反应"。
    // 描边按钮照 Notion 的次要按钮：白底深色字、一圈淡灰框，不出现主色框和主色字（没写 color 时默认是 primary）。
    // 实心主按钮（墨色）照 Notion 按钮的做法：一道内描边加 1px 阴影，看着压得住。
    // 行内的删除类文字按钮（去掉、撤销、删除）平时是灰字，悬停才变红，免得列表里一排红字。
    button: {
      variants: { size: sizes },
      compoundVariants: [
        ...squares,
        { color: ['primary', 'neutral'], variant: 'outline', class: 'bg-white text-default ring-accented hover:bg-hover active:bg-hover disabled:bg-white aria-disabled:bg-white' },
        { color: 'primary', variant: 'solid', class: 'shadow-[inset_0_0_0_1px_rgba(15,15,15,0.1)]' },
        { color: 'error', variant: 'ghost', class: 'text-muted hover:bg-hover hover:text-error active:bg-hover' },
        { color: 'primary', variant: 'solid', class: 'disabled:bg-elevated disabled:text-dimmed aria-disabled:bg-elevated aria-disabled:text-dimmed disabled:opacity-100 aria-disabled:opacity-100' },
        { color: 'ai', variant: 'solid', class: 'disabled:bg-elevated disabled:text-dimmed aria-disabled:bg-elevated aria-disabled:text-dimmed disabled:opacity-100 aria-disabled:opacity-100' }
      ]
    },
    // 输入类控件聚焦时照 Notion：细墨线加一圈很淡的光晕。Nuxt UI 默认是 3px 的主色光晕，主色是墨色，看着像一圈粗黑框。
    input: { variants: { size: fieldSizes }, compoundVariants: [focusRing] },
    textarea: {
      variants: {
        size: {
          xs: { base: 'px-2 py-1 text-meta gap-1' },
          sm: { base: 'px-2 py-1 text-meta gap-1' },
          md: { base: 'px-3 py-2 text-sm gap-2' },
          lg: { base: 'px-4 py-2 text-base gap-2' },
          xl: { base: 'px-4 py-2 text-base gap-2' }
        }
      },
      compoundVariants: [focusRing]
    },
    select: { variants: { size: pickerSizes }, compoundVariants: [focusRing] },
    selectMenu: { variants: { size: pickerSizes }, compoundVariants: [focusRing] },
    inputNumber: { variants: { size: numberSizes }, compoundVariants: [focusRing] },
    inputMenu: { variants: { size: pickerSizes }, compoundVariants: [focusRing] },
    card: {
      slots: {
        root: 'rounded-card'
      }
    }
  }
})
