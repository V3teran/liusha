package proxy

import (
	"testing"

	"github.com/V3teran/liusha/internal/config"
)

// 上报的代理地址：显式配置优先；监听全部网卡时只给端口，监听具体地址时原样给出。
func TestPublicAddr(t *testing.T) {
	cases := []struct {
		listen, public, want string
	}{
		{"0.0.0.0:8588", "", ":8588"},
		{"[::]:8588", "", ":8588"},
		{":8588", "", ":8588"},
		{"127.0.0.1:8588", "", "127.0.0.1:8588"},
		{"192.168.1.5:8588", "", "192.168.1.5:8588"},
		{"0.0.0.0:8588", ":58588", ":58588"},
		{"0.0.0.0:8588", "proxy.example.com:58588", "proxy.example.com:58588"},
	}
	for _, c := range cases {
		got := publicAddr(config.ProxyConfig{ListenAddr: c.listen, PublicAddr: c.public})
		if got != c.want {
			t.Errorf("listen=%q public=%q：got %q，want %q", c.listen, c.public, got, c.want)
		}
	}
}
