/**
 * 客户端设置代理时填的地址。public_addr 以「:」开头时只有端口，主机即控制台所在主机（代理与控制台
 * 部署在一起）：取浏览器访问控制台用的主机名，IPv6 字面量加方括号。
 */
export function proxyAddress(publicAddr: string, consoleHost: string): string {
  if (!publicAddr.startsWith(':')) return publicAddr
  const host = consoleHost.includes(':') && !consoleHost.startsWith('[') ? `[${consoleHost}]` : consoleHost
  return host + publicAddr
}
