package wxpay

import (
	"strings"
	"testing"
)

// TestYuanToFen 账单金额元字符串转分：正常两位小数 / 一位 / 整数 / 空 / 负数 / 脏数据
func TestYuanToFen(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"5664.00", 566400, false},
		{"0.50", 50, false},
		{"188", 18800, false},
		{"", 0, false},
		{"1,889.00", 188900, false}, // 千分位容忍
		{"-0.01", -1, false},
		{"1.234", 0, true}, // 小数超两位
		{"abc", 0, true},
	}
	for _, c := range cases {
		got, err := yuanToFen(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("yuanToFen(%q) 应报错", c.in)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("yuanToFen(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
}

// TestParseTradeBill 合成账单 CSV：成功行 + 退款行 + 汇总行丢弃 + 列名包含匹配兜底
func TestParseTradeBill(t *testing.T) {
	csvData := strings.Join([]string{
		"交易时间,公众账号ID,商户号,商户订单号,微信支付订单号,付款金额,订单状态,申请退款金额,备注",
		"2026-11-08 12:00:00,wx123,1900000109,ZF2611080001,4200001234567890,5664.00,TRADE_SUCCESS,0.00,",
		"2026-11-08 13:00:00,wx123,1900000109,ZF2611080001,4200001234567890,5664.00,REFUND,200.00,部分退款",
		"2026-11-08 14:00:00,wx123,1900000109,ZF2611080002,4200009876543210,888.00,TRADE_SUCCESS,0.00,",
		"总交易单数,2,总交易额,6552.00,总退款金额,200.00,总交易单数为拉取时间段的汇总",
		"", // 尾部空行
	}, "\n")
	recs, err := ParseTradeBill([]byte(csvData))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 { // 两笔支付 + 一笔退款行；汇总行丢弃
		t.Fatalf("解析行数 = %d, want 3: %+v", len(recs), recs)
	}
	r0 := recs[0]
	if r0.OutTradeNo != "ZF2611080001" || r0.TransactionID != "4200001234567890" ||
		r0.State != "TRADE_SUCCESS" || r0.PayFen != 566400 {
		t.Errorf("成功行解析不符: %+v", r0)
	}
	r1 := recs[1]
	if r1.State != "REFUND" || r1.RefundFen != 20000 || r1.PayFen != 566400 {
		t.Errorf("退款行解析不符: %+v", r1)
	}
	if r1.OutTradeNo != r0.OutTradeNo {
		t.Errorf("退款行应锚定同一笔交易: %+v", r1)
	}
}

// TestParseTradeBillMissingCol 缺关键列直接报错（表头漂移尽早暴露）
func TestParseTradeBillMissingCol(t *testing.T) {
	if _, err := ParseTradeBill([]byte("交易时间,商户号\n1,2")); err == nil {
		t.Fatal("缺少商户订单号/订单状态列应报错")
	}
}
