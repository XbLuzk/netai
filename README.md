# LogicMap

**[English](#english) | [中文](#chinese)**

---

<a name="english"></a>

## LogicMap

A local HTTP API service that pre-builds call graph indexes for your codebase and answers natural language questions about code logic — without re-scanning files every time.

### The Problem

AI coding tools like Claude Code can explain any function, but they re-read your entire codebase on every query. For large projects, this burns tokens, takes time, and has no memory between sessions.

LogicMap indexes your codebase once and keeps it. Ask the same question a hundred times — the second answer costs almost nothing.

### How It Works

```
Register repo → Trigger index → Query in natural language
                     │                      │
                     ▼                      ▼
          Tree-sitter parses        POST /impact
          source files              {functions, depth}
                     │                      │
                     ▼                      ▼
     PostgreSQL stores           WITH RECURSIVE CTE
     call graph + pgvector  →    traverses callers N levels
     embeddings                  returns affected files + LLM summary
                     │
                     ▼
     GitHub webhook → PR opened/updated
     → auto-analyze changed functions
     → post impact comment on PR
                     │
                     ▼
     Natural language query → LLM explores call graph
     via Tool Use → streams answer back via SSE
```

### Features

- **Change impact analysis** — given a function name, LogicMap tells you exactly which callers will break, across how many files, N levels deep — deterministic call graph traversal, not probabilistic semantic search
- **GitHub PR auto-comment** — install the webhook once; every PR automatically gets a comment listing affected callers, files, and an LLM-written summary. No human trigger needed
- **Multi-language** — Tree-sitter parses Go, Python, TypeScript, and 40+ more
- **Pre-built index** — parse once, query fast; incremental re-index on changes
- **LLM Tool Use** — the AI autonomously explores your call graph, not a fixed query chain
- **Streaming responses** — answers stream back via Server-Sent Events
- **Multi-model** — OpenAI, Anthropic, or local Ollama (no data leaves your machine)
- **Async indexing** — Redis Streams queue, concurrent workers, non-blocking API
- **Single `docker-compose up`** — Postgres + pgvector + Redis + the service, no other dependencies

### Quick Start

```bash
# Start everything
docker-compose up

# Register your codebase
curl -X POST http://localhost:8080/repos \
  -H "Content-Type: application/json" \
  -d '{"path": "/path/to/your/project"}'

# Trigger indexing
curl -X POST http://localhost:8080/repos/{repo_id}/index

# Check indexing status
curl http://localhost:8080/tasks/{task_id}

# Query — streams back via SSE
curl -N -X POST http://localhost:8080/query \
  -H "Content-Type: application/json" \
  -d '{"repo_id": "...", "question": "What does processOrder do internally?"}'

# Impact analysis — who breaks if I change this function?
curl -X POST http://localhost:8080/impact \
  -H "Content-Type: application/json" \
  -d '{"repo_id": "...", "functions": ["processOrder"], "depth": 3}'
```

### GitHub Webhook

Point your GitHub repo's webhook at `POST /webhooks/github`. Every time a PR is opened or updated, LogicMap will analyze the changed functions and post a comment like:

```
## LogicMap Impact Analysis

Changing `processOrder` affects **5 callers** across **3 files**:

| Function | File | Depth |
|---|---|---|
| handleCheckout | handler.go | 1 |
| TestHandleCheckout | handler_test.go | 2 |
| ... | ... | ... |

*processOrder is called by the checkout handler which is covered by 3 tests...*
```

Set `GITHUB_WEBHOOK_SECRET` and `GITHUB_TOKEN` in `.env`, then install the webhook in your repo settings.

### Configuration

Copy `.env.example` to `.env` and fill in your keys:

```env
DATABASE_URL=postgres://...
REDIS_URL=redis://localhost:6379

# LLM backend: openai | anthropic | ollama
LLM_BACKEND=anthropic
LLM_API_KEY=sk-...

# Embedding backend: openai | ollama
EMBEDDING_BACKEND=openai
EMBEDDING_API_KEY=sk-...

QUERY_CACHE_TTL=3600
WORKER_CONCURRENCY=4
EMBEDDING_CONCURRENCY=3
STALE_TASK_THRESHOLD_MINUTES=10

# GitHub webhook (optional)
GITHUB_WEBHOOK_SECRET=your-secret
GITHUB_TOKEN=ghp_...
```

### Tech Stack

| Component | Technology |
|-----------|-----------|
| Language | Go 1.22+ |
| Parser | Tree-sitter (official Go binding) |
| Database | PostgreSQL + pgvector |
| Cache / Queue | Redis (cache + Redis Streams) |
| HTTP | chi router + SSE |
| SQL | sqlc + pgx/v5 |
| Migrations | goose |

### Status

Under active development. See `docs/plans/` for the implementation roadmap.

---

<a name="chinese"></a>

## LogicMap（中文）

一个本地 HTTP API 服务，预建代码库调用图索引，支持用自然语言查询任意函数的内部逻辑链路——无需每次重新扫描文件。

### 解决什么问题

Claude Code 之类的 AI 编程工具能解释任何函数，但每次查询都要重新读取整个代码库。对大型项目来说，这会消耗大量 token、响应缓慢，而且会话之间没有记忆。

LogicMap 一次索引代码库，永久保留。同一个问题问一百次，第二次之后几乎不消耗任何成本。

### 工作原理

```
注册代码库 → 触发索引 → 用自然语言查询
                │                  │
                ▼                  ▼
     Tree-sitter 解析         POST /impact
     源文件                   {functions, depth}
                │                  │
                ▼                  ▼
     PostgreSQL 存储         WITH RECURSIVE CTE
     调用图 + pgvector  →    递归遍历上游调用者
     向量索引               返回受影响文件 + LLM 摘要
                │
                ▼
     GitHub webhook → PR 提交时自动触发
     → 分析变更函数的影响范围
     → 自动在 PR 下回帖
                │
                ▼
     自然语言查询 → LLM 通过 Tool Use 自主探索调用图
     → SSE 流式返回答案
```

### 核心特性

- **变更影响分析** — 给定函数名，精确返回 N 层上游调用者、受影响文件——确定性的调用图遍历，不是概率语义搜索
- **GitHub PR 自动回帖** — 安装一次 webhook，每次 PR 提交自动分析变更影响，在 PR 下回帖，无需人工触发
- **多语言支持** — Tree-sitter 解析 Go、Python、TypeScript 及 40+ 种语言
- **预建索引** — 一次解析，快速查询；代码变更后增量更新
- **LLM Tool Use** — AI 自主探索调用图，而非固定查询链
- **流式响应** — 通过 Server-Sent Events 实时流式返回
- **多模型支持** — OpenAI、Anthropic 或本地 Ollama（数据不离机）
- **异步索引** — Redis Streams 队列，并发 worker，API 立即返回
- **一条命令启动** — `docker-compose up` 拉起 Postgres + pgvector + Redis + 服务

### 快速开始

```bash
# 启动所有服务
docker-compose up

# 注册代码库
curl -X POST http://localhost:8080/repos \
  -H "Content-Type: application/json" \
  -d '{"path": "/path/to/your/project"}'

# 触发索引
curl -X POST http://localhost:8080/repos/{repo_id}/index

# 查询索引状态
curl http://localhost:8080/tasks/{task_id}

# 查询（SSE 流式返回）
curl -N -X POST http://localhost:8080/query \
  -H "Content-Type: application/json" \
  -d '{"repo_id": "...", "question": "processOrder 这个函数内部做了什么？"}'

# 影响分析——改这个函数会炸哪里？
curl -X POST http://localhost:8080/impact \
  -H "Content-Type: application/json" \
  -d '{"repo_id": "...", "functions": ["processOrder"], "depth": 3}'
```

### GitHub Webhook

将仓库的 webhook 指向 `POST /webhooks/github`。每次 PR 提交时，LogicMap 会自动分析变更函数的影响并回帖：

```
## LogicMap 影响分析

修改 `processOrder` 将影响 **5 个调用者**，横跨 **3 个文件**：

| 函数 | 文件 | 调用深度 |
|---|---|---|
| handleCheckout | handler.go | 1 |
| TestHandleCheckout | handler_test.go | 2 |
| ... | ... | ... |

*processOrder 被 checkout handler 调用，该 handler 有 3 个测试覆盖...*
```

在 `.env` 中配置 `GITHUB_WEBHOOK_SECRET` 和 `GITHUB_TOKEN`，然后在仓库设置里安装 webhook 即可。

### 配置

复制 `.env.example` 为 `.env`，填写配置：

```env
DATABASE_URL=postgres://...
REDIS_URL=redis://localhost:6379

# LLM 后端：openai | anthropic | ollama
LLM_BACKEND=anthropic
LLM_API_KEY=sk-...

# Embedding 后端：openai | ollama
EMBEDDING_BACKEND=openai
EMBEDDING_API_KEY=sk-...

QUERY_CACHE_TTL=3600
WORKER_CONCURRENCY=4
EMBEDDING_CONCURRENCY=3
STALE_TASK_THRESHOLD_MINUTES=10

# GitHub webhook（可选）
GITHUB_WEBHOOK_SECRET=your-secret
GITHUB_TOKEN=ghp_...
```

### 技术栈

| 组件 | 技术选型 |
|------|---------|
| 语言 | Go 1.22+ |
| 代码解析 | Tree-sitter（官方 Go binding） |
| 数据库 | PostgreSQL + pgvector |
| 缓存 / 队列 | Redis（缓存 + Redis Streams） |
| HTTP | chi router + SSE |
| SQL | sqlc + pgx/v5 |
| 迁移工具 | goose |

### 项目状态

开发中。实现计划见 `docs/plans/`。
