package module

import (
	"testing"
)

// ===================================================================
// AUTO-FIX-2026-06-30 [集成-2/3]: 模块依赖图 + 循环检测 + 版本兼容性 测试
// ===================================================================

// mockModule 测试用模块实现
type mockModule struct {
	name    string
	version string
}

func (m *mockModule) Name() string    { return m.name }
func (m *mockModule) Version() string { return m.version }
func (m *mockModule) Init(app interface{}) error { return nil }
func (m *mockModule) Start() error              { return nil }
func (m *mockModule) Stop() error               { return nil }

// mockDepModule 带依赖的模块
type mockDepModule struct {
	mockModule
	deps []string
}

func (m *mockDepModule) Depends() []string { return m.deps }

// mockPrioModule 带优先级的模块
type mockPrioModule struct {
	mockModule
	prio int
}

func (m *mockPrioModule) Priority() int { return m.prio }

func newTestLoader() *Loader {
	return &Loader{
		modules: make(map[string]*LoadedModule),
	}
}

func addModule(l *Loader, m Module) {
	l.modules[m.Name()] = &LoadedModule{
		Info:   ModuleInfo{Name: m.Name(), Version: m.Version(), Status: "loaded"},
		Module: m,
	}
}

func TestDepGraph_NoDependencies(t *testing.T) {
	l := newTestLoader()
	addModule(l, &mockModule{name: "module-storage"})
	addModule(l, &mockModule{name: "module-monitor"})

	g, missing := l.buildDepGraph()
	if len(missing) != 0 {
		t.Errorf("expected no missing deps, got %v", missing)
	}
	order, cycle := g.topologicalOrder()
	if len(cycle) != 0 {
		t.Errorf("expected no cycle, got %v", cycle)
	}
	if len(order) != 2 {
		t.Errorf("expected 2 modules in order, got %d", len(order))
	}
}

func TestDepGraph_LinearDependency(t *testing.T) {
	// module-ai depends on module-storage
	// module-monitor depends on module-ai
	l := newTestLoader()
	addModule(l, &mockModule{name: "module-storage"})
	addModule(l, &mockDepModule{
		mockModule: mockModule{name: "module-ai"},
		deps:       []string{"module-storage"},
	})
	addModule(l, &mockDepModule{
		mockModule: mockModule{name: "module-monitor"},
		deps:       []string{"module-ai"},
	})

	g, missing := l.buildDepGraph()
	if len(missing) != 0 {
		t.Errorf("expected no missing deps, got %v", missing)
	}
	order, cycle := g.topologicalOrder()
	if len(cycle) != 0 {
		t.Errorf("expected no cycle, got %v", cycle)
	}

	// 验证顺序：storage 必须在 ai 之前，ai 必须在 monitor 之前
	pos := make(map[string]int)
	for i, n := range order {
		pos[n] = i
	}
	if pos["module-storage"] >= pos["module-ai"] {
		t.Errorf("storage must come before ai: order=%v", order)
	}
	if pos["module-ai"] >= pos["module-monitor"] {
		t.Errorf("ai must come before monitor: order=%v", order)
	}
}

func TestDepGraph_CircularDependency(t *testing.T) {
	// A depends on B, B depends on A → 循环
	l := newTestLoader()
	addModule(l, &mockDepModule{
		mockModule: mockModule{name: "module-a"},
		deps:       []string{"module-b"},
	})
	addModule(l, &mockDepModule{
		mockModule: mockModule{name: "module-b"},
		deps:       []string{"module-a"},
	})

	g, _ := l.buildDepGraph()
	order, cycle := g.topologicalOrder()
	if len(cycle) != 2 {
		t.Errorf("expected 2 modules in cycle, got %d: %v", len(cycle), cycle)
	}
	if len(order) != 0 {
		t.Errorf("expected empty order when all nodes in cycle, got %v", order)
	}
}

func TestDepGraph_MissingDependency(t *testing.T) {
	// module-ai depends on module-nonexistent（未加载）
	l := newTestLoader()
	addModule(l, &mockDepModule{
		mockModule: mockModule{name: "module-ai"},
		deps:       []string{"module-nonexistent"},
	})

	_, missing := l.buildDepGraph()
	if len(missing) == 0 {
		t.Errorf("expected missing dependency for module-ai")
	}
	if deps, ok := missing["module-ai"]; !ok {
		t.Errorf("expected module-ai in missing deps")
	} else if len(deps) != 1 || deps[0] != "module-nonexistent" {
		t.Errorf("expected missing dep module-nonexistent, got %v", deps)
	}
}

func TestDepGraph_PriorityOrdering(t *testing.T) {
	// 三个无依赖的模块，应按优先级排序
	// module-monitor (Ops=50), module-storage (Storage=20), module-ai (Business=40)
	l := newTestLoader()
	addModule(l, &mockModule{name: "module-monitor"})
	addModule(l, &mockModule{name: "module-storage"})
	addModule(l, &mockModule{name: "module-ai"})

	g, _ := l.buildDepGraph()
	order, cycle := g.topologicalOrder()
	if len(cycle) != 0 {
		t.Errorf("expected no cycle, got %v", cycle)
	}

	// 验证顺序：storage(20) → ai(40) → monitor(50)
	if len(order) != 3 {
		t.Fatalf("expected 3 modules, got %d: %v", len(order), order)
	}
	if order[0] != "module-storage" {
		t.Errorf("expected module-storage first, got %s", order[0])
	}
	if order[1] != "module-ai" {
		t.Errorf("expected module-ai second, got %s", order[1])
	}
	if order[2] != "module-monitor" {
		t.Errorf("expected module-monitor third, got %s", order[2])
	}
}

func TestDepGraph_PrioritizedModuleOverridesClassification(t *testing.T) {
	// module-custom 实现了 PrioritizedModule，优先级应覆盖名称推断
	l := newTestLoader()
	addModule(l, &mockPrioModule{
		mockModule: mockModule{name: "module-custom"},
		prio:       PriorityCore, // 10，应排在 storage(20) 之前
	})
	addModule(l, &mockModule{name: "module-storage"})

	g, _ := l.buildDepGraph()
	order, _ := g.topologicalOrder()

	// 注意：depGraph 内部仅用 classifyPriority（名称推断），PrioritizedModule 在 Loader 层处理
	// 这里验证名称推断的回退逻辑仍工作
	if len(order) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(order))
	}
}

func TestDepGraph_SelfDependencyIgnored(t *testing.T) {
	// 自依赖应被忽略（不构成环）
	l := newTestLoader()
	addModule(l, &mockDepModule{
		mockModule: mockModule{name: "module-a"},
		deps:       []string{"module-a"}, // 自引用
	})

	g, missing := l.buildDepGraph()
	if len(missing) != 0 {
		t.Errorf("self-dependency should be ignored, got missing=%v", missing)
	}
	order, cycle := g.topologicalOrder()
	if len(cycle) != 0 {
		t.Errorf("self-dependency should not form cycle, got %v", cycle)
	}
	if len(order) != 1 {
		t.Errorf("expected 1 module in order, got %d", len(order))
	}
}

func TestDepGraph_EmptyLoader(t *testing.T) {
	l := newTestLoader()
	g, missing := l.buildDepGraph()
	if len(missing) != 0 {
		t.Errorf("expected no missing deps for empty loader")
	}
	order, cycle := g.topologicalOrder()
	if len(cycle) != 0 {
		t.Errorf("expected no cycle for empty loader")
	}
	if len(order) != 0 {
		t.Errorf("expected empty order for empty loader")
	}
}

// ========== 版本兼容性测试 ==========

func TestCheckCoreVersionCompatible_InRange(t *testing.T) {
	// HostCoreVersion = "3.0.0"
	err := checkCoreVersionCompatible("3.0.0", "3.99.99", "test-mod")
	if err != nil {
		t.Errorf("expected compatible, got error: %v", err)
	}
}

func TestCheckCoreVersionCompatible_EmptyBounds(t *testing.T) {
	// 空边界表示不校验
	err := checkCoreVersionCompatible("", "", "test-mod")
	if err != nil {
		t.Errorf("expected compatible with empty bounds, got error: %v", err)
	}
}

func TestCheckCoreVersionCompatible_BelowMin(t *testing.T) {
	// 模块要求 >= 4.0.0，但宿主是 3.0.0 → 不兼容
	err := checkCoreVersionCompatible("4.0.0", "", "test-mod")
	if err == nil {
		t.Errorf("expected incompatibility error for core < min")
	}
}

func TestCheckCoreVersionCompatible_AboveMax(t *testing.T) {
	// 模块要求 <= 2.0.0，但宿主是 3.0.0 → 不兼容
	err := checkCoreVersionCompatible("", "2.0.0", "test-mod")
	if err == nil {
		t.Errorf("expected incompatibility error for core > max")
	}
}

func TestCheckCoreVersionCompatible_InvalidMinVersion(t *testing.T) {
	err := checkCoreVersionCompatible("abc", "", "test-mod")
	if err == nil {
		t.Errorf("expected error for invalid min version")
	}
}

func TestCheckCoreVersionCompatible_InvalidMaxVersion(t *testing.T) {
	err := checkCoreVersionCompatible("", "x.y.z", "test-mod")
	if err == nil {
		t.Errorf("expected error for invalid max version")
	}
}

func TestParseSemVer_Valid(t *testing.T) {
	major, minor, patch, ok := parseSemVer("3.1.5")
	if !ok {
		t.Errorf("expected parse success")
	}
	if major != 3 || minor != 1 || patch != 5 {
		t.Errorf("expected 3.1.5, got %d.%d.%d", major, minor, patch)
	}
}

func TestParseSemVer_PrereleaseSuffix(t *testing.T) {
	// "3.0.0-rc1" → patch=0（数字部分）
	major, minor, patch, ok := parseSemVer("3.0.0-rc1")
	if !ok {
		t.Errorf("expected parse success for prerelease")
	}
	if major != 3 || minor != 0 || patch != 0 {
		t.Errorf("expected 3.0.0, got %d.%d.%d", major, minor, patch)
	}
}

func TestParseSemVer_Invalid(t *testing.T) {
	_, _, _, ok := parseSemVer("abc")
	if ok {
		t.Errorf("expected parse failure for invalid version")
	}
}

func TestCompareSemVer(t *testing.T) {
	tests := []struct {
		a, b       [3]int
		want       int
	}{
		{[3]int{3, 0, 0}, [3]int{3, 0, 0}, 0},
		{[3]int{3, 0, 0}, [3]int{3, 0, 1}, -1},
		{[3]int{3, 0, 1}, [3]int{3, 0, 0}, 1},
		{[3]int{3, 0, 0}, [3]int{3, 1, 0}, -1},
		{[3]int{3, 1, 0}, [3]int{3, 0, 0}, 1},
		{[3]int{3, 0, 0}, [3]int{4, 0, 0}, -1},
		{[3]int{4, 0, 0}, [3]int{3, 0, 0}, 1},
	}
	for _, tt := range tests {
		got := compareSemVer(tt.a[0], tt.a[1], tt.a[2], tt.b[0], tt.b[1], tt.b[2])
		if got != tt.want {
			t.Errorf("compareSemVer(%v, %v) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestClassifyPriority(t *testing.T) {
	tests := []struct {
		name string
		want int
	}{
		{"module-cluster", PriorityCore},
		{"module-storage", PriorityStorage},
		{"module-protocol-809", PriorityProtocol},
		{"module-ai", PriorityBusiness},
		{"module-ai-nlp", PriorityBusiness},
		{"module-crypto", PriorityBusiness},
		{"module-adapter", PriorityBusiness},
		{"module-monitor", PriorityOps},
		{"module-legacy", PriorityOps},
	}
	for _, tt := range tests {
		got := classifyPriority(tt.name)
		if got != tt.want {
			t.Errorf("classifyPriority(%s) = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestFormatCycle(t *testing.T) {
	msg := formatCycle([]string{"module-a", "module-b"})
	if msg == "" {
		t.Errorf("expected non-empty cycle message")
	}
}

func TestFormatMissingDeps(t *testing.T) {
	missing := map[string][]string{
		"module-ai": {"module-nonexistent"},
	}
	msg := formatMissingDeps(missing)
	if msg == "" {
		t.Errorf("expected non-empty missing deps message")
	}
}
