package runtime

import (
	"fmt"
	"net/http"
	"strings"
)

// newInstallHTTPClient 构造预置安装专用 HTTP 客户端：
// 超时、HTTPS 跳转限制、主机白名单（官方发行渠道）。
func newInstallHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultInstallTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("下载重定向次数过多")
			}
			if req.URL == nil || !strings.EqualFold(req.URL.Scheme, "https") {
				return fmt.Errorf("下载重定向必须使用 HTTPS")
			}
			host := strings.ToLower(req.URL.Hostname())
			if !allowedInstallHost(host) {
				return fmt.Errorf("下载重定向主机不在允许列表：%s", host)
			}
			return nil
		},
	}
}

func allowedInstallHost(host string) bool {
	switch host {
	case "nodejs.org",
		"github.com",
		"objects.githubusercontent.com",
		"release-assets.githubusercontent.com",
		"github-releases.githubusercontent.com":
		return true
	default:
		return strings.HasSuffix(host, ".githubusercontent.com")
	}
}
