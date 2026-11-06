// 领域类型定义（金额单位均为分，费率单位 1/10000）
export interface R<T = unknown> {
  code: number
  msg: string
  data: T
}

export interface PageData<T> {
  list: T[]
  total: number
  page: number
  size: number
}

// ---------- 用户域 ----------
export interface LoginResult {
  token: string
  role: 'user' | 'chef' | 'supplier' | 'admin'
  user_id: number
  is_new: boolean
}

export interface ProfileVO {
  id: number
  phone: string
  nickname: string
  avatar: string
  roles?: string[]
}

export interface Address {
  id: number
  contact_name: string
  contact_phone: string
  region: string
  address: string
  is_default: boolean
}

export interface Coupon {
  id: number
  name: string
  type: number // 1满减
  threshold: number
  amount: number
  valid_to: string
  received?: boolean // 当前用户已持有（券中心"已领取"标记，未登录恒 false）
  // 管理端全字段（消费侧接口不返回，均可选）
  valid_from?: string
  total_count?: number
  received_count?: number
  status?: number // 0已下架 1上架中
  created_at?: string
}

// 发放结果：granted 实际新发放人数，skipped 已持有被跳过人数
export interface GrantResult {
  granted: number
  skipped: number
}

export interface UserCoupon {
  id: number
  coupon_id: number
  name: string
  type: number
  threshold: number
  amount: number
  status: number // 0未用 2订单占用（下单锁券未支付） 1已使用
  expire_at: string
}

// ---------- 乡厨域 ----------
export interface Chef {
  id: number
  user_id: number
  real_name: string
  phone: string
  region: string
  cuisines: string
  experience: string
  rating: number
  order_count: number
  status: number // 0待审核 1已通过 2已拒绝 3已禁用
  audit_remark: string
  created_at: string
}

export interface ChefSchedule {
  id: number
  chef_id: number
  date: string
  status: number // 0可约 1已锁定 2休息日
}

export interface ChefPackage {
  id: number
  chef_id: number
  name: string
  banquet_type: string
  price_per_table: number
  dishes: string // JSON 数组字符串
  min_tables: number
  status: number // 1上架 0下架
  sort: number
}

export interface ChefCase {
  id: number
  chef_id: number
  title: string
  cover: string
  images: string
  description: string
  sort: number
}

export interface ChefDetail {
  chef: Chef
  packages: ChefPackage[]
  cases: ChefCase[]
}

export interface Review {
  id: number
  order_id: number
  user_id: number
  chef_id: number
  rating: number
  tags: string
  content: string
  images: string
  reply: string
  status: number
  created_at: string
}

// ---------- 订单域 ----------
export interface Order {
  id: number
  order_no: string
  user_id: number
  chef_id: number
  banquet_type: string
  banquet_time: string
  region: string
  address: string
  contact_name: string
  contact_phone: string
  table_count: number
  people_count: number
  package_id: number
  menu_remark: string
  amount: number
  paid_amount: number
  pay_type: number // 1定金 2全款
  status: number // 0待支付 1待接单 2已接单 3备菜中 4服务中 5已完成 6已取消 7已拒绝
  refund_status: number // 0无 1申请中 2已同意 3已拒绝 4已退款
  cancel_reason: string
  created_at: string
}

export interface AfterSale {
  id: number
  after_no: string
  order_id: number
  amount: number
  reason: string
  images: string
  status: number // 0待审核 1已同意待退款 2已拒绝 3已退款
  audit_remark: string
  created_at: string
}

// ---------- 采购供应域 ----------
export interface Supplier {
  id: number
  user_id: number
  name: string
  license: string
  contact: string
  phone: string
  region: string
  address: string
  categories: string
  rating: number
  status: number
  audit_remark: string
}

export interface Ingredient {
  id: number
  supplier_id: number
  supplier_name?: string
  name: string
  category: string
  unit: string
  price: number
  stock: number
  locked_stock: number
  origin: string
  images: string
  status: number
}

export interface PurchaseItem {
  id: number
  ingredient_id: number
  name: string
  unit: string
  price: number
  quantity: number
  subtotal: number
}

export interface PurchaseOrder {
  id: number
  purchase_no: string
  chef_id: number
  supplier_id: number
  supplier_name?: string
  order_id: number
  total_amount: number
  paid_amount: number
  fulfill_method: number // 1自取 2送货
  pickup_address: string
  deliver_address: string
  deliver_time: string
  status: number // 0待接单 1备货中 2待自取/待送达 3已完成 4已取消
  remark: string
  items?: PurchaseItem[]
  created_at: string
}

// ---------- 财务域 ----------
export interface PayResult {
  payment_no: string
  params: {
    mock: boolean
    payment_no?: string
    app_id?: string
    time_stamp?: string
    nonce_str?: string
    package?: string
    pay_sign?: string
  }
}

export interface Income {
  total_net: number
  month_net: number
  /** 冻结中：未满结算冻结期（T+N 自动解冻为可提） */
  frozen_amount: number
  total_withdrawn: number
  available: number
}

export interface Withdrawal {
  id: number
  withdraw_no: string
  role: number
  target_id: number
  amount: number
  channel: number
  status: number // 0待审核 1已通过待打款 2已拒绝 3已打款
  audit_remark: string
  applied_at: string
  audited_at: string
  paid_at: string
}

export interface Settlement {
  id: number
  settle_no: string
  role: number
  target_id: number
  biz_no: string
  gross_amount: number
  commission_amount: number
  net_amount: number
  status: number
  settle_time: string
}

// ---------- 系统域 ----------
export interface AdminInfo {
  id: number
  username: string
  real_name: string
  role: string
}

export interface Dicts {
  banquet_types: string[]
  cuisines: string[]
  order_status: Record<string, string>
  purchase_status: Record<string, string>
}

export interface OperationLog {
  id: number
  admin_id: number
  action: string
  target_type: string
  target_id: number
  detail: string
  ip: string
  created_at: string
}
