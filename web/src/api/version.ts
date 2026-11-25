/**
 * 程序版本 API 客户端（GET /version）：控制台侧栏显示的版本号。
 */

import { get } from './client'

/** 程序版本：与 liusha version 打印的是同一个值（没注入版本的开发构建是 dev）。 */
export async function getVersion(): Promise<{ version: string }> {
  return get('/version')
}
