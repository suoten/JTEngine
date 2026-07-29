-- ============================================================================
-- JTE 核心业务表迁移脚本
-- AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: 从 sqlite.go 提取的 19 个核心表定义
--
-- 适用数据库: SQLite (JTE 默认关系库) / MySQL (生产环境，需调整语法)
-- 兼容性: 所有语句使用 IF NOT EXISTS，可安全重复执行（幂等）
--
-- 说明:
--   本脚本从 pkg/storage/sqlite/sqlite.go 的 migrate() 方法提取，
--   供 DBA 审计、手动初始化或跨库迁移使用。
--   Go 代码中的 NewSQLiteStore 会自动执行这些迁移，本脚本是补充而非替代。
-- ============================================================================

-- ----------------------------------------------------------------------------
-- 1. vehicles: 车辆信息表
--    存储车辆基本信息（手机号/车牌/终端ID/厂商/省市/在线状态）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS vehicles (
    id TEXT PRIMARY KEY,
    phone TEXT NOT NULL,
    protocol TEXT NOT NULL DEFAULT 'jt808',
    plate_no TEXT DEFAULT '',
    plate_color INTEGER DEFAULT 0,
    terminal_id TEXT DEFAULT '',
    terminal_type TEXT DEFAULT '',
    manufacturer TEXT DEFAULT '',
    province_id INTEGER DEFAULT 0,
    city_id INTEGER DEFAULT 0,
    online INTEGER DEFAULT 0,
    registered_at DATETIME NOT NULL,
    last_active DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_vehicles_phone ON vehicles(phone);
CREATE INDEX IF NOT EXISTS idx_vehicles_online ON vehicles(online);

-- ----------------------------------------------------------------------------
-- 2. locations: 位置数据表（轨迹回放核心表）
--    存储终端上报的 GPS 位置数据，支持按车辆+时间范围查询
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS locations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    vehicle_id TEXT NOT NULL,
    phone TEXT NOT NULL,
    latitude REAL NOT NULL,
    longitude REAL NOT NULL,
    altitude REAL DEFAULT 0,
    speed REAL DEFAULT 0,
    direction INTEGER DEFAULT 0,
    time DATETIME,
    alarm_flag INTEGER DEFAULT 0,
    status_flag INTEGER DEFAULT 0,
    mileage REAL DEFAULT 0,
    fuel REAL DEFAULT 0,
    received_at DATETIME NOT NULL,
    source TEXT DEFAULT 'jt808'
);
CREATE INDEX IF NOT EXISTS idx_locations_vehicle_id ON locations(vehicle_id);
CREATE INDEX IF NOT EXISTS idx_locations_received_at ON locations(received_at);
-- 复合索引：按 vehicle_id + received_at 时间范围查询（轨迹回放核心查询路径）
CREATE INDEX IF NOT EXISTS idx_locations_vehicle_time ON locations(vehicle_id, received_at);

-- ----------------------------------------------------------------------------
-- 3. alarms: 报警数据表
--    存储终端上报的报警事件（超速/疲劳/紧急/围栏等）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS alarms (
    id TEXT PRIMARY KEY,
    vehicle_id TEXT NOT NULL,
    phone TEXT NOT NULL,
    type TEXT DEFAULT '',
    level INTEGER DEFAULT 0,
    alarm_flag INTEGER DEFAULT 0,
    latitude REAL DEFAULT 0,
    longitude REAL DEFAULT 0,
    altitude REAL DEFAULT 0,
    speed REAL DEFAULT 0,
    direction INTEGER DEFAULT 0,
    time DATETIME,
    additional BLOB,
    received_at DATETIME NOT NULL,
    source TEXT DEFAULT 'jt808'
);
CREATE INDEX IF NOT EXISTS idx_alarms_vehicle_id ON alarms(vehicle_id);
CREATE INDEX IF NOT EXISTS idx_alarms_received_at ON alarms(received_at);

-- ----------------------------------------------------------------------------
-- 4. sessions: 终端会话表
--    存储终端连接会话状态（手机号/协议/远程地址/状态/注册时间）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    phone TEXT NOT NULL,
    protocol TEXT NOT NULL DEFAULT 'jt808',
    remote_addr TEXT DEFAULT '',
    status TEXT DEFAULT 'connected',
    registered_at DATETIME NOT NULL,
    last_active DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_phone ON sessions(phone);

-- ----------------------------------------------------------------------------
-- 5. protocol_logs: 协议日志表
--    存储原始协议报文日志（调试/审计/回放用）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS protocol_logs (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    phone TEXT NOT NULL,
    protocol TEXT NOT NULL DEFAULT 'jt808',
    msg_type INTEGER NOT NULL DEFAULT 0,
    msg_name TEXT DEFAULT '',
    direction TEXT NOT NULL DEFAULT 'up',
    raw_hex TEXT DEFAULT '',
    length INTEGER DEFAULT 0,
    received_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_proto_logs_phone ON protocol_logs(phone);
CREATE INDEX IF NOT EXISTS idx_proto_logs_received_at ON protocol_logs(received_at);

-- ----------------------------------------------------------------------------
-- 6. driver_info: 驾驶员信息表
--    存储驾驶员身份信息（姓名/从业资格证号/发证机构/有效期/身份证号）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS driver_info (
    id TEXT PRIMARY KEY,
    vehicle_id TEXT NOT NULL,
    phone TEXT NOT NULL,
    driver_name TEXT DEFAULT '',
    license_id TEXT DEFAULT '',
    license_org TEXT DEFAULT '',
    license_expiry TEXT DEFAULT '',
    id_card TEXT DEFAULT '',
    received_at DATETIME NOT NULL,
    source TEXT DEFAULT 'jt808'
);
CREATE INDEX IF NOT EXISTS idx_driver_info_vehicle_id ON driver_info(vehicle_id);

-- ----------------------------------------------------------------------------
-- 7. multimedia: 多媒体资源表
--    存储终端上报的多媒体资源元数据（音视频/图片）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS multimedia (
    id TEXT PRIMARY KEY,
    vehicle_id TEXT NOT NULL,
    phone TEXT NOT NULL,
    media_type INTEGER DEFAULT 0,
    media_format INTEGER DEFAULT 0,
    event_item INTEGER DEFAULT 0,
    channel_id INTEGER DEFAULT 0,
    latitude REAL DEFAULT 0,
    longitude REAL DEFAULT 0,
    received_at DATETIME NOT NULL,
    source TEXT DEFAULT 'jt808'
);
CREATE INDEX IF NOT EXISTS idx_multimedia_vehicle_id ON multimedia(vehicle_id);

-- ----------------------------------------------------------------------------
-- 8. can_data: CAN 总线数据表
--    存储终端上报的 CAN 总线数据项（JSON 格式）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS can_data (
    id TEXT PRIMARY KEY,
    vehicle_id TEXT NOT NULL,
    phone TEXT NOT NULL,
    items TEXT NOT NULL DEFAULT '[]',
    received_at DATETIME NOT NULL,
    source TEXT DEFAULT 'jt808'
);
CREATE INDEX IF NOT EXISTS idx_can_data_vehicle_id ON can_data(vehicle_id);

-- ----------------------------------------------------------------------------
-- 9. bd_nav_data: 北斗导航数据表
--    存储北斗导航模块上报的卫星定位数据
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS bd_nav_data (
    id TEXT PRIMARY KEY,
    vehicle_id TEXT NOT NULL,
    phone TEXT NOT NULL,
    sat_count INTEGER DEFAULT 0,
    latitude REAL DEFAULT 0,
    longitude REAL DEFAULT 0,
    altitude INTEGER DEFAULT 0,
    speed INTEGER DEFAULT 0,
    direction INTEGER DEFAULT 0,
    bd_time TEXT DEFAULT '',
    received_at DATETIME NOT NULL,
    source TEXT DEFAULT 'jt808'
);
CREATE INDEX IF NOT EXISTS idx_bd_nav_data_vehicle_id ON bd_nav_data(vehicle_id);

-- ----------------------------------------------------------------------------
-- 10. meter_data: 计价器数据表（出租车/网约车专用）
--     存储计价器读数（营运金额/空重车状态）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS meter_data (
    id TEXT PRIMARY KEY,
    vehicle_id TEXT NOT NULL,
    phone TEXT NOT NULL,
    meter_value REAL DEFAULT 0,
    received_at DATETIME NOT NULL,
    source TEXT DEFAULT 'jt808'
);
CREATE INDEX IF NOT EXISTS idx_meter_data_vehicle_id ON meter_data(vehicle_id);

-- ----------------------------------------------------------------------------
-- 11. dispatch_data: 调度信息表
--     存储下发给终端的调度指令内容（电召/包车/预约）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS dispatch_data (
    id TEXT PRIMARY KEY,
    vehicle_id TEXT NOT NULL,
    phone TEXT NOT NULL,
    content TEXT DEFAULT '',
    received_at DATETIME NOT NULL,
    source TEXT DEFAULT 'jt808'
);
CREATE INDEX IF NOT EXISTS idx_dispatch_data_vehicle_id ON dispatch_data(vehicle_id);

-- ----------------------------------------------------------------------------
-- 12. electronic_waybills: 电子路单表
--     存储终端上报的电子路单数据
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS electronic_waybills (
    id TEXT PRIMARY KEY,
    phone TEXT NOT NULL,
    waybill_no TEXT DEFAULT '',
    content TEXT DEFAULT '',
    received_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_waybills_phone ON electronic_waybills(phone);

-- ----------------------------------------------------------------------------
-- 13. command_resps: 指令响应表
--     存储终端对平台指令的响应结果
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS command_resps (
    id TEXT PRIMARY KEY,
    phone TEXT NOT NULL,
    command_id TEXT DEFAULT '',
    result INTEGER DEFAULT 0,
    received_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_command_resps_phone ON command_resps(phone);

-- ----------------------------------------------------------------------------
-- 14. terminal_props: 终端属性表
--     存储终端上报的属性信息（厂商/型号/硬件版本/固件版本/GNSS/通信模块）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS terminal_props (
    id TEXT PRIMARY KEY,
    phone TEXT NOT NULL,
    manufacturer_id TEXT DEFAULT '',
    model TEXT DEFAULT '',
    hardware_version TEXT DEFAULT '',
    firmware_version TEXT DEFAULT '',
    gnss_support INTEGER DEFAULT 0,
    comm_module INTEGER DEFAULT 0,
    received_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_terminal_props_phone ON terminal_props(phone);

-- ----------------------------------------------------------------------------
-- 15. av_params: 音视频参数表
--     存储终端音视频参数（通道/参数类型/参数值）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS av_params (
    id TEXT PRIMARY KEY,
    phone TEXT NOT NULL,
    channel_id INTEGER DEFAULT 0,
    param_type INTEGER DEFAULT 0,
    param_value TEXT DEFAULT '',
    received_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_av_params_phone ON av_params(phone);

-- ----------------------------------------------------------------------------
-- 16. info_menu_resps: 信息菜单响应表
--     存储终端对信息菜单的响应数据
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS info_menu_resps (
    id TEXT PRIMARY KEY,
    phone TEXT NOT NULL,
    info_type INTEGER DEFAULT 0,
    info_id INTEGER DEFAULT 0,
    info_data TEXT DEFAULT '',
    received_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_info_menu_resps_phone ON info_menu_resps(phone);

-- ----------------------------------------------------------------------------
-- 17. sms_forward_resps: 短信转发响应表
--     存储终端短信转发响应数据
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS sms_forward_resps (
    id TEXT PRIMARY KEY,
    phone TEXT NOT NULL,
    sms_content TEXT DEFAULT '',
    received_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sms_forward_resps_phone ON sms_forward_resps(phone);

-- ----------------------------------------------------------------------------
-- 18. event_resps: 事件响应表
--     存储终端事件响应数据
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS event_resps (
    id TEXT PRIMARY KEY,
    phone TEXT NOT NULL,
    event_id INTEGER DEFAULT 0,
    received_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_event_resps_phone ON event_resps(phone);

-- ----------------------------------------------------------------------------
-- 19. ev_data: GB/T 32960 电动汽车数据表
--     存储电动汽车上报的电池/充电/故障等数据（AUTO-FIX-2026-06-29）
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS ev_data (
    id TEXT PRIMARY KEY,
    vehicle_id TEXT NOT NULL,
    phone TEXT,
    data_type TEXT NOT NULL,
    data BLOB,
    received_at DATETIME NOT NULL,
    source TEXT
);
CREATE INDEX IF NOT EXISTS idx_ev_data_vehicle_id ON ev_data(vehicle_id);
CREATE INDEX IF NOT EXISTS idx_ev_data_received_at ON ev_data(received_at);
CREATE INDEX IF NOT EXISTS idx_ev_data_vehicle_type ON ev_data(vehicle_id, data_type);
