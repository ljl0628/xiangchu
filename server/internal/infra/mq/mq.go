package mq

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/xiangchu/server/internal/infra/logger"
	"go.uber.org/zap"
)

// 交换机与队列命名集中管理
const (
	DelayExchange = "xc.delay" // x-delayed-message 插件交换机（延迟任务）
	EventExchange = "xc.event" // 业务事件 topic 交换机
	DeadExchange  = "xc.dlx"   // 死信交换机

	QueueOrderTimeout = "q.order.timeout"
	QueueOrderPay     = "q.order.pay"
	QueueFoodPay      = "q.food.pay"
	QueueChefReview   = "q.chef.review"
	QueueOrderRefund  = "q.order.refund"
	QueueNotify       = "q.notify"
	QueueDead         = "q.dead"

	// 路由键（routing key）
	KeyOrderTimeout  = "order.timeout"
	KeyPaySuccess    = "pay.success"
	KeyPayRefunded   = "pay.refunded"
	KeyReviewCreated = "review.created"
	KeyNotify        = "notify.default"
)

// MQ RabbitMQ 封装；连接失败不阻断启动（发布/消费各自判空降级）
type MQ struct {
	conn *amqp.Connection
	mu   sync.Mutex
	ch   *amqp.Channel
}

// Connect 建立连接并声明全部拓扑（交换机/队列/绑定）
func Connect(url string) (*MQ, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, err
	}
	m := &MQ{conn: conn}
	if err := m.setup(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return m, nil
}

func (m *MQ) setup() error {
	ch, err := m.conn.Channel()
	if err != nil {
		return err
	}
	m.ch = ch
	dlx := amqp.Table{"x-dead-letter-exchange": DeadExchange}
	// 延迟交换机类型必须是 x-delayed-message（插件提供），
	// 误声明为普通 direct 会导致 x-delay 头被忽略、消息立即投递
	_ = ch.ExchangeDeclare(DelayExchange, "x-delayed-message", true, false, false, false,
		amqp.Table{"x-delayed-type": "direct"})
	_ = ch.ExchangeDeclare(EventExchange, "topic", true, false, false, false, nil)
	_ = ch.ExchangeDeclare(DeadExchange, "fanout", true, false, false, false, nil)

	for _, q := range []string{
		QueueOrderTimeout, QueueOrderPay, QueueFoodPay,
		QueueChefReview, QueueOrderRefund, QueueNotify, QueueDead,
	} {
		if _, err := ch.QueueDeclare(q, true, false, false, false, dlx); err != nil {
			return err
		}
	}
	// 延迟任务：订单超时
	if err := ch.QueueBind(QueueOrderTimeout, KeyOrderTimeout, DelayExchange, false, nil); err != nil {
		return err
	}
	// 支付成功事件：订单与采购模块各消费一份
	for _, q := range []string{QueueOrderPay, QueueFoodPay} {
		if err := ch.QueueBind(q, KeyPaySuccess, EventExchange, false, nil); err != nil {
			return err
		}
	}
	// 退款完成 → 订单模块更新退款状态
	if err := ch.QueueBind(QueueOrderRefund, KeyPayRefunded, EventExchange, false, nil); err != nil {
		return err
	}
	// 评价 → 乡厨评分重算
	if err := ch.QueueBind(QueueChefReview, KeyReviewCreated, EventExchange, false, nil); err != nil {
		return err
	}
	// 通知事件 → notify 模块
	if err := ch.QueueBind(QueueNotify, "notify.*", EventExchange, false, nil); err != nil {
		return err
	}
	_ = ch.QueueBind(QueueDead, "", DeadExchange, false, nil)
	return nil
}

// Publish 发布 JSON 消息到指定交换机
func (m *MQ) Publish(exchange, key string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return m.publish(exchange, key, body, nil)
}

// PublishDelay 发布延迟消息（依赖 rabbitmq_delayed_message_exchange 插件）
func (m *MQ) PublishDelay(key string, payload any, delay time.Duration) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return m.publish(DelayExchange, key, body, amqp.Table{"x-delay": delay.Milliseconds()})
}

func (m *MQ) publish(exchange, key string, body []byte, headers amqp.Table) error {
	if m == nil || m.ch == nil {
		return errors.New("rabbitmq 未连接")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.ch.PublishWithContext(ctx, exchange, key, false, false, amqp.Publishing{
		ContentType:  "application/json",
		Body:         body,
		Headers:      headers,
		DeliveryMode: amqp.Persistent,
	})
}

// Consume 启动一个消费者 goroutine；处理失败 Nack 进死信队列并告警日志
func (m *MQ) Consume(name string, h func(ctx context.Context, body []byte) error) {
	if m == nil || m.conn == nil {
		logger.L().Warn("RabbitMQ 未连接，跳过消费者", zap.String("queue", name))
		return
	}
	go func() {
		for {
			ch, err := m.conn.Channel()
			if err != nil {
				logger.L().Error("消费者建通道失败，5s 后重试", zap.String("queue", name), zap.Error(err))
				time.Sleep(5 * time.Second)
				continue
			}
			deliveries, err := ch.Consume(name, "", false, false, false, false, nil)
			if err != nil {
				logger.L().Error("消费者注册失败，5s 后重试", zap.String("queue", name), zap.Error(err))
				_ = ch.Close()
				time.Sleep(5 * time.Second)
				continue
			}
			for d := range deliveries {
				if err := h(context.Background(), d.Body); err != nil {
					logger.L().Error("消息处理失败，转入死信队列",
						zap.String("queue", name), zap.Error(err), zap.String("body", string(d.Body)))
					_ = d.Nack(false, false)
				} else {
					_ = d.Ack(false)
				}
			}
			_ = ch.Close()
			time.Sleep(time.Second) // 通道被关闭，重建
		}
	}()
}

func (m *MQ) Close() {
	if m == nil {
		return
	}
	if m.ch != nil {
		_ = m.ch.Close()
	}
	if m.conn != nil {
		_ = m.conn.Close()
	}
}
