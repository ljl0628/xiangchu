// Package bootstrap 负责装配全部模块依赖并启动 HTTP 服务
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	_ "github.com/xiangchu/server/docs" // swagger 生成文档注册（task server:swagger 重新生成）

	"github.com/xiangchu/server/internal/config"
	"github.com/xiangchu/server/internal/infra/logger"
	"github.com/xiangchu/server/internal/infra/mq"
	"github.com/xiangchu/server/internal/infra/mysql"
	"github.com/xiangchu/server/internal/infra/redis"
	"github.com/xiangchu/server/internal/infra/wxpay"
	"github.com/xiangchu/server/internal/middleware"
	"github.com/xiangchu/server/internal/modules/admin"
	"github.com/xiangchu/server/internal/modules/chef"
	"github.com/xiangchu/server/internal/modules/food"
	"github.com/xiangchu/server/internal/modules/notify"
	"github.com/xiangchu/server/internal/modules/order"
	"github.com/xiangchu/server/internal/modules/pay"
	"github.com/xiangchu/server/internal/modules/supplier"
	"github.com/xiangchu/server/internal/modules/user"
	"github.com/xiangchu/server/internal/pkg/jwtx"
	"github.com/xiangchu/server/internal/pkg/password"
	"go.uber.org/zap"
)

// Run 初始化基础设施、装配模块、启动服务（优雅退出）
func Run(cfg *config.Config) error {
	logger.Init(cfg.Log)
	defer logger.Sync()

	db, err := mysql.Open(cfg.MySQL, cfg.App.Mode)
	if err != nil {
		return fmt.Errorf("连接 MySQL 失败: %w", err)
	}
	rdb, err := redis.New(cfg.Redis)
	if err != nil {
		return fmt.Errorf("连接 Redis 失败: %w", err)
	}
	bus, err := mq.Connect(cfg.RabbitMQ.URL)
	if err != nil {
		logger.L().Warn("RabbitMQ 连接失败，消息能力降级为仅记录日志", zap.Error(err))
		bus = nil
	}
	mk := jwtx.NewMaker(cfg.JWT.Secret, cfg.JWT.ExpireHours, cfg.JWT.Issuer)
	wxClient, err := wxpay.New(cfg.Wx.Pay)
	if err != nil {
		return fmt.Errorf("初始化微信支付失败: %w", err)
	}

	// ---------- 服务装配（依赖注入；paySvc 需先于 chefSvc 构造，chef 经 pay.Service 查结算收入） ----------
	paySvc := pay.NewService(pay.NewRepo(db), wxClient, bus, cfg)
	chefSvc := chef.NewService(chef.NewRepo(db), rdb, paySvc)
	supSvc := supplier.NewService(supplier.NewRepo(db))
	foodSvc := food.NewService(food.NewRepo(db), supSvc, paySvc, bus)
	userSvc := user.NewService(user.NewRepo(db), rdb, mk, cfg, bus, chefSvc, supSvc)
	orderSvc := order.NewService(order.NewRepo(db), chefSvc, paySvc, userSvc, cfg, bus)
	adminRepo := admin.NewRepo(db)
	adminSvc := admin.NewService(adminRepo, mk)

	if err := migrate(cfg.MySQL.AutoMigrate, db); err != nil {
		return fmt.Errorf("数据库迁移失败: %w", err)
	}

	// ---------- MQ 消费者 ----------
	chef.RegisterConsumers(bus, chefSvc)
	order.RegisterConsumers(bus, orderSvc)
	food.RegisterConsumers(bus, foodSvc)
	notify.RunConsumer(bus, notify.NewLogNotifier())

	// 查单补偿：微信回调丢失时定时扫描待支付单主动对齐（mock 模式内部自跳过）
	paySvc.StartCompensator()
	// 每日对账：昨日微信账单生成后自动拉单与本地核对（mock 模式内部自跳过）
	paySvc.StartReconciler()

	return runHTTP(cfg, mk, userSvc, chefSvc, supSvc, foodSvc, paySvc, orderSvc, adminSvc, adminRepo)
}

// migrate 建表与种子数据。AutoMigrate 仅开发期使用（mysql.auto_migrate: true）；
// 生产置 false，表结构由 golang-migrate 迁移文件管理（task server:migrate，先于服务启动执行）
func migrate(autoMigrate bool, db *gorm.DB) error {
	if !autoMigrate {
		return seed(db)
	}
	if err := db.AutoMigrate(
		// 用户域
		&user.User{}, &user.UserAddress{}, &user.Coupon{}, &user.UserCoupon{},
		// 乡厨域
		&chef.Chefs{}, &chef.ChefSchedule{}, &chef.ChefCase{}, &chef.Package{},
		// 供应域
		&supplier.Supplier{}, &food.Ingredient{}, &food.PurchaseOrder{}, &food.PurchaseItem{},
		// 订单域
		&order.Order{}, &order.Review{}, &order.AfterSale{},
		// 财务域
		&pay.Payment{}, &pay.Settlement{}, &pay.Withdrawal{}, &pay.SystemSetting{},
		// 系统域
		&admin.Admin{}, &admin.OperationLog{}, &admin.Role{},
	); err != nil {
		return err
	}
	return seed(db)
}

// seed 初始数据：admin 角色字典 + 默认管理员（admin/admin123，上线前务必改密）
func seed(db *gorm.DB) error {
	var cnt int64
	if err := db.Model(&admin.Role{}).Where("code = ?", "admin").Count(&cnt).Error; err != nil {
		return err
	}
	if cnt == 0 {
		if err := db.Create(&admin.Role{Code: "admin", Name: "超级管理员"}).Error; err != nil {
			return err
		}
	}
	var acnt int64
	if err := db.Model(&admin.Admin{}).Where("username = ?", "admin").Count(&acnt).Error; err != nil {
		return err
	}
	if acnt == 0 {
		hash, err := password.Hash("admin123")
		if err != nil {
			return err
		}
		if err := db.Create(&admin.Admin{
			Username:     "admin",
			PasswordHash: hash,
			RealName:     "超级管理员",
			Role:         "admin",
			Status:       1,
		}).Error; err != nil {
			return err
		}
		logger.L().Info("已创建默认管理员 admin/admin123，请尽快修改密码")
	}
	return nil
}

// runHTTP 装配路由并启动 HTTP 服务
func runHTTP(cfg *config.Config, mk *jwtx.Maker, userSvc user.Service, chefSvc chef.Service, supSvc supplier.Service, foodSvc food.Service, paySvc pay.Service, orderSvc order.Service, adminSvc admin.Service, adminRepo *admin.Repo) error {
	if cfg.App.Mode != "dev" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery(), middleware.CORS())

	// 业务模块路由（挂载顺序无关，/admin/* 由各模块自行注册并鉴权）
	user.NewHandler(userSvc, mk).Routes(r)
	chef.NewHandler(chefSvc).Routes(r, mk)
	supplier.NewHandler(supSvc).Routes(r, mk)
	food.NewHandler(foodSvc).Routes(r, mk)
	pay.NewHandler(paySvc).Routes(r, mk)
	order.NewHandler(orderSvc, chefSvc).Routes(r, mk)
	// admin 模块最后挂载（登录/字典/操作日志等自身路由）
	admin.NewHandler(adminSvc, adminRepo).Routes(r, mk)

	// 健康检查
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Swagger 文档（注解变更后 task server:swagger 重新生成；常驻挂载，如需可改用 cfg.App.Mode == "dev" 门控）
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	srv := &http.Server{
		Addr:    cfg.HTTP.Addr,
		Handler: r,
	}
	go func() {
		logger.L().Info("HTTP 服务启动", zap.String("addr", cfg.HTTP.Addr), zap.String("mode", cfg.App.Mode))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.L().Error("HTTP 服务异常退出", zap.Error(err))
			os.Exit(1)
		}
	}()

	// 优雅退出：劫持 Ctrl+C（SIGINT）/ kill（SIGTERM），把"系统要杀我"翻译成"我先收拾现场再走"。
	// 流程：阻塞等退出信号 → Shutdown 停止接新请求并等在途请求收尾（最多 10s）→ main 返回进程退出。
	quit := make(chan os.Signal, 1) // 缓冲必须 ≥1：signal 包向 channel 发送不阻塞，缓冲不足信号会丢
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit // 阻塞撑住 main 生命周期，直到收到退出信号；不取返回值 = 不区分具体是哪个信号
	logger.L().Info("收到退出信号，正在关闭服务")
	// 10s 是 Shutdown 等待在途请求的期限（超时返回 DeadlineExceeded，剩余连接被强断），
	// 并不会传入业务 handler——在途请求用各自的 request context，Shutdown 只等不取消
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return err
	}
	// 已知边界：此处只优雅关闭了 HTTP；MQ 消费者等后台组件随 main 返回直接终止（依赖消息幂等兜底）
	logger.L().Info("服务已退出")
	return nil
}
