-- ============================================================================
-- 回滚脚本：002_core_tables
-- 回滚 002_core_tables.sql 的变更
--
-- 警告：执行前请确认所有表中的数据已备份。
--       DROP TABLE 会永久删除表中所有数据。
--       回滚顺序：先删依赖表，再删被依赖表（本组表无外键依赖，顺序无要求）
-- ============================================================================

DROP TABLE IF EXISTS ev_data;
DROP TABLE IF EXISTS event_resps;
DROP TABLE IF EXISTS sms_forward_resps;
DROP TABLE IF EXISTS info_menu_resps;
DROP TABLE IF EXISTS av_params;
DROP TABLE IF EXISTS terminal_props;
DROP TABLE IF EXISTS command_resps;
DROP TABLE IF EXISTS electronic_waybills;
DROP TABLE IF EXISTS dispatch_data;
DROP TABLE IF EXISTS meter_data;
DROP TABLE IF EXISTS bd_nav_data;
DROP TABLE IF EXISTS can_data;
DROP TABLE IF EXISTS multimedia;
DROP TABLE IF EXISTS driver_info;
DROP TABLE IF EXISTS protocol_logs;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS alarms;
DROP TABLE IF EXISTS locations;
DROP TABLE IF EXISTS vehicles;
