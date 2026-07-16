# wechat-bot-server

## 项目概述

独立微信机器人消息服务，从 cc-go 项目提取微信 iLink Bot 轮询和消息计数功能封装为独立 Go 服务。提供 REST API、Web 管理页面、Windows 托盘图标。配合 Claude Code Skill 实现微信消息收发。

## 项目类型：增量项目 ⚠️

本项目为增量项目（已有现有代码，首次接入 IronHive）。
baseline 当前为模板占位符，需通过以下方式填充真实基线：

**基线填充路径**（按推荐优先级）：
1. **Comprehensive track**：运行 `/ironhive-cmd-sdd-pipeline`，SDD Pipeline Stage 4-5 会自动触发 `spec-baseline-build` Skill 从现有代码提取基线
2. **Standard/Minimal track**：运行 `/ironhive-cmd-sdd-pipeline`，在 Stage 4 (SOT Reading) 发现 baseline 为空时会提示建立基线
3. **手动触发**：直接调用 `spec-baseline-build` Skill（参考 `skills/spec-baseline-build/SKILL.md`）

**重要提醒**：
- 增量项目的基线是**从代码提取**而非从零创建（spec-baseline-build Skill 强制执行此规则）
- 在基线填充完成前，所有 SDD 变更缺少 SOT 参照点，可能导致 spec 与代码实际行为偏离

## 必须遵守的规约

- 明确要求任何对话必须读取 `.project-memory/` 目录下的文件作为项目上下文记忆知识
- 所有对话必须使用中文，不能使用英文

### 项目记忆上下文知识地图

本项目的 `.project-memory/` 目录结构如下：

| 目录 | 用途 | 关键文件 |
|------|------|----------|
| **specs/** | 领域规范仓库（SOT） | `domain-index.yaml` + 各领域 `spec.md` |
| **checkpoint/** | 各需求变更检查点 | `CHG-2026-*/` 各阶段的 `*.checkpoint.json` |
| **decisions/** | 架构决策记录 | `ADR-*.md` |
| **knowledge/** | 项目知识积累 | `K-*.md` |
| **base_knowledge/** | 基础知识图谱 | 占位符目录 |
| **lessons/** | 经验教训沉淀 | 经验沉淀文档 |
| **eval/** | 评估数据 | 评估相关数据 |
| **comm/** | 沟通模板 | `*output-template.yaml` |
| **audit/** | 审计日志 | `session-state/`, `audit-hash-chain.jsonl` |
| **metrics/** | 运行时指标 | 指标数据 |
| **migration/** | 迁移记录 | `manifest-schema.json` |
| **memory/** | 项目记忆索引 | `MEMORY.md` |

此外，项目根目录的 `.base-knowledge/` 存放**组织级 NFR 基线**（`org/nfr/_index.yaml`），由 `project_init.ps1` 从 IronHive 框架复制。该目录**不在** `.project-memory/` 下，是项目根的独立资产。

## 技术栈

| 层级 | 技术 |
|------|------|
| 语言 | Go 1.24 |
| Web 框架 | Gin 1.10 |
| 桌面托盘 | getlantern/systray (Windows 专属) |
| 前端 | 纯 HTML/CSS/JS (go:embed) |
| 协议 | 微信 iLink Bot (长轮询 + CDN 上传) |
| CI/CD | GitHub Actions (多平台: Windows/macOS/Linux) |

## 项目结构

```
cmd/wechat-bot-server/   — 入口 main.go + EXE 图标 + Windows .syso
internal/
  budget/manager.go       — 消息预算计数 + 缓冲队列
  config/config.go        — JSON 配置持久化 (~/.wechat-bot-server/config.json)
  queue/queue.go          — 接收消息队列 (内存)
  server/
    api/wechat.go         — 管理 API (QR/状态/设置/断开)
    api/wechat_bot.go     — 消息 API (发送文本/图片/文件/视频, 接收消息)
    embed.go              — go:embed 前端静态资源
    router.go             — Gin 路由注册
    web/                  — 管理页面 (纯 HTML/CSS/JS)
  tray/
    tray.go               — 共享类型 + QuitCh
    tray_windows.go       — Windows 托盘实现 (systray)
    tray_nix.go           — Linux/macOS stub (信号阻塞)
  wechat/
    client.go             — 微信客户端 (轮询/登录/发送消息)
    cdn.go                — CDN 上传 (AES 加密)
    message.go            — 消息解析
    reconnect.go          — Token 续期 + 登录提醒
skills/wechat-bot.md      — Claude Code Skill
web/                      — 前端开发目录 (index.html)
```

## 核心设计

### 消息预算机制
- 用户每次发消息，机器人获得固定预算（默认 7 条）用于主动回复
- 预算耗尽后进入缓冲模式，消息排队等待下次激活
- 缓冲队列上限可配置（默认 100 条），超出后淘汰最旧文本消息
- 用户发送 `/` 激活命令：重置预算 + 清空缓冲，不进入接收队列

### Token 续期
- Token 有效期 24 小时
- 20 小时后开始推送续期提醒（可配置）
- 每 60 分钟重复提醒（可配置）
- 剩余 30 分钟时强制进入过期状态

### 端口自动切换
- 启动时探测配置端口，若被占用则递增尝试下一个端口
- 新端口持久化到 `config.json`

## 基线状态

| 组件 | 状态 | 说明 |
|------|------|------|
| baseline/system/spec-full.md | 🟡 EMPTY | 尚未建立系统基线 |
| baseline/modules/ | 🟡 EMPTY | 无模块基线 |
| domain-index.yaml | 🟡 EMPTY | `domains: {}`，尚未添加域映射 |

## 关键行为准则

减少 LLM 编码常见错误的行为准则。

**权衡**：这些指导原则倾向于谨慎而非速度。对于琐碎的任务，请自行判断。

### 1. 编码前先思考
不要妄下断言。坦诚地权衡利弊。如有疑问提出，有更简单的方法提出来。

### 2. 简单优先
用最少的代码解决问题。没有超出要求的功能，不为一次性代码做抽象。

### 3. 精准修改
只碰必须碰的东西。不改进相邻代码、不重构没问题的地方、保持与现有风格一致。

### 4. 目标驱动执行
将任务转化为可验证的目标，循环直至验证通过。

## 工作流程

### 完整开发工作流

IronHive 基于 **1+3+X+V 架构** + **SDD 规范驱动开发** 流程：

```
问题检测 → 需求重构 → 规范阅读 → 调研 → 规范决策 → 规范创建 → 规范评审
    → 计划 → 实施 → 自检 → 评审 → Ralph循环 → 归档
```

**核心角色分工**：
- **Orchestrator**：任务调度中枢，路由决策、阶段转换、螺旋控制
- **Researcher**：调研角色，收集→组织→定向
- **Developer**：开发角色，计划→实施→自检
- **Reviewer**：评审角色，验证→回顾→迭代

**复杂度分级**：
| 分级 | 阶段数 | 使用场景 |
|------|--------|----------|
| Minimal | 5阶段 | 个人快速验证 |
| Standard | 9阶段 | 团队常规需求 |
| Comprehensive | 13阶段 | 企业级 PROVE 合规 |
