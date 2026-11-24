import type { SpotArtName } from './app/utils/art'

declare module '#app' {
  interface PageMeta {
    // 访问页面所需的权限，见后端 perm.Catalogue；数组表示有其中任一项即可。
    permission?: string | string[]
    // 页面宽度（DESIGN.md 页面骨架）：不写就铺满；narrow 是设置类页面，居中、最宽 800px；
    // wide 是列很多的宽表（营业报表），大屏两侧留白收窄一半。工作台页同样收窄。
    width?: 'narrow' | 'wide'
    // 标题下一句灰字：这页管什么、怎么用。
    description?: string
    // 工作台（收银、预约）：顶栏面包屑已经写了页名，正文不再放大标题，高度留给内容；标题只留给读屏。
    workbench?: boolean
    // 标题上方的物件插画（DESIGN.md 插画）：名册、简报、核对类主页面放一张；有了它，页面里的空状态不再放插画（一屏最多一张）。
    art?: SpotArtName
  }
}

export {}
