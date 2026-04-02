---
date: 2026-04-02
topic: logicmap-code-intelligence-service
---

# LogicMap：代码逻辑链路服务

## Problem Frame

AI 辅助编程越来越普遍，代码量暴涨的同时，代码理解成本也随之上升。Claude Code 等工具能解释代码，但每次查询都要重新扫描文件、消耗大量 token，对大型项目尤为明显，且没有跨会话的持久化全局认识。

目标用户是使用 AI 编程工具的后端开发者，需要快速理解一个函数的完整逻辑链路（调用了什么、数据怎么流转、分支条件是什么），而不是每次都手动翻代码或重新喂给 AI。

本项目是一个本地 HTTP API 服务，预建代码库调用图索引，支持自然语言查询任意函数的内部逻辑链路，查询成本低且结果稳定可复用。

## 系统流程

```
┌─────────────────────────────────────────────────────┐
│                   用户操作流                          │
│                                                     │
│  1. 注册本地代码库                                    │
│     POST /repos                                     │
│         │                                           │
│  2. 触发索引                                         │
│     POST /repos/{id}/index                          │
│         │                                           │
│         ▼                                           │
│  ┌─────────────────────┐                            │
│  │   Redis Streams     │ ← 异步任务队列              │
│  └─────────────────────┘                            │
│         │                                           │
│         ▼                                           │
│  ┌─────────────────────┐                            │
│  │   Indexing Worker   │ Tree-sitter 解析            │
│  │   (Go goroutines)   │ 构建调用图 + 生成 embedding  │
│  └─────────────────────┘                            │
│         │                                           │
│         ▼                                           │
│  ┌──────────────────────────┐                       │
│  │  PostgreSQL + pgvector   │ 调用图 + 向量索引      │
│  └──────────────────────────┘                       │
│                                                     │
│  3. 自然语言查询                                      │
│     POST /query                                     │
│         │                                           │
│         ▼                                           │
│  ┌─────────────────────┐     ┌──────────────────┐  │
│  │    Redis Cache      │ hit │  返回缓存结果      │  │
│  └─────────────────────┘     └──────────────────┘  │
│         │ miss                                      │
│         ▼                                           │
│  ┌─────────────────────┐                            │
│  │    LLM Agent        │ Tool Use 探索调用图         │
│  │    (Agentic Loop)   │ → get_function_source      │
│  │                     │ → get_callees              │
│  │                     │ → get_callers              │
│  │                     │ → search_similar_code      │
│  └─────────────────────┘                            │
│         │                                           │
│         ▼                                           │
│     SSE 流式响应 → 自然语言描述 + 内联代码片段        │
└─────────────────────────────────────────────────────┘
```

## Requirements

**代码库管理**
- R1. 用户通过 API 注册本地代码库，提供本地文件路径和语言（或自动探测）
- R2. 支持手动触发全量索引（首次）和增量重建索引
- R3. 索引任务异步执行，API 立即返回任务 ID，用户可查询任务状态

**索引与存储**
- R4. 使用 Tree-sitter 解析代码，支持多语言（Go、Python、TypeScript 等）
- R5. 索引内容包括：函数完整源代码、调用关系（调用图）、每个函数的文件路径和行号（源代码用于查询时提取内联代码片段）
- R6. 每个函数生成 embedding 存入 pgvector，支持语义检索
- R7. 调用图和函数元数据存入 PostgreSQL
- R8. 索引 worker 使用 Go goroutine 并发处理文件，通过 semaphore 控制并发数

**查询**
- R9. 接受自然语言查询，输入为问题字符串 + repo_id
- R10. 查询结果以 SSE（Server-Sent Events）流式返回
- R11. 响应内容包含：自然语言逻辑链路描述 + 关键函数的内联代码片段
- R12. 查询结果写入 Redis 缓存，以 repo_id + 查询字符串的精确匹配（大小写敏感）作为缓存键，TTL 可配置

**AI Agent**
- R13. LLM 通过 Tool Use 自主探索调用图，工具集包括：
  - `get_function_source`：获取指定函数源码
  - `get_callees`：获取函数调用的所有下游函数
  - `get_callers`：获取调用该函数的所有上游函数
  - `search_similar_code`：语义搜索相似代码片段（使用 pgvector）
- R14. 支持多 LLM 后端：OpenAI、Anthropic、Ollama，通过统一 interface 抽象
- R15. LLM 先输出结构化 JSON（调用链节点和边），再渲染为自然语言 + 代码片段

**基础设施**
- R16. Redis Streams 作为索引任务队列，解耦 API 和 indexing worker
- R17. Redis 同时用于查询结果缓存
- R18. 无需用户认证（单用户本地服务）
- R19. 服务配置通过环境变量或配置文件管理（LLM API key、DB 连接等）

## Success Criteria

- 对一个 10,000 行 Go 项目完成全量索引，时间在 60 秒内
- 自然语言查询响应首字节延迟在 2 秒内（cache miss 场景）
- 相同查询命中缓存，响应延迟在 100ms 内
- 查询结果包含起点函数的所有直接上下游（第一层调用者和被调用者）不遗漏，深层链路由 LLM agent 按需探索，准确率主观评估达 80% 以上
- 服务可通过单条 `docker-compose up` 启动（PG + Redis + 服务本体）

## Scope Boundaries

- 不做 Web UI，只提供 HTTP API
- 不做多用户、认证、权限隔离
- 不分析运行时调用（只做静态分析）
- 不支持跨代码库查询
- 不做代码变更 diff 或历史分析（只分析当前状态）
- 不做文件监听自动触发（用户手动触发索引）
- MVP 阶段不支持 Git hook 自动触发

## Key Decisions

- **Tree-sitter 而非 go/ast**：支持多语言，Tree-sitter 是工业级解析器，覆盖 40+ 语言，比语言专属工具更有扩展价值
- **pgvector 而非独立向量数据库**：语义检索和关系数据合一，减少运维复杂度；单用户本地服务无需独立部署 Qdrant/Weaviate
- **Redis Streams 而非 Kafka**：项目规模不需要 Kafka；Redis 已在栈里，Streams 支持消费者组、消息 ACK、持久化，MQ 概念完整
- **SSE 而非 WebSocket**：单向流式输出，SSE 更简单，HTTP 原生支持，不需要 WebSocket 的双向通道
- **LLM Tool Use 而非预编译查询链**：LLM 自主决定探索深度，更灵活；可以根据问题复杂度动态决定调用几层

## Dependencies / Assumptions

- 用户本地已安装 Docker（服务通过 docker-compose 启动）
- Tree-sitter Go 绑定（`github.com/smacker/go-tree-sitter`）可正常解析目标语言
- LLM 服务可用（OpenAI/Anthropic API key 或本地 Ollama）

## Outstanding Questions

### Resolve Before Planning
- 无

### Deferred to Planning
- [Affects R2][Technical] 增量索引如何确定变更文件范围：比较 git HEAD 与上次索引的 commit hash，还是对比文件 mtime？
- [Affects R5][Technical] 调用图数据结构如何在 PostgreSQL 中存储：adjacency list 表，还是使用 ltree/递归 CTE 支持深度遍历？
- [Affects R13][Needs research] Tool Use 的 token 预算控制策略：如何防止 LLM 无限递归调用工具导致 token 爆炸？
- [Affects R15][Technical] 结构化 JSON 调用链的 schema 设计：节点类型、边类型、代码片段如何截断？

## Next Steps

→ `/ce:plan` 进行结构化实现规划
