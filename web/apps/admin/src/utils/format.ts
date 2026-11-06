// 通用格式化与状态映射
export type TagType = 'success' | 'warning' | 'danger' | 'info' | 'primary'

export interface StatusMeta {
  label: string
  type: TagType
}

// 金额：分 -> 元
export function money(fen?: number | null): string {
  return ((fen ?? 0) / 100).toFixed(2)
}

// 时间
export function fmtTime(val?: string | null): string {
  return val && val !== '0001-01-01T00:00:00Z' ? val.replace('T', ' ').slice(0, 19) : '-'
}

// 角色：1 乡厨 2 供应商
export const roleMap: Record<number, StatusMeta> = {
  1: { label: '乡厨', type: 'success' },
  2: { label: '供应商', type: 'primary' },
}

// 乡厨/供应商状态：0待审核 1已通过 2已拒绝 3已禁用
export const auditStatusMap: Record<number, StatusMeta> = {
  0: { label: '待审核', type: 'warning' },
  1: { label: '已通过', type: 'success' },
  2: { label: '已拒绝', type: 'danger' },
  3: { label: '已禁用', type: 'info' },
}

// 订单状态：0待支付 1待接单 2已接单 3备菜中 4服务中 5已完成 6已取消 7已拒绝
export const orderStatusMap: Record<number, StatusMeta> = {
  0: { label: '待支付', type: 'info' },
  1: { label: '待接单', type: 'warning' },
  2: { label: '已接单', type: 'primary' },
  3: { label: '备菜中', type: 'primary' },
  4: { label: '服务中', type: 'primary' },
  5: { label: '已完成', type: 'success' },
  6: { label: '已取消', type: 'info' },
  7: { label: '已拒绝', type: 'danger' },
}

// 退款状态：0无 1申请中 2已同意 3已拒绝 4已退款
export const refundStatusMap: Record<number, StatusMeta> = {
  0: { label: '无', type: 'info' },
  1: { label: '申请中', type: 'warning' },
  2: { label: '已同意', type: 'primary' },
  3: { label: '已拒绝', type: 'danger' },
  4: { label: '已退款', type: 'success' },
}

// 售后状态：0待审核 1已同意待退款 2已拒绝 3已退款
export const aftersaleStatusMap: Record<number, StatusMeta> = {
  0: { label: '待审核', type: 'warning' },
  1: { label: '已同意待退款', type: 'primary' },
  2: { label: '已拒绝', type: 'danger' },
  3: { label: '已退款', type: 'success' },
}

// 采购单状态：0待接单 1备货中 2待自取/待送达 3已完成 4已取消
export const purchaseStatusMap: Record<number, StatusMeta> = {
  0: { label: '待接单', type: 'warning' },
  1: { label: '备货中', type: 'primary' },
  2: { label: '待自取/待送达', type: 'primary' },
  3: { label: '已完成', type: 'success' },
  4: { label: '已取消', type: 'info' },
}

// 履约方式：1自取 2送货
export const fulfillMethodMap: Record<number, StatusMeta> = {
  1: { label: '自取', type: 'info' },
  2: { label: '送货', type: 'success' },
}

// 提现状态：0待审核 1已通过待打款 2已拒绝 3已打款
export const withdrawStatusMap: Record<number, StatusMeta> = {
  0: { label: '待审核', type: 'warning' },
  1: { label: '已通过待打款', type: 'primary' },
  2: { label: '已拒绝', type: 'danger' },
  3: { label: '已打款', type: 'success' },
}

// 提现渠道：1微信 2支付宝（未知则显示原值）
export const channelMap: Record<number, StatusMeta> = {
  1: { label: '微信', type: 'success' },
  2: { label: '支付宝', type: 'primary' },
}

// 评价状态：1显示 0隐藏
export const reviewStatusMap: Record<number, StatusMeta> = {
  0: { label: '已隐藏', type: 'info' },
  1: { label: '显示中', type: 'success' },
}

// 用户状态：1启用 0禁用
export const userStatusMap: Record<number, StatusMeta> = {
  0: { label: '已禁用', type: 'danger' },
  1: { label: '正常', type: 'success' },
}

// 优惠券状态：0已下架 1上架中
export const couponStatusMap: Record<number, StatusMeta> = {
  0: { label: '已下架', type: 'info' },
  1: { label: '上架中', type: 'success' },
}

export function statusMeta(map: Record<number, StatusMeta>, status?: number | null): StatusMeta {
  if (status === undefined || status === null || !(status in map)) {
    return { label: status === undefined || status === null ? '-' : String(status), type: 'info' }
  }
  return map[status]
}
