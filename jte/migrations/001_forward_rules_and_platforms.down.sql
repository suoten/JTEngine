-- ============================================================================
-- 回滚脚本：001_forward_rules_and_platforms
-- 回滚 001_forward_rules_and_platforms.sql 的变更
--
-- 警告：执行前请确认 forward_rules 和 platforms 表中的数据已备份。
--       DROP TABLE 会永久删除表中所有数据。
-- ============================================================================

DROP TABLE IF EXISTS forward_rules;
DROP TABLE IF EXISTS platforms;
