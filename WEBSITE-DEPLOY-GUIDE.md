# JTE 官网部署完整教程

> 本教程面向 **JTE 项目运营方**，从零部署官网到服务器：数据库准备 → License 密钥生成 → 支付配置 → 官网上线。
>
> 官网负责：用户注册 → 在线购买 → 生成授权码 → 模块下载 → 授权验证/吊销。

---

## 目录

- [架构概览](#架构概览)
- [第一部分：环境准备](#第一部分环境准备)
- [第二部分：数据库部署（PostgreSQL）](#第二部分数据库部署postgresql)
- [第三部分：License 密钥对生成](#第三部分license-密钥对生成)
- [第四部分：支付配置](#第四部分支付配置)
- [第五部分：邮件配置](#第五部分邮件配置)
- [第六部分：Docker 部署官网](#第六部分docker-部署官网)
- [第七部分：K8s 生产级部署](#第七部分k8s-生产级部署)
- [第八部分：源码编译部署](#第八部分源码编译部署)
- [第九部分：Nginx 反向代理 + HTTPS](#第九部分nginx-反向代理--https)
- [第十部分：验证与上线检查](#第十部分验证与上线检查)
- [第十一部分：日常运维](#第十一部分日常运维)
- [附录：完整配置文件参考](#附录完整配置文件参考)

---

## 架构概览

```
用户浏览器
    │
    ▼
Nginx (443/HTTPS)  ──── TLS 证书
    │
    ├── /  → Vue3 前端静态文件 (port 3000 build → /app/web/dist)
    │
    └── /api/ → Go 后端 API (port 8081)
                    │
                    ├── PostgreSQL (5432) — 用户/订单/授权码
                    ├── License RSA 密钥  — 签发/验签授权码
                    ├── 微信支付 API       — 在线收款
                    ├── 支付宝 API         — 在线收款
                    └── SMTP 邮件          — 自动发码/通知
```

| 组件 | 端口 | 说明 |
|------|------|------|
| 前端（Vite build） | 静态文件 | Vue3 + Element Plus |
| 后端 API | 8081 | Go + Gin |
| PostgreSQL | 5432 | 用户/订单/授权数据 |

---

## 第一部分：环境准备

### 前置要求

| 项目 | 要求 |
|------|------|
| 操作系统 | Linux（Ubuntu 22.04 / CentOS 8+） |
| Docker 方式 | Docker 24+ 和 Docker Compose v2 |
| K8s 方式 | Kubernetes 1.24+ |
| 源码方式 | Go 1.22+ 和 Node.js 20+ |
| 域名 | 已备案的域名（如 jte.yourcompany.com） |
| SSL 证书 | Let's Encrypt 免费 或 商业证书 |
| PostgreSQL | 14+ |
| 支付 | 微信支付商户号 或 支付宝商户应用 |

### 准备域名和证书

```bash
# 安装 certbot（Let's Encrypt 免费证书）
sudo apt install -y certbot python3-certbot-nginx

# 申请证书（需先配置 Nginx 指向域名）
sudo certbot --nginx -d jte.yourcompany.com
```

---

## 第二部分：数据库部署（PostgreSQL）

### Docker 部署 PostgreSQL

```bash
docker run -d --name jte-postgres \
  -p 5432:5432 \
  -e POSTGRES_USER=jte \
  -e POSTGRES_PASSWORD=你的强密码 \
  -e POSTGRES_DB=jte_website \
  -v jte-pg-data:/var/lib/postgresql/data \
  postgres:14-alpine
```

### 验证数据库

```bash
docker exec -it jte-postgres psql -U jte -d jte_website -c "\dt"
# 首次启动官网后会自动建表（GORM AutoMigrate），此时为空表
```

---

## 第三部分：License 密钥对生成

**这是最关键的一步**——RSA 密钥对用于签发和验证授权码。私钥只在官网保管，公钥内嵌在开源引擎中。

### 步骤 1：生成 RSA-2048 密钥对

```bash
# 创建密钥目录
mkdir -p /opt/jte-website/configs

# 生成 RSA 私钥（PKCS#1 格式）
openssl genrsa -out /opt/jte-website/configs/license_private_key.pem 2048

# 从私钥导出公钥
openssl rsa -in /opt/jte-website/configs/license_private_key.pem \
  -pubout -out /opt/jte-website/configs/license_public_key.pem

# 设置权限（私钥仅 root 可读）
chmod 600 /opt/jte-website/configs/license_private_key.pem
chmod 644 /opt/jte-website/configs/license_public_key.pem
```

### 步骤 2：验证密钥对

```bash
# 查看私钥
openssl rsa -in /opt/jte-website/configs/license_private_key.pem -text -noout | head -5

# 查看公钥
cat /opt/jte-website/configs/license_public_key.pem
```

### 步骤 3：将公钥嵌入开源引擎

**重要：** 开源引擎的 `internal/module/signature.go` 中已内嵌了官方公钥。如果你生成了**自己的密钥对**，需要：

1. 将你的公钥 PEM 内容替换到 `jte/internal/module/signature.go` 的 `officialPublicKeyPEM` 变量中
2. 重新编译开源引擎
3. 所有从你官网购买的模块签名也必须用你的私钥签名

> 如果使用项目自带的密钥对（仅开发测试用），则跳过此步骤。**生产环境必须生成自己的密钥对**。

### 步骤 4：生成离线解绑密钥

```bash
# 生成离线解绑 HMAC 密钥（与 JTE 引擎保持一致）
export OFFLINE_UNBIND_SECRET=$(openssl rand -base64 48)
echo $OFFLINE_UNBIND_SECRET
# 记录此值，需同时配置在官网和 JTE 引擎中
```

---

## 第四部分：支付配置

### 微信支付

1. 登录 [微信支付商户平台](https://pay.weixin.qq.com)
2. 获取以下信息：

| 配置项 | 获取方式 |
|--------|----------|
| `wx_app_id` | 微信公众平台 → 应用基本信息 → AppID |
| `wx_mch_id` | 微信支付商户平台 → 账户中心 → 商户号 |
| `wx_api_key` | 商户平台 → 账户中心 → API 安全 → 设置 API 密钥 |
| `wx_notify_url` | `https://jte.yourcompany.com/api/v1/payment/wechat/callback` |

### 支付宝

1. 登录 [支付宝开放平台](https://open.alipay.com)
2. 创建应用 → 获取以下信息：

| 配置项 | 获取方式 |
|--------|----------|
| `ali_app_id` | 应用详情 → APPID |
| `ali_private_key` | 应用详情 → 接口加签方式 → RSA2 → 上传公钥后下载私钥 |
| `ali_public_key` | 支付宝公钥（用于验签回调） |
| `ali_notify_url` | `https://jte.yourcompany.com/api/v1/payment/alipay/callback` |

### 生成支付宝密钥对

```bash
# 生成应用私钥
openssl genrsa -out app_private_key.pem 2048

# 导出应用公钥（上传到支付宝开放平台）
openssl rsa -in app_private_key.pem -pubout -out app_public_key.pem

# 上传 app_public_key.pem 内容到支付宝后，获取支付宝公钥
# 将支付宝公钥保存为 alipay_public_key.pem
```

---

## 第五部分：邮件配置

官网在支付成功后自动通过邮件发送授权码。

### QQ 邮箱配置

1. 登录 QQ 邮箱 → 设置 → 账户 → 开启 SMTP 服务
2. 获取授权码（非 QQ 密码）

```yaml
mailer:
  host: "smtp.qq.com"
  port: 465
  username: "your_qq@qq.com"
  password: "你的QQ邮箱授权码"
  from: "JTE <your_qq@qq.com>"
  use_ssl: true
```

### 163 邮箱配置

```yaml
mailer:
  host: "smtp.163.com"
  port: 465
  username: "your_email@163.com"
  password: "你的163邮箱授权码"
  from: "JTE <your_email@163.com>"
  use_ssl: true
```

### 阿里云邮件推送（生产推荐）

```yaml
mailer:
  host: "smtpdm.aliyun.com"
  port: 465
  username: "noreply@yourdomain.com"
  password: "阿里云邮件推送密码"
  from: "JTE <noreply@yourdomain.com>"
  use_ssl: true
```

---

## 第六部分：Docker 部署官网

### 步骤 1：准备配置文件

```bash
mkdir -p /opt/jte-website/configs

cat > /opt/jte-website/configs/config.yaml << 'EOF'
server:
  port: 8081

database:
  host: jte-postgres        # Docker 网络中的容器名
  port: 5432
  user: jte
  password: "你的PostgreSQL密码"
  dbname: jte_website
  sslmode: disable

jwt:
  secret_key: "用 openssl rand -base64 48 生成"
  access_expiry_hours: 24
  refresh_expiry_hours: 168

payment:
  wx_app_id: "你的微信AppID"
  wx_mch_id: "你的微信商户号"
  wx_api_key: "你的微信API密钥"
  wx_notify_url: "https://jte.yourcompany.com/api/v1/payment/wechat/callback"
  ali_app_id: "你的支付宝AppID"
  ali_private_key: |
    -----BEGIN RSA PRIVATE KEY-----
    你的支付宝应用私钥PEM内容
    -----END RSA PRIVATE KEY-----
  ali_public_key: |
    -----BEGIN PUBLIC KEY-----
    你的支付宝公钥PEM内容
    -----END PUBLIC KEY-----
  ali_notify_url: "https://jte.yourcompany.com/api/v1/payment/alipay/callback"

logging:
  level: info
  format: json

mailer:
  host: "smtp.qq.com"
  port: 465
  username: "your_qq@qq.com"
  password: "你的QQ邮箱授权码"
  from: "JTE <your_qq@qq.com>"
  use_ssl: true

license:
  private_key_path: "/app/configs/license_private_key.pem"
  offline_unbind_secret: "你的离线解绑密钥"
EOF
```

### 步骤 2：创建 docker-compose.yml

```bash
cat > /opt/jte-website/docker-compose.yml << 'EOF'
version: "3.8"

services:
  postgres:
    image: postgres:14-alpine
    restart: always
    environment:
      POSTGRES_USER: jte
      POSTGRES_PASSWORD: "你的PostgreSQL密码"
      POSTGRES_DB: jte_website
    volumes:
      - jte-pg-data:/var/lib/postgresql/data
    ports:
      - "5432:5432"

  website:
    build:
      context: /opt/jte-website/src
      dockerfile: Dockerfile
    restart: always
    ports:
      - "8081:8081"
    volumes:
      - ./configs:/app/configs
      - ./modules:/app/modules        # 编译好的 .so 模块文件
      - ./downloads:/app/downloads    # 客户下载的模块文件
    depends_on:
      - postgres
    environment:
      - DB_HOST=postgres
      - DB_PORT=5432
      - DB_USER=jte
      - DB_PASSWORD=你的PostgreSQL密码
      - DB_NAME=jte_website

volumes:
  jte-pg-data:
EOF
```

### 步骤 3：构建并启动

```bash
# 将官网源码复制到服务器
# 从开发环境：scp -r jte-website/ user@server:/opt/jte-website/src

cd /opt/jte-website
docker compose up -d --build
```

### 步骤 4：验证

```bash
# 健康检查
curl http://localhost:8081/api/v1/health
# 预期: {"status":"ok"}

# 查看日志
docker compose logs -f website
```

---

## 第七部分：K8s 生产级部署

### 步骤 1：创建命名空间和密钥

```bash
kubectl create namespace jte-website

# 创建 License 私钥 Secret
kubectl create secret generic license-keys \
  --from-file=license_private_key.pem=/opt/jte-website/configs/license_private_key.pem \
  -n jte-website

# 创建数据库密码 Secret
kubectl create secret generic db-secret \
  --from-literal=password='你的PostgreSQL密码' \
  -n jte-website
```

### 步骤 2：创建 ConfigMap

```bash
kubectl apply -f - << 'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: jte-website-config
  namespace: jte-website
data:
  config.yaml: |
    server:
      port: 8081
    database:
      host: postgres-svc
      port: 5432
      user: jte
      password: "从Secret注入"
      dbname: jte_website
      sslmode: disable
    jwt:
      secret_key: "你的JWT密钥"
      access_expiry_hours: 24
      refresh_expiry_hours: 168
    payment:
      wx_app_id: "你的微信AppID"
      wx_mch_id: "你的微信商户号"
      wx_api_key: "你的微信API密钥"
      wx_notify_url: "https://jte.yourcompany.com/api/v1/payment/wechat/callback"
      ali_app_id: ""
      ali_private_key: ""
      ali_public_key: ""
      ali_notify_url: ""
    logging:
      level: info
      format: json
    mailer:
      host: ""
      port: 465
      username: ""
      password: ""
      from: ""
      use_ssl: true
    license:
      private_key_path: "/etc/jte/license/license_private_key.pem"
      offline_unbind_secret: "你的离线解绑密钥"
EOF
```

### 步骤 3：部署 PostgreSQL

```bash
kubectl apply -f - << 'EOF'
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: postgres
  namespace: jte-website
spec:
  serviceName: postgres-svc
  replicas: 1
  selector:
    matchLabels:
      app: postgres
  template:
    metadata:
      labels:
        app: postgres
    spec:
      containers:
      - name: postgres
        image: postgres:14-alpine
        ports:
        - containerPort: 5432
        env:
        - name: POSTGRES_USER
          value: jte
        - name: POSTGRES_PASSWORD
          valueFrom:
            secretKeyRef:
              name: db-secret
              key: password
        - name: POSTGRES_DB
          value: jte_website
        volumeMounts:
        - name: pg-data
          mountPath: /var/lib/postgresql/data
  volumeClaimTemplates:
  - metadata:
      name: pg-data
    spec:
      accessModes: ["ReadWriteOnce"]
      resources:
        requests:
          storage: 20Gi
---
apiVersion: v1
kind: Service
metadata:
  name: postgres-svc
  namespace: jte-website
spec:
  selector:
    app: postgres
  ports:
  - port: 5432
    targetPort: 5432
EOF
```

### 步骤 4：部署官网

```bash
kubectl apply -f jte-website/deploy/k8s/website.yaml
```

### 步骤 5：创建 Ingress

```bash
kubectl apply -f - << 'EOF'
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: jte-website-ingress
  namespace: jte-website
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
spec:
  tls:
  - hosts:
    - jte.yourcompany.com
    secretName: jte-website-tls
  rules:
  - host: jte.yourcompany.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: jte-website-svc
            port:
              number: 8081
EOF
```

---

## 第八部分：源码编译部署

### 步骤 1：编译前端

```bash
cd jte-website/web
npm install --registry=https://registry.npmmirror.com
npm run build
# 产物在 dist/ 目录
```

### 步骤 2：编译后端

```bash
cd jte-website/server
go build -ldflags="-s -w" -o bin/jte-website ./cmd/server/
```

### 步骤 3：部署

```bash
mkdir -p /opt/jte-website/{configs,modules,downloads,web/dist}

# 复制文件
cp bin/jte-website /opt/jte-website/
cp -r dist/* /opt/jte-website/web/dist/
cp configs/config.yaml /opt/jte-website/configs/
cp configs/license_private_key.pem /opt/jte-website/configs/

# 启动
cd /opt/jte-website
./jte-website
```

### 步骤 4：systemd 服务

```bash
sudo tee /etc/systemd/system/jte-website.service << 'EOF'
[Unit]
Description=JTE Website
After=network.target postgresql.service

[Service]
Type=simple
User=jte
WorkingDirectory=/opt/jte-website
ExecStart=/opt/jte-website/jte-website
Environment=DB_HOST=127.0.0.1
Environment=DB_PORT=5432
Environment=DB_USER=jte
Environment=DB_PASSWORD=你的PostgreSQL密码
Environment=DB_NAME=jte_website
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl enable jte-website
sudo systemctl start jte-website
```

---

## 第九部分：Nginx 反向代理 + HTTPS

### Nginx 配置

```bash
sudo tee /etc/nginx/sites-available/jte-website << 'EOF'
server {
    listen 80;
    server_name jte.yourcompany.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name jte.yourcompany.com;

    # SSL 证书
    ssl_certificate /etc/letsencrypt/live/jte.yourcompany.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/jte.yourcompany.com/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256;

    # 前端静态文件
    root /opt/jte-website/web/dist;
    index index.html;

    location / {
        try_files $uri $uri/ /index.html;
    }

    # API 反向代理
    location /api/ {
        proxy_pass http://127.0.0.1:8081;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # 支付回调需要处理 POST
        client_max_body_size 10m;
    }

    # 模块下载
    location /downloads/ {
        alias /opt/jte-website/downloads/;
        add_header Content-Disposition "attachment";
    }

    # 安全头
    add_header X-Frame-Options "SAMEORIGIN";
    add_header X-Content-Type-Options "nosniff";
    add_header Strict-Transport-Security "max-age=63072000; includeSubDomains; preload";
}
EOF

sudo ln -sf /etc/nginx/sites-available/jte-website /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

---

## 第十部分：验证与上线检查

### 上线检查清单

| 检查项 | 命令 | 预期 |
|--------|------|------|
| 官网可访问 | `curl https://jte.yourcompany.com` | 返回首页 HTML |
| API 健康 | `curl https://jte.yourcompany.com/api/v1/health` | `{"status":"ok"}` |
| 数据库连接 | `docker exec jte-postgres psql -U jte -d jte_website -c "\dt"` | 有表 |
| License 公钥 | `curl https://jte.yourcompany.com/api/v1/license/public-key` | 返回 PEM 公钥 |
| HTTPS 证书 | `openssl s_client -connect jte.yourcompany.com:443` | 证书有效 |
| 邮件发送 | 注册账号 → 检查邮箱 | 收到验证邮件 |
| 支付回调 | 微信/支付宝沙箱测试 | 回调成功 |

### 模块文件准备

官网需要预置编译好的 `.so` + `.sig` 模块文件供客户下载：

```bash
# 在开发环境编译模块（Linux）
cd jte-modules/module-protocol-809
go build -buildmode=plugin -o protocol_809.so .

# 签名模块
cd jte-modules/tools/sign-module
go run main.go -key /path/to/license_private_key.pem \
  -module /path/to/protocol_809.so \
  -out /path/to/protocol_809.so.sig

# 复制到官网下载目录
cp protocol_809.so protocol_809.so.sig /opt/jte-website/downloads/
```

### 测试购买流程

1. 访问官网 → 注册账号
2. 选择模块 → 发起支付（先用沙箱）
3. 支付成功 → 检查是否收到授权码邮件
4. 在 JTE 引擎中激活授权码 → 验证通过
5. 从官网下载模块 .so + .sig → 放入引擎 modules/ 目录
6. 重启引擎 → 验证模块加载成功

---

## 第十一部分：日常运维

### 备份

```bash
# 每日备份 PostgreSQL
docker exec jte-postgres pg_dump -U jte jte_website | gzip > /backup/jte_$(date +%Y%m%d).sql.gz

# 备份 License 私钥（一次性，妥善保管！）
cp /opt/jte-website/configs/license_private_key.pem /backup/
```

### 查看授权码使用情况

```sql
-- 活跃授权码
SELECT id, customer_id, tier, modules, expires_at, machine_fingerprint
FROM licenses
WHERE expires_at > NOW()
ORDER BY expires_at DESC;

-- 即将到期（7天内）
SELECT id, customer_id, expires_at
FROM licenses
WHERE expires_at BETWEEN NOW() AND NOW() + INTERVAL '7 days';

-- 已到期
SELECT id, customer_id, expires_at
FROM licenses
WHERE expires_at < NOW();
```

### 吊销授权码

```sql
-- 标记为已吊销
UPDATE licenses SET status = 'revoked' WHERE id = 'LIC-2026-xxxx';
```

引擎下次联网验证时（每 24 小时）会检测到吊销并自动停用模块。

### 更新模块文件

当模块有版本更新时：

```bash
# 1. 编译新版本模块
cd jte-modules/module-protocol-809
go build -buildmode=plugin -o protocol_809_v2.so .

# 2. 签名
go run ../tools/sign-module/main.go \
  -key /opt/jte-website/configs/license_private_key.pem \
  -module protocol_809_v2.so \
  -out protocol_809_v2.so.sig

# 3. 替换下载目录
cp protocol_809_v2.so protocol_809_v2.so.sig /opt/jte-website/downloads/

# 4. 更新数据库中的模块版本
psql -U jte -d jte_website -c \
  "UPDATE module_versions SET version='2.0.0', file_path='protocol_809_v2.so' WHERE name='protocol_809';"
```

---

## 附录：完整配置文件参考

```yaml
# /opt/jte-website/configs/config.yaml

server:
  port: 8081

database:
  host: 127.0.0.1            # Docker 用容器名，K8s 用 Service 名
  port: 5432
  user: jte
  password: "强密码"
  dbname: jte_website
  sslmode: disable

jwt:
  secret_key: "openssl rand -base64 48 生成"  # 必须修改！
  access_expiry_hours: 24
  refresh_expiry_hours: 168

payment:
  # 微信支付
  wx_app_id: "wx1234567890abcdef"
  wx_mch_id: "1234567890"
  wx_api_key: "32位API密钥"
  wx_notify_url: "https://jte.yourcompany.com/api/v1/payment/wechat/callback"

  # 支付宝
  ali_app_id: "2021001234567890"
  ali_private_key: |
    -----BEGIN RSA PRIVATE KEY-----
    MIIEowIBAAKCAQEA...
    -----END RSA PRIVATE KEY-----
  ali_public_key: |
    -----BEGIN PUBLIC KEY-----
    MIIBIjANBgkqhkiG9w0BAQE...
    -----END PUBLIC KEY-----
  ali_notify_url: "https://jte.yourcompany.com/api/v1/payment/alipay/callback"

logging:
  level: info
  format: json

mailer:
  host: "smtp.qq.com"
  port: 465
  username: "noreply@yourcompany.com"
  password: "邮箱授权码"
  from: "JTE <noreply@yourcompany.com>"
  use_ssl: true

license:
  private_key_path: "/app/configs/license_private_key.pem"
  offline_unbind_secret: "openssl rand -base64 48 生成"
```

---

## 附录：官网 API 接口一览

| 方法 | 路径 | 说明 | 认证 |
|------|------|------|------|
| POST | `/api/v1/auth/register` | 用户注册 | 无 |
| POST | `/api/v1/auth/login` | 用户登录 | 无 |
| GET | `/api/v1/health` | 健康检查 | 无 |
| GET | `/api/v1/license/public-key` | 获取 License 公钥 | 无 |
| GET | `/api/v1/products` | 产品列表 | 无 |
| POST | `/api/v1/orders` | 创建订单 | JWT |
| GET | `/api/v1/orders` | 我的订单 | JWT |
| POST | `/api/v1/payment/wechat/callback` | 微信支付回调 | 微信签名 |
| POST | `/api/v1/payment/alipay/callback` | 支付宝回调 | 支付宝签名 |
| POST | `/api/v1/license/verify` | 验证授权码 | JWT |
| POST | `/api/v1/license/bind` | 绑定机器指纹 | JWT |
| POST | `/api/v1/license/renew` | 续期授权 | JWT |
| POST | `/api/v1/license/upgrade` | 升级授权 | JWT |
| GET | `/api/v1/modules/download` | 下载模块 | JWT + License |
| GET | `/api/v1/modules/:name` | 模块详情 | JWT |
