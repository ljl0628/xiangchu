# 乡厨系统 API 一览

> 交互式调试文档：服务启动后访问 `/swagger/index.html`（Authorize 按钮粘贴 `Bearer {token}`）；注解变更后 `task server:swagger` 重新生成。

统一约定：HTTP 200 + 业务码（`code=0` 成功）；登录态 `Authorization: Bearer <token>`；
金额单位为**分**，费率单位为**基点 BP**（3% = 300）。
角色：`user` 顾客 / `chef` 乡厨 / `supplier` 供应商 / `admin` 管理员。
`user` 组接口同时允许 user/chef/supplier 三种 token。

## 1 用户域
| 方法 | 路径 | 说明 | 角色 |
|---|---|---|---|
| POST | /user/auth/code | 发送验证码（dev 固定 123456） | 公开 |
| POST | /user/auth/login | 手机号+验证码登录（自动注册） | 公开 |
| POST | /user/auth/wx | 微信 code 登录 | 公开 |
| POST | /user/auth/web | Web 手机号+密码登录（乡厨/供应商） | 公开 |
| GET | /user/profile | 我的资料 | user |
| PUT | /user/profile | 改昵称/头像 | user |
| GET/POST | /user/addresses | 地址簿列表/新增 | user |
| PUT/DELETE | /user/addresses/:id | 改/删地址 | user |
| GET | /coupons | 券中心 | 公开 |
| GET | /user/coupons | 我的优惠券 | user |
| POST | /user/coupons/:id/receive | 领券 | user |

## 2 乡厨域
| 方法 | 路径 | 说明 | 角色 |
|---|---|---|---|
| GET | /chefs | 乡厨列表（region/cuisine/keyword/min_rating/order_by） | 公开 |
| GET | /chefs/:id | 乡厨详情（含套餐+案例） | 公开 |
| GET | /chefs/:id/schedules | 档期日历 from/to | 公开 |
| GET | /chefs/:id/reviews | 乡厨评价分页 | 公开 |
| POST | /chef/apply | 入驻申请 | chef |
| GET/PUT | /chef/profile | 我的档案 | chef |
| GET | /chef/workbench | 工作台（今日/待接单） | chef |
| GET | /chef/schedules | 我的档期 from/to | chef |
| PUT | /chef/schedules/rest-days | 标记/取消休息日 | chef |
| GET/POST | /chef/packages | 套餐列表/新建 | chef |
| PUT/DELETE | /chef/packages/:id | 套餐改/下架 | chef |

## 3 订单域
| 方法 | 路径 | 说明 | 角色 |
|---|---|---|---|
| POST | /orders | 下单（返回支付参数；pay_type 1定金30% 2全款） | user |
| GET | /orders | 我的订单（status 分页） | user |
| GET | /orders/:id | 订单详情 | user |
| POST | /orders/:id/cancel | 取消（未接单） | user |
| POST | /orders/:id/pay-balance | 补尾款 | user |
| POST | /orders/:id/review | 评价（仅已完成，一单一评） | user |
| POST | /orders/:id/aftersale | 申请售后退款（进行中 2-4 或已完成 5；进行中审核同意=取消+退款） | user |
| GET | /chef/orders | 乡厨订单列表 | chef |
| GET | /chef/orders/:id | 乡厨订单详情 | chef |
| POST | /chef/orders/:id/accept | 接单（1→2） | chef |
| POST | /chef/orders/:id/reject | 拒单（1→7，自动退款） | chef |
| POST | /chef/orders/:id/start-prep | 开始备菜（2→3） | chef |
| POST | /chef/orders/:id/start-service | 开席服务（3→4） | chef |
| POST | /chef/orders/:id/complete | 完成（4→5，触发结算+评分） | chef |

状态机：0待支付 → 1待接单 → 2已接单 → 3备菜中 → 4服务中 → 5已完成；
1 超时 **12 小时**未接单自动取消并退款；1→7 拒单退款；2/3/4→6 售后审核同意取消退款；6已取消 / 7已拒绝。
退款：refund_status 0无 1申请中 2已同意 3已拒绝 4已退款。

## 4 采购供应域
| 方法 | 路径 | 说明 | 角色 |
|---|---|---|---|
| GET | /ingredients | 食材市场（keyword/category/supplier_id） | 公开 |
| POST | /purchases | 创建采购单（可关联 order_id，返回支付参数） | chef |
| GET | /purchases | 我的采购单 | chef |
| GET | /purchases/:id | 采购单详情 | chef |
| POST | /purchases/:id/cancel | 取消（待接单且未接） | chef |
| GET/POST | /supplier/ingredients | 商品列表/新建 | supplier |
| PUT/DELETE | /supplier/ingredients/:id | 改/下架 | supplier |
| GET | /supplier/purchases | 供应商采购单（status 筛选） | supplier |
| GET | /supplier/purchases/:id | 详情 | supplier |
| POST | /supplier/purchases/:id/accept | 接单（锁定库存） | supplier |
| POST | /supplier/purchases/:id/reject | 拒单（已支付自动退款） | supplier |
| POST | /supplier/purchases/:id/ready | 备货完成 | supplier |
| POST | /supplier/purchases/:id/complete | 完成（自取已取/已送达，触发结算） | supplier |

采购状态：0待接单 1备货中 2待自取/待送达 3已完成 4已取消。

## 5 财务域
| 方法 | 路径 | 说明 | 角色 |
|---|---|---|---|
| POST | /pay/notify/wx | 微信支付回调 | 公开(验签) |
| POST | /pay/mock/:paymentNo | 模拟支付成功（dev 联调） | user |
| GET | /chef/income | 乡厨收入总览 | chef |
| POST/GET | /chef/withdrawals | 提现申请/记录 | chef |
| GET | /supplier/income | 供应商收入总览 | supplier |
| POST/GET | /supplier/withdrawals | 提现申请/记录 | supplier |

佣金：乡厨 3%（300BP）、供应商 1%（100BP），可在后台调整（system_settings 覆盖配置默认）。

## 6 管理端
| 方法 | 路径 | 说明 |
|---|---|---|
| POST | /admin/auth/login | 管理员登录（默认 admin/admin123） |
| GET | /admin/me | 当前管理员 |
| GET | /admin/dicts | 字典（宴席类型/菜系/状态映射） |
| GET | /admin/roles | 角色列表 |
| GET | /admin/oper-logs | 操作日志分页 |
| GET | /admin/users | 用户列表 keyword |
| POST | /admin/users/:id/status | 禁用/启用 |
| GET | /admin/chefs | 乡厨列表（全部状态） |
| POST | /admin/chefs/:id/audit | 乡厨入驻审核 |
| GET | /admin/chefs/stats | 乡厨统计 |
| GET | /admin/suppliers | 供应商列表 |
| POST | /admin/suppliers/:id/audit | 供应商审核 |
| GET | /admin/suppliers/stats | 供应商统计 |
| GET | /admin/orders | 订单列表 status/keyword |
| GET | /admin/orders/:id | 订单详情 |
| GET | /admin/stats | 订单统计（GMV/状态/复购率/区域） |
| GET | /admin/aftersales | 售后列表 |
| POST | /admin/aftersales/:id/audit | 售后审核（同意自动退款） |
| GET | /admin/reviews | 评价列表 |
| POST | /admin/reviews/:id/status | 隐藏/恢复评价 |
| GET | /admin/purchases | 采购列表 |
| GET | /admin/purchases/fulfill-stats | 采购统计（履约方式/GMV） |
| GET | /admin/settlements | 结算记录 |
| GET | /admin/withdrawals | 提现列表 |
| POST | /admin/withdrawals/:id/audit | 提现审核 |
| POST | /admin/withdrawals/:id/mark-paid | 标记已打款（线下转账） |
| GET/PUT | /admin/settings | 佣金费率读取/修改 |
| GET | /admin/finance-stats | 财务总览 |
| GET | /admin/reconciles | 对账批次列表（status 0进行中 1完成 2失败） |
| POST | /admin/reconciles | 手动触发对账（body: 账单日；同日已跑非失败直接返回既有批次，仅真实支付模式） |
| GET | /admin/reconciles/:id/diffs | 对账差异明细（MISSING_LOCAL掉单/MISSING_BILL/AMOUNT_MISMATCH/REFUND_MISMATCH） |

## 7 异步消息（RabbitMQ）
| 交换机 | routing key | 队列 | 说明 |
|---|---|---|---|
| xc.delay | order.timeout | q.order.timeout | 支付成功后延迟 12h，未接单自动取消退款 |
| xc.event | pay.success | q.order.pay / q.food.pay | 支付成功（订单/采购各消费一份） |
| xc.event | pay.refunded | q.order.refund | 退款完成 |
| xc.event | review.created | q.chef.review | 评价后重算乡厨评分 |
| xc.event | notify.default | q.notify | 站内通知（v1 落日志） |
| xc.dlx | - | q.dead | 死信队列 |
