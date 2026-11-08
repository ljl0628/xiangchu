package wxpay

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xiangchu/server/internal/config"
)

// makeTestCert 生成自签 RSA 证书模拟平台证书
func makeTestCert(t *testing.T) (*rsa.PrivateKey, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "WeChat Payment Platform Cert"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return key, cert
}

// TestParseNotifyRealFullFlow 真实模式全链路：构造 V3 密文信封 + 签名头，
// 验证验签→解密→解析出正确支付结果；并覆盖三类非法请求
func TestParseNotifyRealFullFlow(t *testing.T) {
	key, cert := makeTestCert(t)
	c := &Client{
		cfg: config.PayConfig{
			Enabled:  true,
			Mock:     false,
			APIv3Key: "12345678901234567890123456789012", // 恰好 32 字节
		},
		http:    &http.Client{Transport: failingRT{}},
		certs:   map[string]*x509.Certificate{"TESTSERIAL0001": cert},
		certsAt: time.Now(),
	}

	// 构造明文 → AES-GCM 加密成 resource 密文信封（含金额，覆盖 amount.total 解析）
	var plain PayNotifyResource
	plain.OutTradeNo = "ZF20260101001"
	plain.TransactionID = "wxpay_txn_001"
	plain.TradeState = "SUCCESS"
	plain.Amount.Total = 100
	plainJSON, _ := json.Marshal(plain)
	nonce := "testnonce123" // 恰好 12 字节
	block, _ := aes.NewCipher([]byte(c.cfg.APIv3Key))
	gcm, _ := cipher.NewGCM(block)
	ct := gcm.Seal(nil, []byte(nonce), plainJSON, []byte("transaction"))
	enc := base64.StdEncoding.EncodeToString(ct)

	body := `{"id":"evt_1","event_type":"TRANSACTION.SUCCESS","resource":{"algorithm":"AEAD_AES_256_GCM","ciphertext":"` + enc + `","associated_data":"transaction","nonce":"testnonce123"}}`

	// 签名串 = ts + \n + nonce + \n + body + \n
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	message := ts + "\n" + "hdr-nonce" + "\n" + body + "\n"
	sum := sha256.Sum256([]byte(message))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	headers := http.Header{}
	headers.Set("Wechatpay-Serial", "TESTSERIAL0001")
	headers.Set("Wechatpay-Timestamp", ts)
	headers.Set("Wechatpay-Nonce", "hdr-nonce")
	headers.Set("Wechatpay-Signature", base64.StdEncoding.EncodeToString(sig))

	// 1) 正常链路：验签 + 解密成功，信封元数据 + 明文解码均正确
	res, err := c.ParseNotify(headers, []byte(body))
	if err != nil {
		t.Fatalf("正常链路应成功: %v", err)
	}
	if res.ID != "evt_1" || res.EventType != EventPaySuccess {
		t.Fatalf("信封元数据不符: %+v", res)
	}
	pay, err := DecodePayNotify(res.Plain)
	if err != nil {
		t.Fatal(err)
	}
	if pay.OutTradeNo != "ZF20260101001" || pay.TransactionID != "wxpay_txn_001" ||
		pay.TradeState != "SUCCESS" || pay.Amount.Total != 100 {
		t.Fatalf("支付结果解码不符: %+v", pay)
	}

	// 2) 篡改请求体 → 验签失败
	tampered := strings.Replace(body, `"associated_data":"transaction"`, `"associated_data":"bansaction"`, 1)
	if _, err := c.ParseNotify(headers, []byte(tampered)); err == nil {
		t.Fatal("篡改请求体应验签失败")
	}

	// 3) 时间戳超窗（用 11 分钟前的时间戳重新签名，签名本身有效但已过期）
	oldTs := strconv.FormatInt(time.Now().Add(-11*time.Minute).Unix(), 10)
	oldMsg := oldTs + "\n" + "hdr-nonce" + "\n" + body + "\n"
	oldSum := sha256.Sum256([]byte(oldMsg))
	oldSig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, oldSum[:])
	oldHeaders := http.Header{}
	oldHeaders.Set("Wechatpay-Serial", "TESTSERIAL0001")
	oldHeaders.Set("Wechatpay-Timestamp", oldTs)
	oldHeaders.Set("Wechatpay-Nonce", "hdr-nonce")
	oldHeaders.Set("Wechatpay-Signature", base64.StdEncoding.EncodeToString(oldSig))
	if _, err := c.ParseNotify(oldHeaders, []byte(body)); err == nil {
		t.Fatal("超窗时间戳应被拒绝")
	}

	// 4) 未知 serial → 触发证书刷新，离线 RoundTripper 返回错误
	unknownHeaders := http.Header{}
	unknownHeaders.Set("Wechatpay-Serial", "UNKNOWN0000000000")
	unknownHeaders.Set("Wechatpay-Timestamp", ts)
	unknownHeaders.Set("Wechatpay-Nonce", "hdr-nonce")
	unknownHeaders.Set("Wechatpay-Signature", base64.StdEncoding.EncodeToString(sig))
	if _, err := c.ParseNotify(unknownHeaders, []byte(body)); err == nil {
		t.Fatal("未知 serial 且刷新失败应报错")
	}
}

// failingRT 离线 RoundTripper：未知 serial 触发证书刷新时直接报错，测试不发真实网络请求
type failingRT struct{}

func (failingRT) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("offline test transport")
}

// TestParseNotifyMock mock 模式：明文 JSON 直读并强制 SUCCESS
func TestParseNotifyMock(t *testing.T) {
	c := &Client{cfg: config.PayConfig{Mock: true}}
	body := `{"out_trade_no":"ZF20260101001","transaction_id":"wx_txn_mock"}`
	res, err := c.ParseNotify(nil, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "" || res.EventType != "" {
		t.Fatalf("mock 模式不应有信封元数据: %+v", res)
	}
	pay, err := DecodePayNotify(res.Plain)
	if err != nil {
		t.Fatal(err)
	}
	if pay.OutTradeNo != "ZF20260101001" || pay.TradeState != "SUCCESS" {
		t.Fatalf("mock 解析结果不符: %+v", pay)
	}
}

// TestDecodeRefundNotify 退款通知明文解码：out_refund_no / refund_id / refund_status / 金额
func TestDecodeRefundNotify(t *testing.T) {
	plain := []byte(`{"out_trade_no":"ZF20260101001","out_refund_no":"TK20260101001",` +
		`"refund_id":"wx_rf_001","refund_status":"SUCCESS","amount":{"refund":60}}`)
	r, err := DecodeRefundNotify(plain)
	if err != nil {
		t.Fatal(err)
	}
	if r.OutRefundNo != "TK20260101001" || r.RefundID != "wx_rf_001" ||
		r.RefundStatus != "SUCCESS" || r.Amount.Refund != 60 {
		t.Fatalf("退款通知解码不符: %+v", r)
	}
}
