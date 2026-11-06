# 乡厨系统（乡村宴席服务平台）

连接**顾客 / 乡厨 / 食材供应商**的一体化乡村宴席服务系统：顾客在线预订乡厨宴席，乡厨接单履约并按需向供应商采购食材，平台按佣金结算。

需求详见 [需求.md](需求.md)（v2.0，所有决策项已定），接口清单见 [docs/api.md](docs/api.md)，表结构参考 [docs/schema.sql](docs/schema.sql)。

## 技术栈

| 端 | 技术 |
|---|---|
| 后端 | Go 1.22 · Gin · GORM · MySQL 8 · Redis 7 · RabbitMQ（延迟消息插件）· JWT · 微信支付 V3（Mock 模式）· 七牛云 |
| 用户端/乡厨端 | uni-app · Vue3 · TypeScript（微信小程序 + H5 双端） |
| 管理后台 | Vue3 · Vite · TypeScript · Element Plus · Pinia |

## 目录结构

```
├── server/            Go 后端（单体分层：internal/modules/<模块>/model,dto,repo,service,handler）
│   ├── cmd/server/    入口
│   ├── configs/       配置（XC_ 前缀环境变量可覆盖任意配置项）
│   └── internal/      bootstrap 装配 / modules 业务模块 / infra 基础设施 / pkg 通用包 / middleware
├── web/               pnpm workspace 前端
│   ├── packages/shared/   共享类型 + 请求封装 + 全量 API（uni.request / axios 双适配）
│   └── apps/          user-mp（用户端）· chef-mp（乡厨端）· admin（管理后台）
├── deploy/            Dockerfile（rabbitmq 延迟插件 / web）、nginx.conf
├── docker-compose.yml 一键部署
└── docs/              api.md · schema.sql
```

## 快速开始（本地开发）

中间件（MySQL / Redis / RabbitMQ 延迟插件）用 Docker 一键起，容器名 `xiangchu-*`，端口 3310 / 6381 / 5673：

```bash
docker compose -f docker-compose.dev.yml up -d
# RabbitMQ 管理台 http://localhost:15673 (guest/guest)——注意不是 15672

cd server
task server:migrate    # 首次建表 + 写入种子数据：管理员 admin/admin123
task server:run
```

前端：

```bash
cd web
pnpm install
pnpm dev:admin          # 管理后台 http://localhost:5173
pnpm dev:user-mp        # 用户端 H5（小程序用 HBuilderX/微信开发者工具导入 apps/user-mp）
pnpm dev:chef-mp        # 乡厨端 H5
```

联调提示：微信支付/短信均为开发模式——支付走 `POST /pay/mock/:paymentNo` 模拟回调，验证码固定 `123456`。

## 一键部署（Docker）

```bash
docker compose up -d --build
# server :8080   管理后台 http://localhost/admin-web/   RabbitMQ 控制台 http://localhost:15672 (guest/guest)
```

容器内配置通过 `XC_` 环境变量覆盖（见 docker-compose.yml），例如 `XC_MYSQL_DSN`。

## 核心业务规则（已在代码中实现）

- **订单**：定金 30%（可配），尾款宴席后补付；支付成功锁乡厨档期（Redis SETNX + 唯一索引双保险）；**12 小时**未接单自动取消并退款；状态机 0待支付→1待接单→2已接单→3备菜中→4服务中→5已完成，另有 6已取消/7已拒绝。
- **采购**：可关联宴席订单；供应商接单即锁库存（`locked_stock`，行级 RowsAffected 校验防超卖）；完成时触发结算。
- **财务**：佣金乡厨 3% / 供应商 1%（BP 计价，后台可调，decimal 精确计算）；提现一期线下打款 + 后台标记。
- **评价**：订单完成后一单一评，异步重算乡厨评分（RabbitMQ `review.created`）。
- **跨模块通信**：模块间只走 Service 接口；事件走 RabbitMQ（`xc.delay` 延迟交换机 + `xc.event` topic + `xc.dlx` 死信）。

## 一期已知边界

- 操作日志自动记录仅覆盖 admin 模块自身路由，业务模块的 /admin/* 端点已鉴权但未自动落日志。
- 微信支付回调已支持真实模式：Wechatpay-* 头平台证书验签 + AES-256-GCM 解密（`internal/infra/wxpay`，单元测试覆盖篡改/超窗时间戳/未知 serial）；生产配置 `wx.pay` 后自动生效，Mock 模式仍覆盖全流程联调。
- 短信通道未接入（dev 固定验证码）；图片上传凭证接口已备（七牛未配置时明确报错）。
- 乡厨工作台"本月收入"为占位 0，收入数据以财务模块 `/chef/income` 为准。
