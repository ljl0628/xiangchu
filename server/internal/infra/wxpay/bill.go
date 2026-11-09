package wxpay

import (
	"bytes"
	"compress/gzip"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// GetTradeBill 拉取指定日期（YYYY-MM-DD）的交易账单 CSV 原文。
// 两步：申请 download_url（GET /v3/bill/tradebill）→ 下载（响应可能 gzip，按魔数识别解压）。
// 注意：账单为 T+1，当日账单次日上午 10 点后可拉；下载响应未做验签（请求侧已带商户签名，v1 从简）
func (c *Client) GetTradeBill(billDate string) ([]byte, error) {
	if c.cfg.Mock || !c.cfg.Enabled {
		return nil, errMockOnly
	}
	var out struct {
		DownloadURL string `json:"download_url"`
		HashType    string `json:"hash_type"`
		HashValue   string `json:"hash_value"`
	}
	if err := c.do(http.MethodGet,
		"/v3/bill/tradebill?bill_date="+billDate+"&bill_type=ALL", nil, &out); err != nil {
		return nil, fmt.Errorf("申请交易账单失败: %w", err)
	}
	if out.DownloadURL == "" {
		return nil, errors.New("账单 download_url 为空")
	}
	raw, err := c.doRaw(http.MethodGet, out.DownloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("下载交易账单失败: %w", err)
	}
	// gzip 魔数 1f 8b：微信按内容协商可能返回压缩流
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("账单 gzip 解压失败: %w", err)
		}
		defer zr.Close()
		data, err := io.ReadAll(zr)
		if err != nil {
			return nil, fmt.Errorf("账单 gzip 读取失败: %w", err)
		}
		return data, nil
	}
	return raw, nil
}

// BillRecord 交易账单一行（金额统一转分）。REFUND 行是该交易的退款明细行
type BillRecord struct {
	TradeTime     string
	OutTradeNo    string
	TransactionID string
	State         string // TRADE_SUCCESS / REFUND / REVOKED ...
	PayFen        int64  // 付款金额（TRADE_SUCCESS 行有效）
	RefundFen     int64  // 申请退款金额（REFUND 行有效）
}

// 账单用到的列名（微信列名可能随版本微调，取值时做包含匹配兜底）
const (
	colOutTradeNo = "商户订单号"
	colTxnID      = "微信支付订单号"
	colTxnIDAlt   = "微信订单号" // V2 风格列名兜底
	colState      = "订单状态"
	colPay        = "付款金额"
	colRefund     = "申请退款金额"
)

// ParseTradeBill 解析账单 CSV：首行为中文表头按列名定位；首列为"总"开头的是
// 汇总行及之后的内容（含第二段统计）全部丢弃；金额"元"字符串转分
func ParseTradeBill(data []byte) ([]BillRecord, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1 // 各行列数可能不齐，不按首行严格校验
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("账单 CSV 解析失败: %w", err)
	}
	if len(rows) < 2 {
		return nil, errors.New("账单 CSV 行数不足")
	}
	header := rows[0]
	idx := func(name string) int { // 精确匹配优先，包含匹配兜底
		for i, h := range header {
			if h == name {
				return i
			}
		}
		for i, h := range header {
			if strings.Contains(h, name) {
				return i
			}
		}
		return -1
	}
	iOut, iState := idx(colOutTradeNo), idx(colState)
	iTxn := idx(colTxnID)
	if iTxn < 0 {
		iTxn = idx(colTxnIDAlt)
	}
	iPay, iRefund := idx(colPay), idx(colRefund)
	if iOut < 0 || iState < 0 {
		return nil, fmt.Errorf("账单缺少关键列（商户订单号/订单状态），表头: %v", header)
	}
	var out []BillRecord
	for _, row := range rows[1:] {
		if len(row) <= iOut || row[0] == "" || strings.HasPrefix(row[0], "总") {
			continue // 空行 / 汇总行
		}
		rec := BillRecord{
			OutTradeNo: strings.TrimSpace(row[iOut]),
			State:      strings.TrimSpace(pick(row, iState)),
			TradeTime:  strings.TrimSpace(row[0]),
		}
		if iTxn >= 0 {
			rec.TransactionID = strings.TrimSpace(pick(row, iTxn))
		}
		if iPay >= 0 {
			rec.PayFen, _ = yuanToFen(pick(row, iPay))
		}
		if iRefund >= 0 {
			rec.RefundFen, _ = yuanToFen(pick(row, iRefund))
		}
		out = append(out, rec)
	}
	return out, nil
}

func pick(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return row[i]
}

// yuanToFen 账单金额"元"字符串 → 分（整数，无浮点误差）。
// 形如 "5664.00" / "0.5" / ""（空视为 0）；容忍千分位逗号与前后空白
func yuanToFen(s string) (int64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" || s == "-" {
		return 0, nil
	}
	neg := false
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		neg = s[0] == '-'
		s = s[1:]
	}
	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
	}
	if len(fracPart) > 2 { // 账单固定两位小数，超长视为脏数据
		return 0, fmt.Errorf("金额小数位超长: %q", s)
	}
	for len(fracPart) < 2 {
		fracPart += "0"
	}
	if intPart == "" {
		intPart = "0"
	}
	base, err := strconv.ParseInt(intPart+fracPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("金额解析失败: %q", s)
	}
	if neg {
		base = -base
	}
	return base, nil
}
