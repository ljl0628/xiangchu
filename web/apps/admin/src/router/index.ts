import { createRouter, createWebHashHistory, type RouteRecordRaw } from 'vue-router'

import Main from '@/layout/Main.vue'

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/Login.vue'),
    meta: { title: '登录' },
  },
  {
    path: '/',
    component: Main,
    redirect: '/dashboard',
    children: [
      // ---------- 平台管理员 ----------
      {
        path: 'dashboard',
        name: 'dashboard',
        component: () => import('@/views/Dashboard.vue'),
        meta: { title: '仪表盘', roles: ['admin'] },
      },
      {
        path: 'users',
        name: 'users',
        component: () => import('@/views/Users.vue'),
        meta: { title: '用户管理', roles: ['admin'] },
      },
      {
        path: 'chefs',
        name: 'chefs',
        component: () => import('@/views/Chefs.vue'),
        meta: { title: '乡厨管理', roles: ['admin'] },
      },
      {
        path: 'suppliers',
        name: 'suppliers',
        component: () => import('@/views/Suppliers.vue'),
        meta: { title: '供应商管理', roles: ['admin'] },
      },
      {
        path: 'orders',
        name: 'orders',
        component: () => import('@/views/Orders.vue'),
        meta: { title: '订单管理', roles: ['admin'] },
      },
      {
        path: 'aftersales',
        name: 'aftersales',
        component: () => import('@/views/Aftersales.vue'),
        meta: { title: '售后管理', roles: ['admin'] },
      },
      {
        path: 'reviews',
        name: 'reviews',
        component: () => import('@/views/Reviews.vue'),
        meta: { title: '评价管理', roles: ['admin'] },
      },
      {
        path: 'purchases',
        name: 'purchases',
        component: () => import('@/views/Purchases.vue'),
        meta: { title: '采购管理', roles: ['admin'] },
      },
      {
        path: 'coupons',
        name: 'coupons',
        component: () => import('@/views/Coupons.vue'),
        meta: { title: '优惠券管理', roles: ['admin'] },
      },
      {
        path: 'finance/settlements',
        name: 'finance-settlements',
        component: () => import('@/views/Finance/Settlements.vue'),
        meta: { title: '结算记录', roles: ['admin'] },
      },
      {
        path: 'finance/withdrawals',
        name: 'finance-withdrawals',
        component: () => import('@/views/Finance/Withdrawals.vue'),
        meta: { title: '提现审核', roles: ['admin'] },
      },
      {
        path: 'finance/settings',
        name: 'finance-settings',
        component: () => import('@/views/Finance/Settings.vue'),
        meta: { title: '费率设置', roles: ['admin'] },
      },
      {
        path: 'oper-logs',
        name: 'oper-logs',
        component: () => import('@/views/OperLogs.vue'),
        meta: { title: '操作日志', roles: ['admin'] },
      },

      // ---------- 供应商工作台 ----------
      {
        path: 'sup/ingredients',
        name: 'sup-ingredients',
        component: () => import('@/views/Supplier/Ingredients.vue'),
        meta: { title: '商品管理', roles: ['supplier'] },
      },
      {
        path: 'sup/purchases',
        name: 'sup-purchases',
        component: () => import('@/views/Supplier/Purchases.vue'),
        meta: { title: '采购履约', roles: ['supplier'] },
      },
      {
        path: 'sup/finance',
        name: 'sup-finance',
        component: () => import('@/views/Supplier/Finance.vue'),
        meta: { title: '收入与提现', roles: ['supplier'] },
      },
    ],
  },
  {
    path: '/:pathMatch(.*)*',
    redirect: '/',
  },
]

// 按角色返回首页路径（登录后/越权时跳转）
export function roleHome(role: string): string {
  switch (role) {
    case 'supplier':
      return '/sup/ingredients'
    case 'chef':
      return '/sup/ingredients'
    default:
      return '/dashboard'
  }
}

const router = createRouter({
  // 使用 hash 模式：nginx 将路径代理到 Go 后端，SPA 走 /#/ 避免冲突
  history: createWebHashHistory(),
  routes,
})

// /login 为白名单，其余路由需要 token + 角色匹配
const WHITELIST = ['/login']

router.beforeEach((to) => {
  const token = localStorage.getItem('xc_admin_token')
  const role = localStorage.getItem('xc_admin_role') || ''
  if (WHITELIST.includes(to.path)) {
    if (token) return '/'
    return true
  }
  if (!token) {
    return { path: '/login', query: { redirect: to.fullPath } }
  }
  // 角色不匹配 → 跳转对应角色的首页
  const roles = to.meta.roles as string[] | undefined
  if (roles && !roles.includes(role)) {
    return roleHome(role)
  }
  return true
})

router.afterEach((to) => {
  const title = (to.meta.title as string | undefined) ?? ''
  document.title = title ? `${title} - 乡厨管理后台` : '乡厨管理后台'
})

export default router
