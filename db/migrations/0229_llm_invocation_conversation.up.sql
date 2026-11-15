-- 0229：LLM 调用记下所在对话。
--
-- 智能体运行的调用归属任务（task_id），经任务找到对话；对话里直接发起的调用（问答、意图
-- 分类、标题生成）没有任务，此前审计记录里什么归属都没有，详情无从跳回所在对话，按对话
-- 搜索也搜不到它们。这里为这类调用记下对话；对话删除时置空，与任务外键同一语义。

ALTER TABLE public.llm_invocation
  ADD COLUMN conversation_id uuid REFERENCES public.conversation(id) ON DELETE SET NULL;
CREATE INDEX llm_invocation_conversation_idx ON public.llm_invocation USING btree (conversation_id)
  WHERE (conversation_id IS NOT NULL);

COMMENT ON COLUMN public.llm_invocation.conversation_id IS '对话里直接发起的调用（问答、意图分类、标题生成）所在的对话；智能体运行的调用为空，经 task_id 找对话。';
