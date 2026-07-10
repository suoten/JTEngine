-- ============================================================================
-- JTE 平台级联转发规则与平台配置迁移脚本
-- AUTO-FIX-2026-07-02 [P1]
--
-- 适用数据库: SQLite (JTE 默认关系库)
-- 兼容性: 所有语句使用 IF NOT EXISTS，可安全重复执行（幂等）
--
-- 说明:
--   JTE 默认通过 Go 代码内联迁移（sqlite_extras.go 中的 migrateForwardRule/migratePlatform），
--   本脚本作为独立迁移脚本供 DBA 审计、手动初始化或跨库迁移使用。
--   两个表均被 Go 代码中的 NewSQLiteStore 自动创建，本脚本是补充而非替代。
-- ============================================================================

-- ----------------------------------------------------------------------------
-- 1. forward_rules: 809 转发规则表
--    描述"将某源平台的某类数据转发到某目标上级平台"的过滤条件
--    持久化版本，替代 YAML ForwardRules 静态配置，支持热更新
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS forward_rules (
    id                 TEXT PRIMARY KEY,          -- 规则唯一标识（如 "rule_001"）
    source_platform_id TEXT NOT NULL DEFAULT '',  -- 源下级平台 ID，空=所有平台（对应 809 登录 UserID）
    platform_id        TEXT NOT NULL DEFAULT '',  -- 目标上级平台 ID（对应 JT809PlatformConfig.ID）
    data_type          TEXT NOT NULL DEFAULT '',  -- "location" | "alarm" | "video"
    phone              TEXT DEFAULT '',           -- 车辆手机号，空=全部车辆
    alarm_types        TEXT DEFAULT '',           -- 逗号分隔报警类型，空=全部（如 "overspeed,emergency"）
    min_level          INTEGER DEFAULT 0,         -- 最低报警级别（0=全部，1=一般，2=严重，3=紧急）
    time_start         TEXT DEFAULT '',           -- 每日生效起始时间 HH:MM:SS，空=不限
    time_end           TEXT DEFAULT '',           -- 每日生效结束时间 HH:MM:SS，空=不限
    enabled            INTEGER DEFAULT 1,         -- 是否启用（0=禁用，1=启用）
    created_at         DATETIME NOT NULL,         -- 创建时间
    updated_at         DATETIME NOT NULL          -- 更新时间
);

-- source_platform_id 列在已有库上通过 ALTER TABLE 幂等添加（Go 代码中执行）
-- 新库已包含在 CREATE TABLE 中，此处仅作兼容旧库
-- 注意: SQLite 不支持 IF NOT EXISTS 于 ALTER TABLE，Go 代码中忽略错误实现幂等
-- ALTER TABLE forward_rules ADD COLUMN source_platform_id TEXT DEFAULT '';

-- 索引: 按上级平台查询规则（ListForwardRules 的主查询路径）
CREATE INDEX IF NOT EXISTS idx_forward_rules_platform  ON forward_rules(platform_id);
-- 索引: 按数据类型过滤（管理后台审计）
CREATE INDEX IF NOT EXISTS idx_forward_rules_data_type ON forward_rules(data_type);
-- 索引: 按源平台过滤（级联定向转发规则匹配）
CREATE INDEX IF NOT EXISTS idx_forward_rules_source    ON forward_rules(source_platform_id);

-- ----------------------------------------------------------------------------
-- 2. platforms: 809 级联平台配置表
--    持久化上下级平台接入信息，替代 YAML-only 的 DownstreamPlatformConfig/JT809PlatformConfig
--    支持运行时动态增删平台，通过 API 热重载（连接/断开/重连）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS platforms (
    id          TEXT PRIMARY KEY,                 -- 平台唯一标识（如 "plat_1001"）
    name        TEXT NOT NULL DEFAULT '',          -- 平台名称（显示用）
    user_id     TEXT NOT NULL DEFAULT '',          -- 809 用户 ID（数字字符串，登录时用）
    password    TEXT NOT NULL DEFAULT '',          -- 809 登录密码
    role        TEXT NOT NULL DEFAULT 'downstream',-- "downstream"（下级，被动接收）| "upstream"（上级，主动连接）
    host        TEXT DEFAULT '',                   -- 上级平台地址（role="upstream" 时使用）
    port        INTEGER DEFAULT 0,                 -- 上级平台端口（role="upstream" 时使用）
    link_type   INTEGER DEFAULT 0,                 -- 链路类型（0=主链路，1=从链路）
    downlink_id TEXT DEFAULT '',                   -- 下级平台标识（role="downstream" 时使用）
    enabled     INTEGER DEFAULT 1,                 -- 是否启用（0=禁用，1=启用）
    created_at  DATETIME NOT NULL,                 -- 创建时间
    updated_at  DATETIME NOT NULL                  -- 更新时间
);

-- 索引: 按角色查询（ListPlatforms 的主查询路径）
CREATE INDEX IF NOT EXISTS idx_platforms_role    ON platforms(role);
-- 索引: 按 809 用户 ID 查询（登录鉴权时反查平台配置）
CREATE INDEX IF NOT EXISTS idx_platforms_user_id ON platforms(user_id);

-- ----------------------------------------------------------------------------
-- 示例数据（可选，仅作参考，默认不插入）
-- ----------------------------------------------------------------------------
-- 上级平台示例（本平台作为下级主动连接）:
-- INSERT INTO platforms (id, name, user_id, password, role, host, port, link_type, enabled, created_at, updated_at)
-- VALUES ('plat_1001', '省厅监控平台', '1001', 'pass123', 'upstream', '10.0.1.100', 9001, 0, 1, datetime('now'), datetime('now'));

-- 下级平台示例（本平台作为上级被动接收连接）:
-- INSERT INTO platforms (id, name, user_id, password, role, downlink_id, enabled, created_at, updated_at)
-- VALUES ('plat_2001', '地市平台A', '2001', 'pass456', 'downstream', 'DL_2001', 1, datetime('now'), datetime('now'));

-- 转发规则示例（将地市平台A的位置数据转发到省厅平台）:
-- INSERT INTO forward_rules (id, source_platform_id, platform_id, data_type, phone, enabled, created_at, updated_at)
-- VALUES ('rule_001', 'plat_2001', 'plat_1001', 'location', '', 1, datetime('now'), datetime('now'));

-- 转发规则示例（将所有平台的紧急报警转发到省厅平台）:
-- INSERT INTO forward_rules (id, source_platform_id, platform_id, data_type, alarm_types, min_level, enabled, created_at, updated_at)
-- VALUES ('rule_002', '', 'plat_1001', 'alarm', '', 3, 1, datetime('now'), datetime('now'));

-- 转发规则示例（工作时间内的视频转发）:
-- INSERT INTO forward_rules (id, source_platform_id, platform_id, data_type, phone, time_start, time_end, enabled, created_at, updated_at)
-- VALUES ('rule_003', '', 'plat_1001', 'video', '', '08:00:00', '18:00:00', 1, datetime('now'), datetime('now'));
