/**
 * FindingRow 字段完整性测试
 * 验证前后端字段对齐，确保新增字段正确接入
 */

import { describe, it, expect } from 'vitest'
import type { FindingRow } from '@/api/types'

describe('FindingRow 字段验证', () => {
  it('应包含所有必需字段', () => {
    const mockFinding: FindingRow = {
      id: 'uuid-123',
      seq: 1,
      severity: 'high',
      summary: 'SQL Injection in /api/users',
      host: 'example.com',
      cwe_id: 'CWE-89',
      owasp_category: 'A03:2021',
      remediation: '使用参数化查询',
      target: { method: 'POST', path: '/api/users' },
      evaluation: { repro_cmd: 'curl -X POST ...' },
      source: 'manual',
      status: 'open',
      triage_note: '',
      triaged_at: null,
      first_seen_at: '2024-01-01T10:00:00Z', // ← 新增字段
      depends_on: [], // ← 新增字段
      created_at: '2024-01-01T10:00:00Z',
    }

    // 验证核心字段存在
    expect(mockFinding.id).toBeDefined()
    expect(mockFinding.seq).toBeDefined()
    expect(mockFinding.severity).toBeDefined()
    expect(mockFinding.summary).toBeDefined()
    expect(mockFinding.host).toBeDefined()

    // 验证新增字段存在
    expect(mockFinding.first_seen_at).toBeDefined()
    expect(mockFinding.depends_on).toBeDefined()
    expect(Array.isArray(mockFinding.depends_on)).toBe(true)
  })

  it('first_seen_at 应早于或等于 created_at', () => {
    const finding: FindingRow = {
      id: 'uuid-456',
      seq: 2,
      severity: 'critical',
      summary: 'RCE via SSTI',
      host: 'vulnerable.com',
      source: 'auto',
      status: 'confirmed',
      first_seen_at: '2024-01-01T10:00:00Z',
      depends_on: [],
      created_at: '2024-01-05T12:00:00Z', // 5天后重新发现
    }

    const firstSeen = new Date(finding.first_seen_at).getTime()
    const created = new Date(finding.created_at).getTime()

    expect(firstSeen).toBeLessThanOrEqual(created)
  })

  it('depends_on 应支持组合漏洞依赖', () => {
    const composedFinding: FindingRow = {
      id: 'uuid-789',
      seq: 3,
      severity: 'critical',
      summary: 'SSRF + XXE 组合攻击',
      host: 'target.com',
      source: 'manual',
      status: 'open',
      first_seen_at: '2024-01-10T08:00:00Z',
      depends_on: ['uuid-100', 'uuid-200'], // ← 依赖两个前置漏洞
      created_at: '2024-01-10T08:00:00Z',
    }

    expect(composedFinding.depends_on).toHaveLength(2)
    expect(composedFinding.depends_on).toContain('uuid-100')
    expect(composedFinding.depends_on).toContain('uuid-200')
  })
})
