-- 0226：一个任务至多一个对话。
--
-- 对话是人与任务交互的通道，任务列表、漏洞台账、LLM 审计都按 conversation.task_id 联表
-- 带出对话 ID——同一任务关联两个对话会让这些列表出现重复行。全部建对话的路径都是一个任务
-- 一个对话、续接复用同一个，此前只靠约定，这里由部分唯一索引保证。
--
-- 存量：同一任务关联了多个对话时，保留最近更新的那个（与此前按任务反查对话的取法一致），
-- 其余解除关联（对话与消息保留）。

UPDATE public.conversation c
   SET task_id = NULL
 WHERE c.task_id IS NOT NULL
   AND c.id <> (SELECT c2.id FROM public.conversation c2
                 WHERE c2.task_id = c.task_id
                 ORDER BY c2.updated_at DESC, c2.id DESC
                 LIMIT 1);

DROP INDEX public.conversation_task_idx;
CREATE UNIQUE INDEX conversation_one_per_task ON public.conversation USING btree (task_id) WHERE (task_id IS NOT NULL);
