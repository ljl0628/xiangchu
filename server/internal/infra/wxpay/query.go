package wxpay

import (
	"fmt"
	"net/http"
	"net/url"
)

// QueryResult 微信查单返回（仅取补偿所需字段；完整字段见 V3 文档「查询订单」）
type QueryResult struct {
	OutTradeNo    string `json:"out_trade_no"`
	TransactionID string `json:"transaction_id"` // 微信支付订单号（SUCCESS 时必有）
	TradeState    string `json:"trade_state"`    // SUCCESS/NOTPAY/USERPAYING/CLOSED/PAYERROR/REVOKED/REFUND
}

// QueryOrderByOutTradeNo 主动查单：按商户单号查询支付结果。
// 回调丢失时的补偿入口（微信官方建议：未收到回调应主动查单对齐）。
func (c *Client) QueryOrderByOutTradeNo(outTradeNo string) (*QueryResult, error) {
	var out QueryResult
	// V3 签名的 canonical URL 含 query string，do() 内部直接用 path 整体参与签名
	path := fmt.Sprintf("/v3/pay/transactions/out-trade-no/%s?mchid=%s",
		url.PathEscape(outTradeNo), url.QueryEscape(c.cfg.MchID))
	if err := c.do(http.MethodGet, path, nil, &out); err != nil {
		return nil, fmt.Errorf("微信查单失败: %w", err)
	}
	return &out, nil
}
