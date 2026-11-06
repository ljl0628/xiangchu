// 管理后台 API
import type { ApiClient } from '../request'
import type { AdminInfo, AfterSale, Chef, Coupon, Dicts, GrantResult, Income, Order, PageData, PurchaseOrder, Review, Settlement, Supplier, UserCoupon, OperationLog, Withdrawal, Ingredient } from '../types'

export function createAdminApi(c: ApiClient) {
  return {
    login: (username: string, password: string) => c.post<{ token: string; role: string }>('/admin/auth/login', { username, password }),
    me: () => c.get<AdminInfo>('/admin/me'),
    dicts: () => c.get<Dicts>('/admin/dicts'),
    roles: () => c.get('/admin/roles'),
    operLogs: (params: { page?: number; size?: number; admin_id?: number; action?: string } = {}) => c.get<PageData<OperationLog>>('/admin/oper-logs', params),
    // 用户/乡厨/供应商管理
    users: (params: { keyword?: string; page?: number; size?: number } = {}) => c.get<PageData<Record<string, unknown>>>('/admin/users', params),
    setUserStatus: (id: number, status: number) => c.post(`/admin/users/${id}/status`, { status }),
    chefs: (params: { keyword?: string; status?: number; page?: number; size?: number } = {}) => c.get<PageData<Chef>>('/admin/chefs', params),
    auditChef: (id: number, approve: boolean, remark: string) => c.post(`/admin/chefs/${id}/audit`, { approve, remark }),
    suppliers: (params: { keyword?: string; status?: number; page?: number; size?: number } = {}) => c.get<PageData<Supplier>>('/admin/suppliers', params),
    auditSupplier: (id: number, approve: boolean, remark: string) => c.post(`/admin/suppliers/${id}/audit`, { approve, remark }),
    // 订单与售后
    orders: (params: { status?: number; keyword?: string; page?: number; size?: number } = {}) => c.get<PageData<Order>>('/admin/orders', params),
    order: (id: number) => c.get<Order>(`/admin/orders/${id}`),
    aftersales: (params: { status?: number; page?: number; size?: number } = {}) => c.get<PageData<AfterSale>>('/admin/aftersales', params),
    auditAftersale: (id: number, approve: boolean, remark: string) => c.post(`/admin/aftersales/${id}/audit`, { approve, remark }),
    reviews: (params: { page?: number; size?: number } = {}) => c.get<PageData<Review>>('/admin/reviews', params),
    // 采购
    purchases: (params: { status?: number; fulfill_method?: number; page?: number; size?: number } = {}) => c.get<PageData<PurchaseOrder>>('/admin/purchases', params),
    purchaseStats: () => c.get<{ total: number; pending: number; completed: number; by_fulfill: Record<string, number>; gmv: number }>('/admin/purchases/fulfill-stats'),
    // 财务
    settlements: (params: { role?: number; page?: number; size?: number } = {}) => c.get<PageData<Settlement>>('/admin/settlements', params),
    withdrawals: (params: { role?: number; status?: number; page?: number; size?: number } = {}) => c.get<PageData<Withdrawal>>('/admin/withdrawals', params),
    auditWithdrawal: (id: number, approve: boolean, remark: string) => c.post(`/admin/withdrawals/${id}/audit`, { approve, remark }),
    markWithdrawalPaid: (id: number) => c.post(`/admin/withdrawals/${id}/mark-paid`),
    settings: () => c.get<{ chef_bp: number; supplier_bp: number }>('/admin/settings'),
    updateSettings: (chef_bp: number, supplier_bp: number) => c.put('/admin/settings', { chef_bp, supplier_bp }),
    financeStats: () => c.get<{ gmv: number; settlement_count: number; settlement_amount: number; withdraw_pending: number }>('/admin/finance-stats'),
    // 订单统计（GMV/状态计数/复购率/区域分布，来自 order 模块）
    orderStats: () => c.get<{ gmv: number; total: number; pending: number; completed: number; repurchase_rate: number; regions: { region: string; count: number }[] }>('/admin/stats'),
    chefStats: () => c.get<{ total: number; pending: number }>('/admin/chefs/stats'),
    supplierStats: () => c.get<{ total: number; pending: number }>('/admin/suppliers/stats'),
    // 优惠券管理
    coupons: (params: { status?: number; page?: number; size?: number } = {}) => c.get<PageData<Coupon>>('/admin/coupons', params),
    createCoupon: (data: { name: string; type: number; threshold: number; amount: number; valid_from: string; valid_to: string; total_count: number }) => c.post<Coupon>('/admin/coupons', data),
    updateCoupon: (id: number, data: { name: string; type: number; threshold: number; amount: number; valid_from: string; valid_to: string; total_count: number }) => c.put<Coupon>(`/admin/coupons/${id}`, data),
    setCouponStatus: (id: number, status: number) => c.post(`/admin/coupons/${id}/status`, { status }),
    grantCoupon: (id: number, user_ids: number[]) => c.post<GrantResult>(`/admin/coupons/${id}/grant`, { user_ids }),
  }
}
