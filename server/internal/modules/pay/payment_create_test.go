package pay

import (
	"testing"
	"time"
)

// TestPlanFor 在途支付单处置决策：复用 / 关旧建新
func TestPlanFor(t *testing.T) {
	now := time.Now()
	base := func(amount int64, expireIn time.Duration) *Payment {
		return &Payment{Amount: amount, ExpireTime: now.Add(expireIn)}
	}
	cases := []struct {
		name string
		p    *Payment
		want createPlan
	}{
		{"同金额有效期充足，复用", base(100, time.Hour), planReuse},
		{"同金额但临期（低于复用余量），关旧建新", base(100, 5*time.Minute), planCloseAndCreate},
		{"同金额已过期，关旧建新", base(100, -time.Minute), planCloseAndCreate},
		{"金额不同（定金→尾款），关旧建新", base(200, time.Hour), planCloseAndCreate},
	}
	for _, c := range cases {
		if got := planFor(c.p, 100, now); got != c.want {
			t.Errorf("%s: planFor = %v, want %v", c.name, got, c.want)
		}
	}
}
