package wxpay

import (
	"fmt"
	"net/http"
	"net/url"
)

// CloseOrder 微信侧关单：作废未支付订单，关单后用户无法再支付该单。
// 成功返回 204；订单不存在（404，如从未成功下单/已过期回收）视为无需可关，
// 由调用方按 APIError.Status 区分；订单已支付会返回错误码，调用方必须中止流程。
func (c *Client) CloseOrder(outTradeNo string) error {
	path := fmt.Sprintf("/v3/pay/transactions/out-trade-no/%s/close", url.PathEscape(outTradeNo))
	return c.do(http.MethodPost, path, map[string]string{"mchid": c.cfg.MchID}, nil)
}
