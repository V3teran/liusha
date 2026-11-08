import { describe, expect, it } from 'vitest'
import { buildEvidenceView } from './findingEvidence'

// 真库形态的样本（active:full 实测 finding 的 evaluation/repro 结构）
const realEvaluation = {
  node_id: 'af96e33e',
  verification_id: '5d01f219',
  verification_tier: 'full_gate',
  judge_reasoning: 'exploit 请求带 cmd=id 返回 uid=33(www-data)，基线无该特征，主张成立',
  duration_ms: 75,
  evidence: {
    url: 'http://t:8080/shell.php?cmd=id',
    method: 'GET',
    assert_passed: true,
    assert_reasons: ['status_code=200 ✓', 'body 含 "uid=33(www-data)" ✓'],
    exploit_status_code: 200,
    baseline_status_code: 200,
    response_body_snippet: 'uid=33(www-data) gid=33(www-data)\n',
  },
}
const realRepro = {
  domain: 'web',
  recipe: {
    request: {
      method: 'GET',
      url: 'http://t:8080/shell.php?cmd=id',
      headers: { Cookie: 'session=abc' },
      body: '',
    },
    baseline: { url: 'http://t:8080/shell.php', method: 'GET' },
  },
  assert: { status_code: 200, body_contains: ['uid=33(www-data)'] },
}

describe('buildEvidenceView', () => {
  it('web 域配方渲染为可复制 curl（含 header/body）', () => {
    const v = buildEvidenceView(realEvaluation, realRepro)
    expect(v.reproCommand).toContain("curl -X GET 'http://t:8080/shell.php?cmd=id'")
    expect(v.reproCommand).toContain("-H 'Cookie: session=abc'")
  })

  it('断言明细逐条提取', () => {
    const v = buildEvidenceView(realEvaluation, realRepro)
    expect(v.assertReasons).toHaveLength(2)
    expect(v.assertReasons[0]).toContain('status_code=200')
  })

  it('exploit 与 baseline 对照齐备', () => {
    const v = buildEvidenceView(realEvaluation, realRepro)
    expect(v.exploit?.status).toBe(200)
    expect(v.exploit?.bodySnippet).toContain('uid=33')
    expect(v.baseline?.url).toBe('http://t:8080/shell.php')
  })

  it('裁决理由与档位提为第一层', () => {
    const v = buildEvidenceView(realEvaluation, realRepro)
    expect(v.judgeReasoning).toContain('主张成立')
    expect(v.tier).toBe('full_gate')
  })

  it('generic 域（steps 配方）渲染为文本步骤', () => {
    const v = buildEvidenceView(undefined, { domain: 'generic', recipe: { steps: '1. 登录 2. 访问 /admin' } })
    expect(v.reproCommand).toContain('1. 登录')
  })

  it('空输入退化为空视图（区块按需不渲染）', () => {
    const v = buildEvidenceView(undefined, undefined)
    expect(v.reproCommand).toBeNull()
    expect(v.assertReasons).toHaveLength(0)
    expect(v.exploit).toBeNull()
  })
})
