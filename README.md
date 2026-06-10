# liusha-ui

liusha 对话式扫描前端（Vue 3 + Vite + TS）。详见 liusha 仓
`docs/superpowers/specs/2026-06-10-liusha-ui-conversational-frontend-design.md`。

## Dev

需要 liusha Go API 跑在 `localhost:8080`，且后端已设 `LIUSHA_STREAM_COOKIE_SECRET`。

```bash
pnpm install
pnpm dev   # Vite dev server，/api/* 代理到 localhost:8080（同源 → cookie 生效）
```

打开页面输入 X-API-Key，选场景，输入任务发起扫描，agent 过程实时流式展示。

## Build & 部署

```bash
pnpm build   # 产出 dist/
```

prod 用反向代理把 dist/ 与 Go API 挂同一域名（`/` → dist，`/api/*` → Go API），
保持同源，使 `liusha_stream` cookie（SameSite=Lax）生效。

## Test

```bash
pnpm test    # vitest：类型解析 / store 去重 / 事件映射 / 组件渲染
```
