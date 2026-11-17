package proxy

import (
	"context"
	"net"
	"testing"

	"github.com/miekg/dns"
)

// fakeResolver 按表回答，模拟只有本机解析得到的名字（容器服务名、内网域名、hosts 条目）。
type fakeResolver map[string][]net.IP

func (f fakeResolver) LookupIP(_ context.Context, network, host string) ([]net.IP, error) {
	var out []net.IP
	for _, ip := range f[host] {
		if (network == "ip4") == (ip.To4() != nil) {
			out = append(out, ip)
		}
	}
	if len(out) == 0 {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return out, nil
}

func query(t *testing.T, addr, name string, qtype uint16) *dns.Msg {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	resp, _, err := new(dns.Client).Exchange(m, addr)
	if err != nil {
		t.Fatalf("查询 %s: %v", name, err)
	}
	return resp
}

func TestSystemDNS_AnswersWithSystemResolver(t *testing.T) {
	d, err := startSystemDNS(fakeResolver{
		"app.internal": {net.ParseIP("10.1.2.3"), net.ParseIP("fd00::7")},
		"v4.internal":  {net.ParseIP("10.9.9.9")},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	a := query(t, d.addr, "app.internal", dns.TypeA)
	if len(a.Answer) != 1 || a.Answer[0].(*dns.A).A.String() != "10.1.2.3" {
		t.Fatalf("A 记录应取自系统解析，得 %v", a.Answer)
	}
	aaaa := query(t, d.addr, "app.internal", dns.TypeAAAA)
	if len(aaaa.Answer) != 1 || aaaa.Answer[0].(*dns.AAAA).AAAA.String() != "fd00::7" {
		t.Fatalf("AAAA 记录应取自系统解析，得 %v", aaaa.Answer)
	}
	// 只有 IPv4 的名字：AAAA 查询是「有这个名字、没有这类记录」，不能答成查无此名，
	// 否则同时查 A / AAAA 的客户端会把整个名字当成不存在。
	none := query(t, d.addr, "v4.internal", dns.TypeAAAA)
	if none.Rcode != dns.RcodeSuccess || len(none.Answer) != 0 {
		t.Fatalf("只有 IPv4 的名字查 AAAA 应为空应答，得 rcode=%d %v", none.Rcode, none.Answer)
	}
	missing := query(t, d.addr, "nope.internal", dns.TypeA)
	if len(missing.Answer) != 0 {
		t.Fatalf("解析不到的名字不应有应答，得 %v", missing.Answer)
	}
}

// 目标一次连不上（服务还没起、重启中）不能让之后的请求一直失败：拨号器每次都真正去连。
func TestSystemResolvingDialer_RetriesAfterRefused(t *testing.T) {
	dns, err := startSystemDNS(net.DefaultResolver)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dns.Close() }()
	d, err := systemResolvingDialer(dns.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// 先占一个端口再释放：此刻没有服务在听
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	_, port, _ := net.SplitHostPort(addr)
	target := net.JoinHostPort("localhost", port)

	if conn, err := d.Dial(context.Background(), "tcp", target); err == nil {
		_ = conn.Close()
		t.Fatal("端口无人监听时应连不上")
	}

	// 服务起来了：下一次连接必须成功
	l, err = net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("端口 %s 被占用，跳过：%v", addr, err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		if c, err := l.Accept(); err == nil {
			_ = c.Close()
		}
	}()
	conn, err := d.Dial(context.Background(), "tcp", target)
	if err != nil {
		t.Fatalf("服务恢复后应能连上，got %v", err)
	}
	_ = conn.Close()
}
