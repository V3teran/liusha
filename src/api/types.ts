/**
 * 后端镜像类型
 *
 * Go 后端 Message 和 ScanEvent 结构体无 json tag，
 * 序列化为大写首字母键。
 */

export type MessageKind = 'message' | 'event'
export type MessageRole = 'user' | 'assistant' | 'system' | 'tool'
export type ScanEventKind = 'tool_call' | 'tool_result'

/**
 * SSE 事件的工具执行元数据
 * 大写键（Go 端序列化格式）
 */
export interface ScanEvent {
  Kind: ScanEventKind
  ToolName: string
  Args: string
  Result: string
  DurationMs: number
  Err: string
}

/**
 * SSE 消息帧
 * 大写键（Go 端序列化格式）
 */
export interface Message {
  Seq: number
  ID: string
  ConversationID: string
  Role: MessageRole
  Kind: MessageKind
  Content: string
  Metadata: ScanEvent | null
  CreatedAt: string
}

/**
 * 对话会话
 * 大写键（Go 端序列化格式）
 */
export interface Conversation {
  ID: string
  Title: string
  ScanID: string
  RoleID: string
  Status: string
  CreatedAt: string
  UpdatedAt: string
}

/**
 * 扫描角色（来自 /roles 端点）
 * 小写键（Go DTO 格式）
 */
export interface Role {
  id: string
  name: string
  description: string
  mode: string
}

/**
 * 判断消息是否为 event 类型
 */
export function isEventMessage(m: Message): boolean {
  return m.Kind === 'event'
}

/**
 * 从消息中解析 ScanEvent。
 * 仅当消息类型为 event 且 Metadata 不为空时返回事件；否则返回 null。
 */
export function parseScanEvent(m: Message): ScanEvent | null {
  if (m.Kind !== 'event' || !m.Metadata) return null
  return m.Metadata
}
