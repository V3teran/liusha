/**
 * 漏洞证据链的结构化视图模型：从 evaluation/repro 两个 jsonb 提炼出
 * 「复现步骤 / 机器证据 / 裁决」三段渲染所需的数据，抽屉只消费结果。
 * 纯函数（排序/归一/兜底），可单测。
 */

export interface EvidenceView {
  /** 复现步骤：配方请求渲染为 curl 等价命令（人可直接复制执行）。 */
  reproCommand: string | null
  /** 机器断言（主张成立的判据明细，逐条展示）。 */
  assertReasons: string[]
  /** exploit 请求：URL/方法/状态码/响应片段。 */
  exploit: { method: string; url: string; status: number | null; bodySnippet: string | null } | null
  /** baseline 对照（有则展示差分逻辑：正常态 vs 利用态）。 */
  baseline: { url: string; status: number | null } | null
  /** 裁决官理由（为什么判定成立）。 */
  judgeReasoning: string | null
  /** 验证档位：full_gate=机器复放+终裁 / judge_only=仅终裁。 */
  tier: string | null
}

/** POSIX shell 单引号转义：整体包进单引号，内部 ' 写作 '\''——任何字节都按字面传给 curl。 */
function shellQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`
}

/** HTTP 方法白名单形态（RFC 7230 token 子集）；不合法时落 GET，不让该字段进入命令。 */
function safeMethod(method: unknown): string {
  const m = String(method ?? 'GET').toUpperCase()
  return /^[A-Z]+$/.test(m) ? m : 'GET'
}

/** 配方请求 → curl 命令（业界报告的标准复现形态：可复制、可重放）。
 *  配方字段来自 LLM 与被测目标，可能含任意字符：所有参数经 shell 转义，复制执行
 *  不会被单引号打断，也不会执行额外命令。 */
export function buildEvidenceView(
  evaluation?: Record<string, unknown>,
  repro?: Record<string, unknown>,
): EvidenceView {
  const ev = (evaluation ?? {}) as Record<string, any>
  const rp = (repro ?? {}) as Record<string, any>
  const evidence = (ev.evidence ?? {}) as Record<string, any>

  // 复现命令：web 域配方渲染 curl；generic 域渲染 steps 文本
  let reproCommand: string | null = null
  const recipe = rp.recipe as Record<string, any> | undefined
  if (recipe?.request) {
    const req = recipe.request
    const parts = [`curl -X ${safeMethod(req.method)} ${shellQuote(String(req.url ?? ''))}`]
    if (req.headers && Object.keys(req.headers).length > 0) {
      for (const [k, v] of Object.entries(req.headers)) parts.push(`  -H ${shellQuote(`${k}: ${String(v)}`)}`)
    }
    if (req.body) parts.push(`  -d ${shellQuote(String(req.body))}`)
    reproCommand = parts.join(' \\\n')
  } else if (recipe?.steps) {
    reproCommand = String(recipe.steps)
  }

  const assertReasons: string[] = Array.isArray(evidence.assert_reasons)
    ? evidence.assert_reasons.map(String)
    : []

  let exploit: EvidenceView['exploit'] = null
  if (evidence.url || rp.recipe?.request?.url) {
    exploit = {
      method: String(evidence.method ?? rp.recipe?.request?.method ?? 'GET'),
      url: String(evidence.url ?? rp.recipe?.request?.url ?? ''),
      status: typeof evidence.exploit_status_code === 'number' ? evidence.exploit_status_code : null,
      bodySnippet: evidence.response_body_snippet ? String(evidence.response_body_snippet) : null,
    }
  }

  let baseline: EvidenceView['baseline'] = null
  if (rp.recipe?.baseline?.url) {
    baseline = {
      url: String(rp.recipe.baseline.url),
      status: typeof evidence.baseline_status_code === 'number' ? evidence.baseline_status_code : null,
    }
  }

  return {
    reproCommand,
    assertReasons,
    exploit,
    baseline,
    judgeReasoning: ev.judge_reasoning ? String(ev.judge_reasoning) : null,
    tier: ev.verification_tier ? String(ev.verification_tier) : null,
  }
}
