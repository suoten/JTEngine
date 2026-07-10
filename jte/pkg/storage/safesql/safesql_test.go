package safesql

import "testing"

func TestValidateOrderBy(t *testing.T) {
	cases := []struct {
		input, defaultOrder, expected string
	}{
		// 空值返回默认
		{"", "created_at DESC", "created_at DESC"},
		// 白名单列名
		{"created_at", "id", "`created_at` ASC"},
		{"created_at DESC", "id", "`created_at` DESC"},
		{"created_at ASC", "id", "`created_at` ASC"},
		// 大小写不敏感
		{"CREATED_AT desc", "id", "`created_at` DESC"},
		// 带表前缀
		{"vehicles.created_at", "id", "`created_at` ASC"},
		// 带反引号
		{"`created_at` DESC", "id", "`created_at` DESC"},
		// 不在白名单 → 返回默认
		{"password", "id", "id"},
		{"DROP TABLE users", "id", "id"},
		{"1=1", "id", "id"},
		{"created_at; DROP TABLE", "id", "id"}, // 分号导致列名不在白名单，返回默认
		// 无效方向 → 默认 ASC
		{"created_at XYZ", "id", "`created_at` ASC"},
	}
	for _, c := range cases {
		got := ValidateOrderBy(c.input, c.defaultOrder)
		if got != c.expected {
			t.Errorf("ValidateOrderBy(%q, %q) = %q, want %q", c.input, c.defaultOrder, got, c.expected)
		}
	}
}

func TestValidateOrderByMulti(t *testing.T) {
	got := ValidateOrderByMulti("created_at DESC, name ASC", "id")
	if got != "`created_at` DESC, `name` ASC" {
		t.Errorf("multi order got %q", got)
	}
	// 含非法列 → 过滤掉非法部分
	got = ValidateOrderByMulti("created_at DESC, password, name", "id")
	if got != "`created_at` DESC, `name` ASC" {
		t.Errorf("multi with invalid got %q", got)
	}
	// 全部非法 → 默认
	got = ValidateOrderByMulti("password, evil", "id")
	if got != "id" {
		t.Errorf("all invalid got %q", got)
	}
}

func TestSanitizeLikeValue(t *testing.T) {
	cases := []struct {
		input, expected string
	}{
		{"normal", "normal"},
		{"100%", "100\\%"},
		{"a_b", "a\\_b"},
		{"path\\to", "path\\\\to"},
		{"%_\\", "\\%\\_\\\\"},
	}
	for _, c := range cases {
		got := SanitizeLikeValue(c.input)
		if got != c.expected {
			t.Errorf("SanitizeLikeValue(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestSQLInjectionAttempts(t *testing.T) {
	// 常见 SQL 注入 payload 应被拒绝
	injections := []string{
		"' OR '1'='1",
		"'; DROP TABLE users; --",
		"1; EXEC xp_cmdshell('dir')",
		"UNION SELECT * FROM passwords",
		"(SELECT 1)",
		"CASE WHEN 1=1 THEN id END",
	}
	for _, inj := range injections {
		got := ValidateOrderBy(inj, "created_at DESC")
		if got != "created_at DESC" {
			t.Errorf("SQL injection %q should be rejected, got %q", inj, got)
		}
	}
}
