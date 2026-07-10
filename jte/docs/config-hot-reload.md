# JTE 配置热加载指南

## 概述

JTE 主引擎使用 [Viper](https://github.com/spf13/viper) 进行配置管理，原生支持配置文件热加载。
修改配置文件后，无需重启服务即可生效。

## 支持热加载的配置项

| 配置项 | 热加载 | 说明 |
|--------|--------|------|
| `api.rate_limit` | ✅ | API 限流阈值 |
| `api.cors_origins` | ✅ | CORS 白名单 |
| `gateway.max_connections` | ✅ | 最大连接数 |
| `storage.retention_days` | ✅ | 数据保留天数 |
| `crypto` | ✅ | 国密算法配置 |
| `telemetry` | ✅ | 监控指标配置 |
| `alerts` | ✅ | 告警规则（alerts.yaml） |
| `server.port` | ❌ | 需重启 |
| `database` | ❌ | 需重启（连接池已初始化） |
| `jwt.secret_key` | ❌ | 需重启（密钥已加载） |

## 使用方式

### 1. 文件监听模式（默认）

```yaml
# jte.yaml 中启用
config:
  watch: true       # 启用文件监听
  watch_interval: 5s # 轮询间隔（备用）
```

Viper 使用 `fsnotify` 监听配置文件变更：

```go
// 代码中的实现（已内置）
viper.OnConfigChange(func(e fsnotify.Event) {
    logger.Info("配置文件已变更，正在热加载", zap.String("file", e.Name))
    // 重新读取配置
    if err := viper.Unmarshal(&cfg); err != nil {
        logger.Error("热加载配置失败", zap.Error(err))
        return
    }
    // 应用新配置
    applyConfig(cfg)
    logger.Info("配置热加载完成")
})
viper.WatchConfig()
```

### 2. 在 Docker 中使用热加载

```yaml
# docker-compose.yml
services:
  jte:
    volumes:
      - ./jte/configs:/app/configs:ro  # 挂载配置目录
    # 修改宿主机 configs/jte.yaml 后，容器内自动热加载
```

### 3. 在 Kubernetes 中使用热加载

```yaml
# ConfigMap 挂载为文件，subPath 避免符号链接问题
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
      - name: jte
        volumeMounts:
        - name: config
          mountPath: /app/configs/jte.yaml
          subPath: jte.yaml
      volumes:
      - name: config
        configMap:
          name: jte-config
```

```bash
# 更新 ConfigMap 后，Pod 内文件自动更新（约 60-120 秒）
kubectl edit configmap jte-config -n jte

# 加速更新（立即生效）
kubectl rollout restart deployment jte-blue -n jte
```

### 4. API 触发配置重载

```bash
# 通过管理 API 触发配置重载（需管理员权限）
POST /api/v1/admin/config/reload
Authorization: Bearer <admin-token>

# 响应
{
  "code": 0,
  "message": "配置已重载",
  "data": {
    "reload_at": "2026-07-02T10:30:00Z"
  }
}
```

## 配置变更审计

所有配置变更都会记录到审计日志：

```
2026-07-02T10:30:00Z INFO 配置文件已变更 file=/app/configs/jte.yaml user=system
2026-07-02T10:30:00Z INFO 热加载完成 changes=["api.rate_limit: 100→200"]
```

## 最佳实践

1. **生产环境建议通过 ConfigMap + kubectl edit 管理**，避免直接修改文件
2. **敏感配置（密码、密钥）通过 Secret 注入环境变量**，不放入配置文件
3. **配置变更前备份**：`cp jte.yaml jte.yaml.bak.$(date +%Y%m%d%H%M%S)`
4. **灰度发布配置**：先在一个 Pod 上测试，确认无副作用再全量
5. **监控配置加载状态**：`/api/v1/admin/config/status` 查看当前配置版本
