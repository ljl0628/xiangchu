# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

乡厨系统：连接用户（办席主家）/ 乡厨 / 食材供应商的乡村宴席服务平台。需求与全部决策项见 [需求.md](需求.md)，API 清单见 [docs/api.md](docs/api.md)，表结构见 [docs/schema.sql](docs/schema.sql)。注释、文档、提交说明均使用中文。

## 常用命令

### 后端（Go，目录 server/，用 Task 管理任务）

```bash
task server:run      # 启动开发服务（自动先杀掉占用 8080 的旧进程）
task server:dev      # air 热重载
task server:build    # 编译到 server/bin/server.exe
task server:test     # go test ./...
task server:swagger  # 重新生成 Swagger 文档（改 handler 注解后必须跑）
task server:migrate  # 数据库迁移到最新版本（生产部署必须先于服务启动执行）
task server:stop     # 停掉占用 8080 的进程
```

也可直接 `cd server && go run ./cmd/server -c configs/config.yaml`。配置在 `server/configs/config.yaml`，任何配置项可用 `XC_` 前缀环境变量覆盖（如 `XC_MYSQL_DSN`）。

前置依赖：`docker compose -f docker-compose.dev.yml up -d` 一键起三个中间件容器（`xiangchu-mysql` / `xiangchu-redis` / `xiangchu-rabbitmq`，延迟插件已内置；宿主机端口 3310 / 6381 / 5673，避让本机原生 MySQL 与其他项目容器，RabbitMQ 管理台 http://localhost:15673 guest/guest）。首次使用先 `task server:migrate` 建表再启动服务（种子自动写入管理员 admin/admin123）。表结构统一走 golang-migrate；`mysql.auto_migrate: true` 仅作应急开关（与迁移混用会重复列冲突）。

### 前端（pnpm workspace，目录 web/）

```bash
cd web && pnpm install
pnpm dev:admin            # 管理后台 http://localhost:5173
pnpm dev:user-mp          # 用户端 H5
pnpm dev:chef-mp          # 乡厨端 H5
pnpm dev:supplier-mp:wx   # 供应商端微信小程序（H5 同理 --filter supplier-mp dev:h5）
```

uni-app 小程序端用 HBuilderX/微信开发者工具导入对应 apps/ 目录。

### 联调便利（dev 模式）

- 支付：`POST /pay/mock/:paymentNo` 模拟微信回调，覆盖全流程
- 短信：验证码固定 `123456`（`sms.dev_mode: true`）
- 健康检查：`GET /healthz`
- 接口文档：`/swagger/index.html`（Authorize 粘贴 `Bearer {token}`）；注解在 `cmd/server/main.go`（全局信息；**@tag.* 必须放在 @SecurityDefinitions 之前**，否则被区块解析吞掉）与各模块 handler（swag 语法，`response.R{data=...}` 链式覆盖描述响应体）；产物提交在 `server/docs/`，改注解后跑 `task server:swagger`

**Windows curl 陷阱**：`curl -d` 内联中文请求体按 GBK 编码发送，后端解析乱码。测试含中文的接口必须把 JSON 写入 UTF-8 文件后 `curl -d @payload.json`。

## 后端架构

Go 1.22 模块化单体（Gin + GORM）。`server/internal/`：

### 模块结构（核心约束）

`internal/modules/<模块>/` 固定五文件：`model.go`（GORM 模型）、`dto.go`、`repo.go`、`service.go`、`handler.go`。

**模块间只允许通过 Service 接口调用，禁止跨模块访问 Repo/Model。** 跨模块解耦的异步逻辑走 RabbitMQ 事件（各模块 `RegisterConsumers`，消费者按业务单号幂等）。

现有模块：user（登录/地址/优惠券）、chef（档期/套餐/收入）、order（宴席订单状态机/评价/售后）、food（食材/采购单/库存）、pay（支付/结算/提现/系统配置）、supplier、admin（后台/审核/操作日志）、notify（Notifier 接口，当前 Log 实现）。

### 装配（bootstrap.go）

`internal/bootstrap/bootstrap.go` 手写依赖注入，无 Wire/DI 框架。**构造顺序有依赖**（如 paySvc 先于 chefSvc，chef 经 pay.Service 查收入），新增模块依赖时注意。每个模块 Handler 自己注册路由（`Routes(r, mk)`），admin 模块最后挂载；MQ 不可用时降级为仅记日志，不阻塞启动。

### 鉴权与角色

- JWT claim 携带 role，`middleware.Auth(maker, roles...)` 校验
- `roleAllows` 映射：**user 组接口同时接受 user/chef/supplier 三种 token**（`/user/*` 这类通用接口）；chef/supplier/admin 组只接受对应角色
- admin 走 `admins` 表（username+password）；chef/supplier 走 users 手机号+密码，登录时校验档案已审核通过

### 关键约定

- **响应**：一律 HTTP 200 + `R{code, msg, data}`，`code=0` 成功；错误经 `pkg/errs` 的 `*errs.Err` 传给 `response.Fail`。分页用 `response.PageData`
- **金额一律 int64 分**，费率用基点 BP（3% = 300），计算用 shopspring/decimal，展示层分转元
- **并发/幂等三处双保险**：档期锁 = Redis SETNX + `chef_schedules` 唯一索引；库存 = `locked_stock` 行级更新 + RowsAffected 校验防超卖；支付 = `payments.out_trade_no` 唯一索引 + 状态只向前流转
- 订单状态机：0待支付→1待接单→2已接单→3备菜中→4服务中→5已完成，6已取消/7已拒绝；待接单超 12 小时由 RabbitMQ 延迟消息（`xc.delay` 交换机）自动取消退款。改状态流转逻辑先看 order/service.go 与 docs/api.md 的状态机注释
- MQ 拓扑：`xc.delay`（延迟）+ `xc.event`（topic）+ `xc.dlx`（死信），见 `internal/infra/mq`

### 数据库迁移（golang-migrate）

迁移文件在 `server/migrations/`（embed 内嵌，随代码版本化），命令在 `cmd/migrate`。流程：

1. `task server:migrate:new -- add_orders_channel` 生成一对 up/down 文件
2. 填 SQL；**改 GORM 模型必须同步写迁移文件**（000001 基线从实库导出，与模型一致）
3. `task server:migrate` 应用；`task server:migrate:status` 看版本；`task server:migrate:down` 回滚一步

- 存量库（AutoMigrate 时代建的表）一次性 `task server:migrate:baseline` 标记基线 v1；空库误用会报错
- `docs/schema.sql` 是 000001 基线的同步副本（仅文档），改表后两处一起更新
- dev/prod 一律 `mysql.auto_migrate: false`：改模型 → 写迁移 → `task server:migrate`；发布流程先迁移再启动服务

## 前端架构

pnpm workspace，`web/packages/shared` 是关键：

- `shared/src/request.ts` — ApiClient 类，**底层适配器模式**（小程序 `uni.request` / Web `axios`），自动注入 Bearer token、解包 `R<T>`、非 0 code 抛 `ApiError`
- `shared/src/types.ts` + `src/api/*.ts` — 全量类型与按域封装的 API，四个 app 全部从这里引，不要在 app 里裸写请求
- `web/shared`（根下的同名目录）是 pnpm workspace 链接产物，源码在 `web/packages/shared`

四个 app：user-mp / chef-mp / supplier-mp（uni-app Vue3+TS，编译微信小程序 + H5）、admin（Vue3 + Element Plus + Pinia，按角色动态菜单）。

## 一期已知边界（不要找不存在的实现）

- 微信支付回调已支持真实模式：`wxpay.Client.ParseNotify` 做平台证书验签（`/v3/certificates` 自动下载缓存，未知 serial 自动刷新）+ AES-256-GCM 解密；单测在 `internal/infra/wxpay/notify_test.go`。**退款结果仍为 mock 即时成功，真实退款回调确认未接**
- 短信通道未接入（dev 固定验证码）
- 操作日志自动记录仅覆盖 admin 模块自身路由，各业务模块的 `/admin/*` 端点已鉴权但不落日志
- 提现一期线下打款 + 后台标记，无自动转账
- 仓库目前仅 `wxpay/notify_test.go` 一处单测（回调验签解密链路），其余模块无用例

## 部署

`docker compose up -d --build` 一键部署（server :8080，管理后台 `http://localhost/admin-web/`），相关文件在 `deploy/` 与 `docker-compose.yml`。
