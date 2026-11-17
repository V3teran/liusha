/**
 * 代理接入 API 客户端（GET /proxy 系列）：代理的运行状态与根证书下载。
 */

import { API_BASE, get } from './client'
import type { ProxyStatus } from './types'

/** 代理的运行状态、客户端该连的地址与根证书指纹。 */
export function getProxyStatus(): Promise<ProxyStatus> {
  return get('/proxy')
}

/** 根证书下载地址（PEM，文件名 liusha-ca.crt）：直接作为链接下载，浏览器带上会话 Cookie。 */
export const PROXY_CA_URL = `${API_BASE}/proxy/ca.crt`
