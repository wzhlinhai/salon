// 未登录跳到登录页；页面声明了所需权限而当前员工没有时，跳到无权限页。
export default defineNuxtRouteMiddleware(async (to) => {
  if (to.path === '/login') return
  if (!getToken()) return navigateTo('/login')
  const { me, load, can } = useMe()
  if (!me.value && !(await load())) return navigateTo('/login')
  const permission = to.meta.permission
  if (permission && !can(permission)) return navigateTo('/forbidden')
})
