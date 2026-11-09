package pay

import (
	"testing"

	"github.com/xiangchu/server/internal/infra/wxpay"
)

func rec(state, outNo string, pay, refund int64) wxpay.BillRecord {
	return wxpay.BillRecord{OutTradeNo: outNo, State: state, PayFen: pay, RefundFen: refund}
}

// TestAggregateBill 账单聚合：同单 SUCCESS 付款额 + REFUND 行退款额累计，无关状态行忽略
func TestAggregateBill(t *testing.T) {
	agg := aggregateBill([]wxpay.BillRecord{
		rec("TRADE_SUCCESS", "ZF1", 10000, 0),
		rec("REFUND", "ZF1", 0, 3000),
		rec("REFUND", "ZF1", 0, 2000), // 同单二次退款：累计
		rec("TRADE_SUCCESS", "ZF2", 500, 0),
		rec("REVOKED", "ZF3", 999, 0), // 未成交：不参与资金核对
	})
	if got := agg["ZF1"].PayFen; got != 10000 {
		t.Errorf("ZF1 PayFen = %d, want 10000", got)
	}
	if got := agg["ZF1"].RefundFen; got != 5000 {
		t.Errorf("ZF1 RefundFen = %d, want 5000", got)
	}
	if _, ok := agg["ZF3"]; ok {
		t.Errorf("REVOKED 行不应进入聚合（未成交）")
	}
}

// TestCompareBill 双向核对四类差异 + 完全一致零差异
func TestCompareBill(t *testing.T) {
	local := func(amount int64) *Payment {
		return &Payment{PaymentNo: "ZF1", Amount: amount}
	}

	// 完全一致（含退款一致）：零差异
	diffs, cnt, total := compareBill(
		[]wxpay.BillRecord{rec("TRADE_SUCCESS", "ZF1", 10000, 0), rec("REFUND", "ZF1", 0, 3000)},
		[]*Payment{local(10000)},
		map[string]int64{"ZF1": 3000},
	)
	if len(diffs) != 0 || cnt != 1 || total != 10000 {
		t.Errorf("一致场景 diffs=%d cnt=%d total=%d, want 0/1/10000", len(diffs), cnt, total)
	}

	// 金额不符 + 退款不符：两类差异
	diffs, _, _ = compareBill(
		[]wxpay.BillRecord{rec("TRADE_SUCCESS", "ZF1", 10000, 0), rec("REFUND", "ZF1", 0, 3000)},
		[]*Payment{local(9900)},
		map[string]int64{},
	)
	if len(diffs) != 2 || diffs[0].DiffType != DiffAmountMismatch || diffs[1].DiffType != DiffRefundMismatch {
		t.Errorf("金额/退款不符应各报一条, got %+v", diffs)
	}
	if diffs[0].WxAmount != 10000 || diffs[0].LocalAmount != 9900 {
		t.Errorf("AMOUNT_MISMATCH 两侧金额应原样落表, got %+v", diffs[0])
	}

	// 掉单：账单有本地无（最严重）
	diffs, _, _ = compareBill(
		[]wxpay.BillRecord{rec("TRADE_SUCCESS", "ZF9", 10000, 0)},
		[]*Payment{},
		map[string]int64{},
	)
	if len(diffs) != 1 || diffs[0].DiffType != DiffMissingLocal || diffs[0].OutTradeNo != "ZF9" {
		t.Errorf("本地缺失应报 MISSING_LOCAL, got %+v", diffs)
	}

	// 纯退款行且本地无支付单：支付侧缺失不重复报（掉单已由别处暴露）
	diffs, _, _ = compareBill(
		[]wxpay.BillRecord{rec("REFUND", "ZF9", 0, 500)},
		[]*Payment{},
		map[string]int64{},
	)
	if len(diffs) != 0 {
		t.Errorf("纯退款行不单独报 MISSING_LOCAL, got %+v", diffs)
	}

	// 本地有账单无
	diffs, cnt, _ = compareBill(
		[]wxpay.BillRecord{},
		[]*Payment{local(700)},
		map[string]int64{},
	)
	if len(diffs) != 1 || diffs[0].DiffType != DiffMissingBill || cnt != 0 {
		t.Errorf("账单缺失应报 MISSING_BILL, got diffs=%+v cnt=%d", diffs, cnt)
	}
}
