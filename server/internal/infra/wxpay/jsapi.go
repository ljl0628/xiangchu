package wxpay

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// PayParams 返回给前端的 JSAPI 调起支付参数
type PayParams struct {
	Mock      bool   `json:"mock"`
	PaymentNo string `json:"payment_no"`
	AppID     string `json:"app_id"`
	TimeStamp string `json:"time_stamp"`
	NonceStr  string `json:"nonce_str"`
	Package   string `json:"package"`
	PaySign   string `json:"pay_sign"`
}

// JSAPI 统一下单并返回调起支付参数；Mock 模式直接返回 mock 标记
func (c *Client) JSAPI(ctx context.Context, openid, paymentNo string, amountFen int64, desc string) (*PayParams, error) {
	if c.cfg.Mock || !c.cfg.Enabled {
		return &PayParams{Mock: true, PaymentNo: paymentNo}, nil
	}
	var out struct {
		PrepayID string `json:"prepay_id"` // PrepayID（预支付交易会话标识）是微信侧的"预付单号"
	}
	body := map[string]any{
		"appid":        c.cfg.AppID,
		"mchid":        c.cfg.MchID,
		"out_trade_no": paymentNo,
		"notify_url":   c.cfg.NotifyURL,
		"amount":       map[string]any{"total": amountFen, "currency": "CNY"},
		"payer":        map[string]any{"openid": openid},
	}
	if err := c.do(http.MethodPost, "/v3/pay/transactions/jsapi", body, &out); err != nil {
		return nil, fmt.Errorf("微信下单失败: %w", err)
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	n := nonce()
	msg := strings.Join([]string{c.cfg.AppID, ts, n, "prepay_id=" + out.PrepayID, ""}, "\n")
	sum := sha256.Sum256([]byte(msg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.key, crypto.SHA256, sum[:])
	if err != nil {
		return nil, fmt.Errorf("支付签名失败: %w", err)
	}
	return &PayParams{
		Mock:      false,
		PaymentNo: paymentNo,
		AppID:     c.cfg.AppID,
		TimeStamp: ts,
		NonceStr:  n,
		Package:   "prepay_id=" + out.PrepayID,
		PaySign:   base64.StdEncoding.EncodeToString(sig),
	}, nil
}
