package gateway

import (
	"testing"
	"time"

	"github.com/jte-engine/jte/internal/config"
	"github.com/jte-engine/jte/pkg/storage"
	"go.uber.org/zap"
)

// AUTO-FIX-2026-06-29 [P1-6]: 809 转发规则过滤逻辑单元测试
// AUTO-FIX-2026-07-02: 新增 SourcePlatformID + Video DataType 测试
// 覆盖 matchForwardRule 和 shouldForward 的全部过滤维度：
//   - 数据类型匹配 (location/alarm/video)
//   - 车辆手机号匹配
//   - 源平台 ID 匹配（平台间定向转发）
//   - 报警类型匹配（逗号分隔多类型）
//   - 报警最低级别过滤
//   - 每日生效时间窗口（同日 + 跨日）
//   - 规则启用/禁用
//   - YAML 静态规则回退
//   - atomic 热更新并发安全

func newTestClientWithRules(t *testing.T, rules []*storage.ForwardRule, yamlRules config.ForwardRules) *JT809Client {
	t.Helper()
	c := &JT809Client{
		cfg:          &config.JT809PlatformConfig{ID: "test-platform"},
		logger:       zap.NewNop(),
		forwardRules: yamlRules,
	}
	if rules == nil {
		empty := make([]*storage.ForwardRule, 0)
		c.rulesSnapshot.Store(&empty)
	} else {
		// 深拷贝避免测试间共享指针
		cp := make([]*storage.ForwardRule, len(rules))
		for i, r := range rules {
			rc := *r
			cp[i] = &rc
		}
		c.rulesSnapshot.Store(&cp)
	}
	return c
}

func TestMatchForwardRule_DisabledRule(t *testing.T) {
	r := &storage.ForwardRule{Enabled: false, DataType: "location"}
	if matchForwardRule(r, "location", "13800000000", "", nil) {
		t.Fatal("disabled rule should not match")
	}
}

func TestMatchForwardRule_DataTypeMismatch(t *testing.T) {
	r := &storage.ForwardRule{Enabled: true, DataType: "alarm"}
	if matchForwardRule(r, "location", "13800000000", "", nil) {
		t.Fatal("rule with data_type=alarm should not match location data")
	}
}

func TestMatchForwardRule_PhoneMismatch(t *testing.T) {
	r := &storage.ForwardRule{Enabled: true, DataType: "location", Phone: "13800000000"}
	if matchForwardRule(r, "location", "13900000000", "", nil) {
		t.Fatal("rule with phone filter should not match different phone")
	}
	if !matchForwardRule(r, "location", "13800000000", "", nil) {
		t.Fatal("rule with matching phone should match")
	}
}

func TestMatchForwardRule_EmptyPhoneMatchesAll(t *testing.T) {
	r := &storage.ForwardRule{Enabled: true, DataType: "location", Phone: ""}
	if !matchForwardRule(r, "location", "13800000000", "", nil) {
		t.Fatal("empty phone should match any phone")
	}
}

func TestMatchForwardRule_AlarmTypeMatch(t *testing.T) {
	r := &storage.ForwardRule{
		Enabled:    true,
		DataType:   "alarm",
		AlarmTypes: "overspeed,emergency",
	}
	alarm := &storage.AlarmData{Type: "overspeed"}
	if !matchForwardRule(r, "alarm", "13800000000", "", alarm) {
		t.Fatal("alarm type overspeed should match rule")
	}
	alarm.Type = "emergency"
	if !matchForwardRule(r, "alarm", "13800000000", "", alarm) {
		t.Fatal("alarm type emergency should match rule")
	}
	alarm.Type = "fatigue"
	if matchForwardRule(r, "alarm", "13800000000", "", alarm) {
		t.Fatal("alarm type fatigue should not match rule with overspeed,emergency")
	}
}

func TestMatchForwardRule_AlarmTypeEmptyMatchesAll(t *testing.T) {
	r := &storage.ForwardRule{
		Enabled:    true,
		DataType:   "alarm",
		AlarmTypes: "", // 空=全部类型
	}
	alarm := &storage.AlarmData{Type: "any_type"}
	if !matchForwardRule(r, "alarm", "13800000000", "", alarm) {
		t.Fatal("empty AlarmTypes should match any alarm type")
	}
}

func TestMatchForwardRule_AlarmMinLevel(t *testing.T) {
	r := &storage.ForwardRule{
		Enabled:  true,
		DataType: "alarm",
		MinLevel: 2, // 最低级别 2，仅转发严重(2)/紧急(3)
	}
	// 一般报警(1) 不转发
	if matchForwardRule(r, "alarm", "13800000000", "", &storage.AlarmData{Type: "t", Level: 1}) {
		t.Fatal("level=1 should not match MinLevel=2")
	}
	// 严重报警(2) 转发
	if !matchForwardRule(r, "alarm", "13800000000", "", &storage.AlarmData{Type: "t", Level: 2}) {
		t.Fatal("level=2 should match MinLevel=2")
	}
	// 紧急报警(3) 转发
	if !matchForwardRule(r, "alarm", "13800000000", "", &storage.AlarmData{Type: "t", Level: 3}) {
		t.Fatal("level=3 should match MinLevel=2")
	}
}

func TestMatchForwardRule_TimeWindowSameDay(t *testing.T) {
	now := time.Now()
	nowStr := now.Format("15:04:05")
	// 构造包含当前时间的窗口：[当前-1h, 当前+1h]
	start := now.Add(-time.Hour).Format("15:04:05")
	end := now.Add(time.Hour).Format("15:04:05")
	r := &storage.ForwardRule{
		Enabled:   true,
		DataType:  "location",
		TimeStart: start,
		TimeEnd:   end,
	}
	if !matchForwardRule(r, "location", "13800000000", "", nil) {
		t.Fatalf("current time %s should be within [%s, %s]", nowStr, start, end)
	}
}

func TestMatchForwardRule_TimeWindowOutside(t *testing.T) {
	now := time.Now()
	// 构造不包含当前时间的窗口：[当前+1h, 当前+2h]
	start := now.Add(time.Hour).Format("15:04:05")
	end := now.Add(2 * time.Hour).Format("15:04:05")
	r := &storage.ForwardRule{
		Enabled:   true,
		DataType:  "location",
		TimeStart: start,
		TimeEnd:   end,
	}
	if matchForwardRule(r, "location", "13800000000", "", nil) {
		t.Fatal("current time should be outside future window")
	}
}

func TestMatchForwardRule_TimeWindowCrossDay(t *testing.T) {
	// 跨日窗口：22:00:00 - 06:00:00（夜班时段）
	r := &storage.ForwardRule{
		Enabled:   true,
		DataType:  "location",
		TimeStart: "22:00:00",
		TimeEnd:   "06:00:00",
	}
	now := time.Now()
	nowStr := now.Format("15:04:05")
	// 当前时间若在夜班时段（>=22:00 或 <=06:00）应匹配，否则不匹配
	expectMatch := nowStr >= "22:00:00" || nowStr <= "06:00:00"
	got := matchForwardRule(r, "location", "13800000000", "", nil)
	if got != expectMatch {
		t.Fatalf("cross-day window: now=%s expect=%v got=%v", nowStr, expectMatch, got)
	}
}

// AUTO-FIX-2026-07-02: SourcePlatformID 过滤测试
func TestMatchForwardRule_SourcePlatformMatch(t *testing.T) {
	r := &storage.ForwardRule{
		Enabled:          true,
		DataType:         "location",
		SourcePlatformID: "platform_A",
	}
	// 源平台匹配
	if !matchForwardRule(r, "location", "13800000000", "platform_A", nil) {
		t.Fatal("source platform A should match rule with SourcePlatformID=platform_A")
	}
	// 源平台不匹配
	if matchForwardRule(r, "location", "13800000000", "platform_B", nil) {
		t.Fatal("source platform B should not match rule with SourcePlatformID=platform_A")
	}
	// 空 SourcePlatformID 规则匹配所有源平台
	r.SourcePlatformID = ""
	if !matchForwardRule(r, "location", "13800000000", "any_platform", nil) {
		t.Fatal("empty SourcePlatformID should match any source platform")
	}
}

// AUTO-FIX-2026-07-02: Video DataType 匹配测试
func TestMatchForwardRule_VideoDataType(t *testing.T) {
	r := &storage.ForwardRule{
		Enabled:  true,
		DataType: "video",
		Phone:    "13800000000",
	}
	// video 类型 + 匹配 phone → 转发
	if !matchForwardRule(r, "video", "13800000000", "", nil) {
		t.Fatal("video data with matching phone should match")
	}
	// video 类型 + 不匹配 phone → 不转发
	if matchForwardRule(r, "video", "13900000000", "", nil) {
		t.Fatal("video data with non-matching phone should not match")
	}
	// 非 video 类型不匹配 video 规则
	if matchForwardRule(r, "location", "13800000000", "", nil) {
		t.Fatal("location data should not match video rule")
	}
}

func TestShouldForward_PersistentRulesPriority(t *testing.T) {
	// 持久化规则存在但无匹配时，应不转发（不回退到 YAML）
	rules := []*storage.ForwardRule{
		{Enabled: true, DataType: "alarm", Phone: "13800000000", AlarmTypes: "overspeed"},
	}
	yamlRules := config.ForwardRules{ForwardLocation: true, ForwardAlarm: true}
	c := newTestClientWithRules(t, rules, yamlRules)
	// location 数据无匹配规则，应不转发（即使 YAML ForwardLocation=true）
	if c.shouldForward("location", "13900000000", "", nil) {
		t.Fatal("should not forward location when persistent rules exist but no match")
	}
	// 报警类型不匹配也不应转发
	if c.shouldForward("alarm", "13800000000", "", &storage.AlarmData{Type: "fatigue"}) {
		t.Fatal("should not forward alarm with non-matching type when persistent rules exist")
	}
	// 匹配的报警应转发
	if !c.shouldForward("alarm", "13800000000", "", &storage.AlarmData{Type: "overspeed"}) {
		t.Fatal("should forward alarm matching persistent rule")
	}
}

func TestShouldForward_YAMLFallback(t *testing.T) {
	// 无持久化规则时回退到 YAML 静态规则
	yamlRules := config.ForwardRules{
		ForwardLocation: true,
		ForwardAlarm:    false,
		ForwardPhones:   []string{"13800000000"},
	}
	c := newTestClientWithRules(t, nil, yamlRules)
	// YAML ForwardLocation=true 且 phone 匹配 → 转发
	if !c.shouldForward("location", "13800000000", "", nil) {
		t.Fatal("YAML fallback: location should forward")
	}
	// YAML ForwardLocation=true 但 phone 不匹配 → 不转发
	if c.shouldForward("location", "13900000000", "", nil) {
		t.Fatal("YAML fallback: location with non-whitelist phone should not forward")
	}
	// YAML ForwardAlarm=false → 不转发
	if c.shouldForward("alarm", "13800000000", "", &storage.AlarmData{Type: "t"}) {
		t.Fatal("YAML fallback: alarm should not forward when ForwardAlarm=false")
	}
}

func TestShouldForward_YAMLFallbackNoPhoneFilter(t *testing.T) {
	// YAML ForwardPhones 为空时全部转发
	yamlRules := config.ForwardRules{
		ForwardLocation: true,
		ForwardAlarm:    true,
		ForwardPhones:   nil,
	}
	c := newTestClientWithRules(t, nil, yamlRules)
	if !c.shouldForward("location", "any_phone", "", nil) {
		t.Fatal("YAML fallback without phone filter should forward all")
	}
}

// AUTO-FIX-2026-07-02: Video YAML 回退测试
func TestShouldForward_VideoYAMLFallback(t *testing.T) {
	yamlRules := config.ForwardRules{
		ForwardVideo:  true,
		ForwardPhones: nil,
	}
	c := newTestClientWithRules(t, nil, yamlRules)
	if !c.shouldForward("video", "13800000000", "", nil) {
		t.Fatal("YAML fallback: video should forward when ForwardVideo=true")
	}

	yamlRules.ForwardVideo = false
	c = newTestClientWithRules(t, nil, yamlRules)
	if c.shouldForward("video", "13800000000", "", nil) {
		t.Fatal("YAML fallback: video should not forward when ForwardVideo=false")
	}
}

func TestReloadForwardRules_AtomicUpdate(t *testing.T) {
	// 验证 ReloadForwardRules 原子替换快照，旧快照指针不变（无锁读取安全）
	c := newTestClientWithRules(t, nil, config.ForwardRules{})
	oldPtr := c.rulesSnapshot.Load()
	// ReloadForwardRules 在 store 为 nil 时设置空切片
	c.ReloadForwardRules()
	newPtr := c.rulesSnapshot.Load()
	if oldPtr == newPtr {
		t.Fatal("ReloadForwardRules should replace snapshot pointer")
	}
}

func TestReloadForwardRules_ConcurrentSafe(t *testing.T) {
	// 并发 ReloadForwardRules + shouldForward 不应 panic
	c := newTestClientWithRules(t, nil, config.ForwardRules{ForwardLocation: true})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			c.ReloadForwardRules()
		}
	}()
	for i := 0; i < 100; i++ {
		c.shouldForward("location", "13800000000", "", nil)
	}
	<-done
}
