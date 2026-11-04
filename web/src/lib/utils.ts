import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

/**
 * 字符串的 UTF-8 字节长度。密码规则的后端口径是字节（bcrypt 上限 72 字节），
 * 前端校验必须同口径，否则多字节字符（如 CJK）会"前端放行、后端拒绝"。
 */
export function byteLength(s: string): number {
  return new TextEncoder().encode(s).length
}
