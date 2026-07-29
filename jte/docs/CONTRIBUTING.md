# JTE 贡献指南

感谢您对 JTE 项目的关注！本文档描述如何参与 JTE 开源项目贡献。

## 开发环境准备

### 依赖要求

- Go 1.21+
- Node.js 18+（前端开发）
- Docker & Docker Compose（可选，用于本地部署）
- Make（使用 Makefile 命令）

### 获取源码

```bash
git clone https://github.com/suoten/jt-engine.git
cd jte
```

### 构建与测试

```bash
# 编译所有包
cd jte && make build

# 运行测试
make test

# 运行测试并生成覆盖率报告
make test-coverage

# 运行基准测试
make benchmark

# 代码检查
make lint
```

## 项目结构

```
JTE/
├── jte/                    # 主引擎
│   ├── cmd/                # 命令行入口
│   ├── internal/           # 内部包（不对外暴露）
│   │   ├── api/            # REST API + WebSocket
│   │   ├── audit/          # 等保2.0 审计日志
│   │   ├── config/         # 配置管理
│   │   ├── gateway/        # 协议网关
│   │   ├── security/       # 安全加固
│   │   └── ...
│   ├── pkg/                # 公共包（可被外部引用）
│   │   ├── protocol/       # JT/T 协议实现
│   │   ├── storage/        # 存储抽象层
│   │   ├── crypto/         # 国密算法
│   │   └── ...
│   └── deploy/             # 部署配置
├── jte-modules/            # 可插拔模块
│   ├── module-ai/          # AI 智能分析
│   ├── module-cluster/     # 集群支持
│   ├── module-storage/     # 存储适配器
│   └── ...
├── jte-website/            # 官网系统
└── .github/workflows/      # CI/CD
```

## 代码规范

### Go 代码

1. 遵循 [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)
2. 使用 `gofmt` 和 `goimports` 格式化代码
3. 错误处理：所有 error 必须被处理，不允许 `_ =`
4. 日志：使用 `zap.Logger`，禁止 `fmt.Println`（除 main 包）
5. 并发：使用 `util.SafeGo` 包装 goroutine，防止 panic 泄漏
6. 测试：新增功能必须包含单元测试

### 提交规范

```
<type>(<scope>): <subject>

<body>

<footer>
```

**type 类型：**
- `feat`: 新功能
- `fix`: Bug 修复
- `docs`: 文档更新
- `refactor`: 代码重构
- `test`: 测试相关
- `chore`: 构建/工具链
- `security`: 安全修复

**示例：**
```
feat(protocol): 支持 JT/T 808-2019 国密 SM4 加密

新增 0x0100 终端注册消息的 SM4 加密支持，
兼容明文和密文两种模式。

Closes #123
```

### PR 流程

1. Fork 仓库并创建特性分支：`git checkout -b feat/my-feature`
2. 编写代码并确保通过所有测试：`make test`
3. 运行代码检查：`make lint`
4. 提交 PR，描述变更内容和测试结果
5. 等待 CI 通过和 Code Review

## 模块开发

JTE 支持可插拔模块架构。开发新模块：

1. 在 `jte-modules/` 下创建 `module-xxx/`
2. 实现 `module-adapter` 的 `Module` 接口
3. 在 `jte/configs/config.yaml` 中注册模块
4. 编写模块测试

## 安全相关

- 发现安全漏洞请**不要**公开提交 Issue
- 请发送邮件至 security@suoten.com
- 我们会在 48 小时内响应

## 许可证

JTE 基于 Apache License 2.0 开源。
