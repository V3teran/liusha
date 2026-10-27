// 漏洞评估详情(evaluation jsonb)的领域映射:标签、优先序、条目归类。
// 从 FindingDrawer 抽出为纯函数——排序/过滤规则可单测,渲染层只消费结果。

export interface EvItem {
  key: string
  value: string
  kind: 'code' | 'text' | 'json'
}

export const EV_LABELS: Record<string, string> = {
  repro_cmd: '复现命令',
  repro_cmd_time: '复现命令（时间盲注）',
  repro_cmd_boolean: '复现命令（布尔盲注）',
  repro_steps: '复现步骤',
  repro_response: '复现响应',
  payload: 'Payload',
  observation: '观察',
  key_observation: '关键观察',
  conclusion: '结论',
  impact: '影响',
  analysis: '分析',
  description: '描述',
  result: '结果',
  issue: '问题',
  time_delay: '时间延迟',
  response_excerpt: '响应片段',
  response_body: '响应体',
  response_status: '响应状态',
  vulnerable_endpoints: '受影响端点',
  affected_users: '受影响用户',
}

export const EV_PRIORITY: string[] = [
  'repro_cmd',
  'repro_cmd_time',
  'repro_cmd_boolean',
  'repro_steps',
  'payload',
  'repro_response',
  'response_excerpt',
  'response_body',
  'response_status',
  'time_delay',
  'observation',
  'key_observation',
  'vulnerable_endpoints',
  'affected_users',
  'analysis',
  'description',
  'issue',
  'conclusion',
  'impact',
  'result',
]

export interface EvItem {
  key: string
  value: string
  kind: 'code' | 'text' | 'json'
}

export function buildEvaluationItems(evaluation?: Record<string, unknown>): EvItem[] {
  if (!evaluation) return []
  const items: EvItem[] = []
  for (const [k, raw] of Object.entries(evaluation)) {
    if (raw == null || raw === '') continue
    let kind: EvItem['kind'] = 'text'
    let value: string
    if (typeof raw === 'object') {
      kind = 'json'
      value = JSON.stringify(raw, null, 2)
    } else {
      value = String(raw)
      if (/cmd|command|curl|repro|request|payload/i.test(k)) kind = 'code'
    }
    items.push({ key: k, value, kind })
  }
  const rank = (k: string) => {
    const i = EV_PRIORITY.indexOf(k)
    return i === -1 ? EV_PRIORITY.length : i
  }
  return items.sort((a, b) => rank(a.key) - rank(b.key))
}
