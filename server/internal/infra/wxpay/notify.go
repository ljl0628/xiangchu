package wxpay

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// 回调事件类型（V3 event_type）
const (
	EventPaySuccess     = "TRANSACTION.SUCCESS"
	EventRefundSuccess  = "REFUND.SUCCESS"
	EventRefundAbnormal = "REFUND.ABNORMAL"
	EventRefundClosed   = "REFUND.CLOSED"
)

// Notify 回调解析产物：信封元数据 + 解密后的资源明文。
// 业务字段结构随 event_type 而异（支付/退款），由 DecodePayNotify / DecodeRefundNotify 解出
type Notify struct {
	ID        string // 微信通知 ID；重试推送同 ID，可作幂等键。mock 模式为空
	EventType string // TRANSACTION.SUCCESS / REFUND.SUCCESS / ...
	Plain     []byte // resource 解密明文（mock 模式即原始 body，且强制 trade_state=SUCCESS）
}

// PayNotifyResource TRANSACTION.SUCCESS 明文结构（金额校验用 amount.total）
type PayNotifyResource struct {
	OutTradeNo    string `json:"out_trade_no"`
	TransactionID string `json:"transaction_id"`
	TradeState    string `json:"trade_state"`
	Amount        struct {
		Total int64 `json:"total"` // 订单总金额（分）
	} `json:"amount"`
}

// RefundNotifyResource REFUND.SUCCESS / ABNORMAL / CLOSED 明文结构
type RefundNotifyResource struct {
	OutTradeNo   string `json:"out_trade_no"`
	OutRefundNo  string `json:"out_refund_no"`
	RefundID     string `json:"refund_id"`
	RefundStatus string `json:"refund_status"` // SUCCESS / ABNORMAL / CLOSED
	Amount       struct {
		Refund int64 `json:"refund"` // 退款金额（分）
	} `json:"amount"`
}

// DecodePayNotify 支付结果通知明文 → 结构
func DecodePayNotify(plain []byte) (*PayNotifyResource, error) {
	var r PayNotifyResource
	if err := json.Unmarshal(plain, &r); err != nil {
		return nil, fmt.Errorf("支付通知明文解析失败: %w", err)
	}
	return &r, nil
}

// DecodeRefundNotify 退款结果通知明文 → 结构
func DecodeRefundNotify(plain []byte) (*RefundNotifyResource, error) {
	var r RefundNotifyResource
	if err := json.Unmarshal(plain, &r); err != nil {
		return nil, fmt.Errorf("退款通知明文解析失败: %w", err)
	}
	return &r, nil
}

// notifyEnvelope V3 回调请求体（密文信封）
type notifyEnvelope struct {
	ID        string `json:"id"`
	EventType string `json:"event_type"`
	Resource  struct {
		Algorithm      string `json:"algorithm"`
		Ciphertext     string `json:"ciphertext"`
		AssociatedData string `json:"associated_data"`
		Nonce          string `json:"nonce"`
	} `json:"resource"`
}

// ParseNotify 解析微信支付回调：
//   - mock/未启用：接受明文 JSON（补齐 trade_state=SUCCESS），与真实模式同构
//   - 真实模式：Wechatpay-* 头验签（平台证书）+ resource 密文 AES-256-GCM 解密
func (c *Client) ParseNotify(headers http.Header, body []byte) (*Notify, error) {
	if c.cfg.Mock || !c.cfg.Enabled {
		// mock 明文按支付通知补默认值，下游解码逻辑与真实模式完全一致
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			return nil, err
		}
		if _, ok := m["trade_state"]; !ok {
			m["trade_state"] = "SUCCESS"
		}
		plain, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		return &Notify{Plain: plain}, nil
	}
	return c.parseReal(headers, body)
}

// parseReal 真实模式回调处理：头齐全性 → 时间戳窗口 → 平台证书验签 → 密文解密
func (c *Client) parseReal(headers http.Header, body []byte) (*Notify, error) {
	serial := headers.Get("Wechatpay-Serial")
	tsStr := headers.Get("Wechatpay-Timestamp")
	nonceHdr := headers.Get("Wechatpay-Nonce")
	sigB64 := headers.Get("Wechatpay-Signature")
	if serial == "" || tsStr == "" || nonceHdr == "" || sigB64 == "" {
		return nil, errors.New("缺少 Wechatpay-* 签名头")
	}

	// 时间戳窗口 ±5 分钟，超窗拒绝（防重放）
	sec, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil || time.Since(time.Unix(sec, 0)) > 5*time.Minute {
		return nil, errors.New("回调时间戳缺失或超出 ±5 分钟窗口")
	}

	// 用 Wechatpay-Serial 选平台证书验签（未知 serial 自动刷新证书缓存）
	cert, err := c.platformCert(serial)
	if err != nil {
		return nil, err
	}

	message := tsStr + "\n" + nonceHdr + "\n" + string(body) + "\n"
	if err := verifySignature(cert, message, sigB64); err != nil {
		return nil, err
	}

	// 解密 resource 密文得到明文 JSON
	var env notifyEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("回调体非 JSON: %w", err)
	}
	plain, err := aesGCMDecrypt([]byte(c.cfg.APIv3Key), env.Resource.Nonce, env.Resource.AssociatedData, env.Resource.Ciphertext)
	if err != nil {
		return nil, err
	}
	return &Notify{ID: env.ID, EventType: env.EventType, Plain: plain}, nil
}

// verifySignature 平台证书公钥 RSA-SHA256 验签。
// 签名串 = Wechatpay-Timestamp + \n + Wechatpay-Nonce + \n + 请求原文 + \n
func verifySignature(cert *x509.Certificate, message, sigB64 string) error {
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("平台证书公钥非 RSA")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("签名非 base64: %w", err)
	}
	sum := sha256.Sum256([]byte(message))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		return fmt.Errorf("回调验签失败: %w", err)
	}
	return nil
}
