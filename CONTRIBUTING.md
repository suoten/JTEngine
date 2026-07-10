# 贡献指南

感谢你对 JTE 项目的关注！欢迎提交 Issue 和 Pull Request。

## 🤝 贡献流程

### 1. Fork 仓库

- GitHub: https://github.com/suoten/jt-engine
- Gitee: https://gitee.com/suoten/jt-engine

### 2. 创建分支

```bash
git checkout -b feature/your-feature-name
# 或
git checkout -b fix/your-bugfix-name
```

### 3. 开发规范

#### 代码规范
- **Go 代码**：遵循 [Effective Go](https://go.dev/doc/effective_go) 和项目现有风格
- **Go 1.22+**：使用最新 Go 语法（`min`/`max` 内建函数、range over int 等）
- **错误处理**：不得吞掉错误，返回具体错误类型
- **日志**：使用 zap，包含 `device_id`/`trace_id` 字段
- **并发安全**：使用 `sync.RWMutex` 或 `atomic`
- **命名**：驼峰命名，导出符号加注释

#### 测试规范
- 所有新增代码必须有单元测试
- 使用表驱动测试（table-driven tests）
- 测试覆盖关键路径和边界条件
- `go test ./... -count=1` 必须通过

#### 提交规范
- 使用 Conventional Commits 格式：
  ```
  feat: 新增 808 报警附件解析
  fix: 修复 1078 RTP 转发内存泄漏
  docs: 更新部署文档
  refactor: 重构会话管理器
  test: 补充 809 协议测试
  ```
- 一个 PR 只做一件事，保持变更最小化

### 4. 提交 PR

- PR 标题遵循 Conventional Commits
- PR 描述说明：改了什么、为什么改、如何测试
- 确保 CI 全部通过（`go build` + `go test` + `go vet`）

---

## 📋 开源版范围

本仓库是 **JTE 开源版**，只包含核心引擎和 JT/T 808 + JT/T 1078 协议。

**欢迎贡献的方向**：
- 核心引擎 Bug 修复和性能优化
- 808/1078 协议消息解析补全
- 前端仪表盘 UI/UX 改进
- 部署文档和配置示例完善
- 单元测试和集成测试补充

**不在开源版范围**（属于付费模块，请勿提交）：
- 809/905/1045/1253/32960 等扩展协议
- AI 智能分析 / NLP
- 集群部署
- 地方标准协议
- 国密加密增强
- 等保 2.0 审计模块

如果你实现了上述付费模块的功能，我们可能会将其纳入付费模块体系，请先通过 Issue 沟通。

---

## 🏗️ 开发环境搭建

### 前置条件
- Go 1.22+
- Node.js 18+（前端开发）
- MySQL 8.0+ 或 SQLite
- Redis（可选）
- TDengine 3.8+（可选，时序数据）
- MinIO（可选，对象存储）

### 本地开发

```bash
# 克隆仓库
git clone https://github.com/suoten/jt-engine.git
cd jt-engine/jte

# 安装依赖
go mod download

# 编译
make build

# 运行测试
make test

# 启动开发服务器（SQLite 模式，无需外部依赖）
go run ./cmd/jte serve --config configs/jte.yaml

# 前端开发
cd web
npm install
npm run dev
```

### 项目结构

```
jte/
├── cmd/jte/              # 主程序入口
├── internal/             # 内部包（不对外暴露）
│   ├── api/              # REST API + WebSocket
│   ├── config/           # 配置管理
│   ├── gateway/          # 协议网关（TCP/UDP）
│   ├── module/           # 模块加载器
│   ├── security/         # 安全（鉴权/限流/登录守卫）
│   ├── audit/            # 审计日志
│   └── ...
├── pkg/                  # 公共包（可被外部模块引用）
│   ├── protocol/         # JT808/JT1078 协议编解码
│   ├── storage/          # 存储抽象层
│   ├── crypto/gmsm/      # 国密 SM2/SM3/SM4
│   └── ...
├── web/                  # Vue3 前端
└── configs/              # 配置文件
```

---

## 🐛 报告 Bug

提交 Issue 时请包含：
1. JTE 版本（`jte version`）
2. 操作系统和 Go 版本
3. 复现步骤
4. 期望行为和实际行为
5. 相关日志（脱敏后）

---

## 💡 功能建议

如果你有功能建议，请先搜索现有 Issue 避免重复，然后提交 Feature Request Issue。

对于付费模块相关的功能需求，我们会在评估后决定是否纳入开源版或付费模块体系。

---

## 📄 贡献者协议

提交 PR 即表示你同意将代码以 AGPL-3.0 协议开源，并授权 JTE 团队在任何许可下使用你的贡献（用于付费模块时无需额外授权）。

---

再次感谢你的贡献！🚗💨
