---
date: 2026-04-02
topic: resume-project-direction
focus: 后端+AI应用简历项目，Go技术栈
---

# Ideation: 简历项目方向

## 背景

目标：投 AI 创业公司 / 外企，后端+AI应用岗位。需要一个用 Go+AI 技术栈、有含金量、面试能深聊的项目。

netai（嵌入式网关运维）方向已放弃，赛道太窄。

## 最终选定方向

### Codebase Logic Chain Service（代码逻辑链路服务）

**核心问题**：AI 写的代码越来越多，代码量暴涨但理解成本没降，反而成了黑盒。Claude Code 能解释代码，但每次都要重新扫文件、花大量 token，对大型项目尤为明显，且没有持久化的全局认识。

**解决方案**：预建代码库调用图索引，一次分析持久复用，支持自然语言查询任意函数的内部逻辑链路。

**与 Claude Code 的差别**：
| | Claude Code | 本项目 |
|---|---|---|
| 理解方式 | 每次查询重新扫文件 | 预建调用图，持久化 |
| 大型项目 | token 爆炸，慢 | 一次索引，查询极快 |
| 全局认识 | 没有，靠 context window | 有，call graph 存在库里 |
| 触发方式 | 手动问 | 代码变更自动增量更新 |

**技术栈（已精简，每个组件都有明确理由）**：
| 组件 | 用途 |
|------|------|
| Go | AST 解析、并发 indexing worker、HTTP API |
| PostgreSQL + pgvector | 调用图存储 + 语义检索合一，减少运维复杂度 |
| Redis | AST 缓存 + Redis Streams 消息队列 |
| LLM（多模型） | 生成自然语言逻辑链路描述 |

**AI 技术栈**：
- Tool Use / Function Calling：LLM 主动探索调用链（最有深度，agentic 模式）
- Streaming：流式输出，生产标配
- Structured Output：输出结构化 JSON，可靠可测试
- 多模型支持：OpenAI / Anthropic / Ollama 统一 interface

**面试亮点**：
- 增量索引设计（只重新分析变更文件，如何维护 call graph 一致性）
- Agentic loop：LLM 通过 tool use 自主探索调用链
- pgvector 混合检索（关键词 + 语义）
- Redis Streams 解耦 webhook 和索引任务

## 淘汰的方向

| 方向 | 淘汰理由 |
|------|---------|
| netai（网络设备运维） | 赛道太窄，嵌入式场景局限 |
| AI Code Review Service | 与 Claude Code / CodeRabbit 重叠，难以差异化 |
| 电商 AI 选品（Accio方向） | 市面已有成熟产品 |
| 代码库知识库（纯RAG） | 太 generic，技术点不够集中 |

## Session Log
- 2026-04-02: 从 netai 价值主张 ideation 转向简历项目方向探索
- 2026-04-02: 经多轮讨论，确定 Codebase Logic Chain Service 方向，技术栈精简至合理范围
