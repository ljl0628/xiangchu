package pay

import "testing"

// TestCompensateActionFor 微信 trade_state → 补偿动作映射
func TestCompensateActionFor(t *testing.T) {
	cases := []struct {
		state string
		want  compAction
	}{
		{"SUCCESS", compMarkPaid}, // 已支付：走回调同款记账
		{"NOTPAY", compWait},      // 未支付：等回调或下一轮
		{"USERPAYING", compWait},  // 支付中：等回调或下一轮
		{"CLOSED", compClose},     // 已关闭（含 2h 过期）：终态关单
		{"PAYERROR", compClose},   // 支付失败：终态关单
		{"REVOKED", compClose},    // 已撤销：终态关单
		{"REFUND", compManual},    // 已退款但本地仍待支付：账务异常，人工核对
		{"", compManual},          // 未识别状态：不自动流转
		{"whatever", compManual},  // 同上
	}
	for _, c := range cases {
		if got := compensateActionFor(c.state); got != c.want {
			t.Errorf("compensateActionFor(%q) = %v, want %v", c.state, got, c.want)
		}
	}
}
