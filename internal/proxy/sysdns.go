package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// IPResolver 按名字解析地址；net.Resolver（系统解析）满足它。
type IPResolver interface {
	LookupIP(ctx context.Context, network, host string) ([]net.IP, error)
}

// systemDNS 是只监听回环地址的最小 DNS 服务，把代理要连的主机名交给系统解析。
//
// 代理底层的拨号器（proxify 内置的 fastdialer）缺省轮询公共 DNS（1.1.1.1、8.8.8.8 等），
// 只在查询出错时才回退到系统解析：容器服务名、内网域名、hosts 条目、VPN 的分域解析因此
// 连错或连不上，内网主机名也会发往公共 DNS。拨号器只查本服务，代理的解析就与发起请求的
// 浏览器一致（hosts 文件、容器内置 DNS、分域解析都按操作系统的规则）。
type systemDNS struct {
	server *dns.Server
	addr   string // 回环上的 UDP 地址，供拨号器作为唯一的 DNS 服务器
}

// lookupTimeout 是单个名字的系统解析时限：拨号器自己的 DNS 查询超时更短，超时后它按出错
// 回退到系统解析，不会卡住代理。
const lookupTimeout = 10 * time.Second

func startSystemDNS(resolver IPResolver) (*systemDNS, error) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("监听回环 DNS: %w", err)
	}
	started := make(chan struct{})
	srv := &dns.Server{
		PacketConn:        pc,
		Handler:           dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) { _ = w.WriteMsg(answer(resolver, req)) }),
		NotifyStartedFunc: func() { close(started) },
	}
	served := make(chan error, 1)
	go func() { served <- srv.ActivateAndServe() }()
	select {
	case <-started:
	case err := <-served:
		_ = pc.Close()
		return nil, fmt.Errorf("启动回环 DNS: %w", err)
	}
	return &systemDNS{server: srv, addr: pc.LocalAddr().String()}, nil
}

// Close 停止服务。
func (s *systemDNS) Close() error {
	return s.server.Shutdown()
}

// answer 用系统解析回答 A / AAAA 查询。名字存在但没有所问类型的地址时给空应答而不是
// 「查无此名」：同时查 A / AAAA 的客户端会把查无此名当成整个名字不存在。
func answer(resolver IPResolver, req *dns.Msg) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(req)
	for _, q := range req.Question {
		var network string
		switch q.Qtype {
		case dns.TypeA:
			network = "ip4"
		case dns.TypeAAAA:
			network = "ip6"
		default:
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
		ips, err := resolver.LookupIP(ctx, network, strings.TrimSuffix(q.Name, "."))
		cancel()
		if err != nil {
			var dnsErr *net.DNSError
			if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
				m.Rcode = dns.RcodeServerFailure
			}
			continue
		}
		hdr := dns.RR_Header{Name: q.Name, Class: dns.ClassINET, Rrtype: q.Qtype}
		for _, ip := range ips {
			if q.Qtype == dns.TypeA {
				m.Answer = append(m.Answer, &dns.A{Hdr: hdr, A: ip.To4()})
			} else {
				m.Answer = append(m.Answer, &dns.AAAA{Hdr: hdr, AAAA: ip.To16()})
			}
		}
	}
	return m
}
