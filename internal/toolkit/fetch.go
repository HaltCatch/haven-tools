package toolkit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
)

// Like mature server-side fetchers, enforce the destination at the dial boundary,
// not only at URL parsing: redirects and DNS rebinding cannot reach private hosts.
var blockedNetworks = func() []netip.Prefix {
	out := []netip.Prefix{}
	for _, p := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001::/23", "2001:db8::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48"} {
		out = append(out, netip.MustParsePrefix(p))
	}
	return out
}()

func publicAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range blockedNetworks {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
func resourceURL(raw string) (*url.URL, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Opaque != "" || len(raw) > 8192 {
		return nil, errors.New("请输入公开的 HTTP / HTTPS 文件或网页链接。")
	}
	if p := u.Port(); p != "" && !((u.Scheme == "http" && p == "80") || (u.Scheme == "https" && p == "443")) {
		return nil, errors.New("仅支持 HTTP 80 / HTTPS 443 端口。")
	}
	if ip, e := netip.ParseAddr(u.Hostname()); e == nil && !publicAddress(ip) {
		return nil, errors.New("不能访问私有、回环或保留地址。")
	}
	return u, nil
}
func resourceClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConns = 4
	transport.MaxIdleConnsPerHost = 1
	transport.IdleConnTimeout = 30 * time.Second
	transport.ResponseHeaderTimeout = 20 * time.Second
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if e != nil || len(ips) == 0 {
			return nil, errors.New("无法解析下载地址。")
		}
		for _, ip := range ips {
			if !publicAddress(ip) {
				return nil, errors.New("下载目标不属于公开网络。")
			}
		}
		var last error
		for _, ip := range ips {
			var c net.Conn
			c, last = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if last == nil {
				return idleConnection{c}, nil
			}
		}
		return nil, last
	}
	return &http.Client{Transport: transport, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("重定向次数过多。")
		}
		_, e := resourceURL(r.URL.String())
		return e
	}}
}

var fetchClient = resourceClient()

type resource struct {
	URL, Filename, ContentType string
	Bytes                      int64
}

func fetchResource(ctx context.Context, raw, dest string, limit int64) (resource, error) {
	var result resource
	u, e := resourceURL(raw)
	if e != nil {
		return result, e
	}
	req, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if e != nil {
		return result, e
	}
	req.Header.Set("User-Agent", "HavenTools/1.0 (+server-side resource tools)")
	return fetchResourceRequest(fetchClient, req, dest, limit)
}

func fetchResourceRequest(client *http.Client, req *http.Request, dest string, limit int64) (resource, error) {
	var result resource
	resp, e := client.Do(req)
	if e != nil {
		return result, errors.New("下载失败：目标不可访问、不是公开地址或请求超时。")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("资源服务器返回 HTTP %d；未保存为成功结果。", resp.StatusCode)
	}
	if limit > 0 && resp.ContentLength > limit {
		return result, fmt.Errorf("文件超出 %d MiB 限制。", limit>>20)
	}
	f, e := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return result, e
	}
	var source io.Reader = resp.Body
	if limit > 0 {
		source = io.LimitReader(source, limit+1)
	}
	n, e := copyToDisk(f, source)
	ce := f.Close()
	if errors.Is(e, errDiskSpace) {
		os.Remove(dest)
		return result, e
	}
	if e != nil || ce != nil || (limit > 0 && n > limit) || n == 0 {
		os.Remove(dest)
		return result, errors.New("文件为空、超过大小限制或下载中断。")
	}
	name := path.Base(resp.Request.URL.Path)
	if _, params, e := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); e == nil && params["filename"] != "" {
		name = params["filename"]
	}
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == "/" {
		name = "download.bin"
	}
	if len([]rune(name)) > 160 {
		name = string([]rune(name)[:160])
	}
	ct, params, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if params["charset"] != "" {
		ct = mime.FormatMediaType(ct, map[string]string{"charset": params["charset"]})
	}
	if ct == "" || ct == "application/octet-stream" {
		f, e := os.Open(dest)
		if e == nil {
			head := make([]byte, 512)
			n, _ := f.Read(head)
			f.Close()
			ct = http.DetectContentType(head[:n])
		}
	}
	return resource{resp.Request.URL.String(), name, ct, n}, nil
}
