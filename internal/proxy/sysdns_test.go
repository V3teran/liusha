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
