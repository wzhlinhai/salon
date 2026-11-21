declare module '#app' {
  interface PageMeta {
    // 访问页面所需的权限，见后端 perm.Catalogue。
    permission?: string
    // 页面宽度（DESIGN.md 页面骨架）：不写就铺满；narrow 是设置类页面，居中、最宽 800px。
    width?: 'narrow'
    // 标题下一句灰字：这页管什么、怎么用。
    description?: string
  }
}

export {}
