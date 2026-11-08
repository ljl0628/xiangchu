package order

import (
	"strings"
	"testing"
)

// TestStateMachineInvariants 状态表结构不变式：
// 无重复边、终态无出边、非终态有出边、全部状态从初始态可达
func TestStateMachineInvariants(t *testing.T) {
	seen := map[[2]int8]bool{}
	outgoing := map[int8]int{}
	for _, e := range legalTransitions {
		key := [2]int8{e.From, e.To}
		if seen[key] {
			t.Errorf("重复流转边 %d→%d", e.From, e.To)
		}
		seen[key] = true
		outgoing[e.From]++
	}
	for st := int8(0); st <= 7; st++ {
		if terminalStates[st] {
			if outgoing[st] > 0 {
				t.Errorf("终态 %d 不应有出边，现有 %d 条", st, outgoing[st])
			}
			continue
		}
		if outgoing[st] == 0 {
			t.Errorf("非终态 %d 无任何出边（死状态）", st)
		}
	}
	// 可达性：从待支付 BFS，0-7 全部状态必须可达
	reach := map[int8]bool{StatusUnpaid: true}
	queue := []int8{StatusUnpaid}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range legalTransitions {
			if e.From == cur && !reach[e.To] {
				reach[e.To] = true
				queue = append(queue, e.To)
			}
		}
	}
	for st := int8(0); st <= 7; st++ {
		if !reach[st] {
			t.Errorf("状态 %d 从初始态不可达", st)
		}
	}
}

// TestCheckEdge 合法边全部放行 + 非法边抽例拒绝
func TestCheckEdge(t *testing.T) {
	for _, e := range legalTransitions {
		if err := checkEdge(e.From, e.To); err != nil {
			t.Errorf("合法边 %d→%d 被误拒: %v", e.From, e.To, err)
		}
	}
	illegal := [][2]int8{
		{StatusUnpaid, StatusDone},      // 跳态
		{StatusDone, StatusPending},     // 终态出边
		{StatusCancelled, StatusDone},   // 终态出边
		{StatusPrepping, StatusPending}, // 回退
		{StatusServing, StatusAccepted}, // 回退
	}
	for _, e := range illegal {
		if err := checkEdge(e[0], e[1]); err == nil {
			t.Errorf("非法边 %d→%d 未被拦截", e[0], e[1])
		}
	}
}

// TestMermaidStateDiagram 状态图生成：行数 = 起点行 + 边数 + 终态行
func TestMermaidStateDiagram(t *testing.T) {
	diagram := MermaidStateDiagram()
	wantLines := 2 + len(legalTransitions) + len(terminalStates) // 头 + 起点 + 边 + 终态
	if got := len(strings.Split(strings.TrimSuffix(diagram, "\n"), "\n")); got != wantLines {
		t.Errorf("Mermaid 行数 = %d, want %d\ndiagram:\n%s", got, wantLines, diagram)
	}
	if !strings.Contains(diagram, "0_待支付 --> 1_待接单 : 支付成功") {
		t.Errorf("Mermaid 缺少支付成功边:\n%s", diagram)
	}
}
