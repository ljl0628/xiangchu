package wxpay

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xiangchu/server/internal/config"
)

const apiBase = "https://api.mch.weixin.qq.com"

// errMockOnly is returned when real wxpay is not enabled.
var errMockOnly = errors.New("real wxpay mode not enabled")

// APIError 微信 V3 接口非 2xx 响应（调用方按 Status 区分处置，如关单遇 404 视为已关）
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("微信支付接口错误 %d: %s", e.Status, e.Body)
}

// Client 微信支付 V3 客户端：JSAPI 下单、退款、回调验签解密。
// Mock 模式下不依赖商户证书即可联调全流程。
type Client struct {
	cfg  config.PayConfig
	key  *rsa.PrivateKey
	http *http.Client

	// 微信平台证书缓存（验回调签名用），按 serial 索引；未知 serial 触发刷新
	certMu  sync.Mutex
	certs   map[string]*x509.Certificate
	certsAt time.Time // 最近一次拉取时间，限制刷新频率
}

// New 创建客户端；未启用或 Mock 时跳过私钥加载
func New(cfg config.PayConfig) (*Client, error) {
	c := &Client{cfg: cfg, http: &http.Client{Timeout: 10 * time.Second}}
	if cfg.Enabled && !cfg.Mock {
		key, err := loadPrivateKey(cfg.PrivateKeyPath)
		if err != nil {
			return nil, err
		}
		c.key = key
	}
	return c, nil
}

func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取商户私钥失败: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("私钥文件不是有效的 PEM")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if k2, ok := k.(*rsa.PrivateKey); ok {
			return k2, nil
		}
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

func nonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// authHeader 构造 V3 请求签名头（WECHATPAY2-SHA256-RSA2048）
func (c *Client) authHeader(method, canonicalURL string, body []byte) (string, error) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	n := nonce()
	message := strings.Join([]string{method, canonicalURL, ts, n, string(body), ""}, "\n")
	sum := sha256.Sum256([]byte(message))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",timestamp="%s",serial_no="%s",signature="%s"`,
		c.cfg.MchID, n, ts, c.cfg.SerialNo, base64.StdEncoding.EncodeToString(sig),
	), nil
}

// do 发起 V3 API 请求并解析 JSON 响应
func (c *Client) do(method, path string, reqBody any, out any) error {
	raw, err := c.doRaw(method, apiBase+path, reqBody)
	if err != nil {
		return err
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// doRaw 发起 V3 API 请求返回原始响应体（账单下载等非 JSON 场景）。
// url 可为绝对地址（download_url）或相对 path；签名串一律取 path+query
func (c *Client) doRaw(method, url string, reqBody any) ([]byte, error) {
	if c.cfg.Mock || !c.cfg.Enabled {
		return nil, errMockOnly
	}
	var body []byte
	if reqBody != nil {
		body, _ = json.Marshal(reqBody)
	}
	if !strings.HasPrefix(url, "http") {
		url = apiBase + url
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "xiangchu-server/1.0")
	auth, err := c.authHeader(method, req.URL.RequestURI(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", auth)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求微信支付失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	// 关单等接口成功返回 204 No Content（无响应体），一并视为成功
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return nil, &APIError{Status: resp.StatusCode, Body: string(raw)}
	}
	return raw, nil
}
