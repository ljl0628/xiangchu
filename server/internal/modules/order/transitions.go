package order

import (
	"fmt"

	"github.com/xiangchu/server/internal/pkg/errs"
)

// 订单状态机（表驱动）：全部合法流转集中声明，repo 层 Transition/TransitionByChef
// 落库前过 checkEdge 闸——业务代码写错流转在数据层即被拦截，而非依赖各方法自觉。
// 新增流转：先加表（含测试），再写业务调用；与 docs/api.md 状态机注释同源。

// transition 一条合法流转边（from→to）；同一 to 可由多个业务事件触发
// （如 1→6 既有用户取消也有接单超时），事件语义见 Desc 与各 service 方法
type transition struct {
	From int8
	To   int8
	Desc string
}

// legalTransitions 全部合法流转
var legalTransitions = []transition{
	{StatusUnpaid, StatusPending, "支付成功"},
	{StatusUnpaid, StatusCancelled, "用户未支付取消"},
	{StatusPending, StatusAccepted, "乡厨接单"},
	{StatusPending, StatusRejected, "乡厨拒单"},
	{StatusPending, StatusCancelled, "用户取消/接单超时"},
	{StatusAccepted, StatusPrepping, "开始备菜"},
	{StatusPrepping, StatusServing, "开始服务"},
	{StatusServing, StatusDone, "完成履约"},
	// 售后取消边：进行中订单（2-4）售后审核同意 → 取消退款（服务不再发生）
	{StatusAccepted, StatusCancelled, "售后审核同意取消"},
	{StatusPrepping, StatusCancelled, "售后审核同意取消"},
	{StatusServing, StatusCancelled, "售后审核同意取消"},
}

// legalEdgeSet (from,to) → 合法
var legalEdgeSet = func() map[[2]int8]bool {
	m := make(map[[2]int8]bool, len(legalTransitions))
	for _, t := range legalTransitions {
		m[[2]int8{t.From, t.To}] = true
	}
	return m
}()

// terminalStates 终态（无出边）
var terminalStates = map[int8]bool{
	StatusDone:      true,
	StatusCancelled: true,
	StatusRejected:  true,
}

// checkEdge 非法流转统一拒绝；Transition 系列落库前的第一道闸
func checkEdge(from, to int8) error {
	if !legalEdgeSet[[2]int8{from, to}] {
		return errs.BadRequest(fmt.Sprintf("非法状态流转 %d→%d", from, to))
	}
	return nil
}

var statusNames = map[int8]string{
	StatusUnpaid: "待支付", StatusPending: "待接单", StatusAccepted: "已接单",
	StatusPrepping: "备菜中", StatusServing: "服务中", StatusDone: "已完成",
	StatusCancelled: "已取消", StatusRejected: "已拒绝",
}

// MermaidStateDiagram 由流转表生成状态图（stateDiagram-v2），
// 粘贴进文档由测试保证与代码同步，防文档漂移
func MermaidStateDiagram() string {
	s := "stateDiagram-v2\n"
	s += fmt.Sprintf("    [*] --> %d_%s\n", StatusUnpaid, statusNames[StatusUnpaid])
	for _, t := range legalTransitions {
		s += fmt.Sprintf("    %d_%s --> %d_%s : %s\n",
			t.From, statusNames[t.From], t.To, statusNames[t.To], t.Desc)
	}
	for st := range terminalStates {
		s += fmt.Sprintf("    %d_%s --> [*]\n", st, statusNames[st])
	}
	return s
}
