declare module '#app' {
  interface PageMeta {
    // 访问页面所需的权限，见后端 perm.Catalogue。
    permission?: string
  }
}

export {}
