package module

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"go.uber.org/zap"
)

// ===================================================================
// AUTO-FIX-2026-06-30 [集成-5]: 模块加载模式 + 进程模块测试
// ===================================================================

func TestParseLoadMode(t *testing.T) {
	tests := []struct {
		input string
		want  ModuleLoadMode
	}{
		{"plugin", LoadModePlugin},
		{"so", LoadModePlugin},
		{"process", LoadModeProcess},
		{"rpc", LoadModeProcess},
		{"grpc", LoadModeProcess},
		{"auto", LoadModeAuto},
		{"", LoadModeAuto},
		{"unknown", LoadModeAuto},
		{"PLUGIN", LoadModePlugin},
		{" Process ", LoadModeProcess},
	}
	for _, tt := range tests {
		got := ParseLoadMode(tt.input)
		if got != tt.want {
			t.Errorf("ParseLoadMode(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestModuleLoadModeString(t *testing.T) {
	tests := []struct {
		mode ModuleLoadMode
		want string
	}{
		{LoadModeAuto, "auto"},
		{LoadModePlugin, "plugin"},
		{LoadModeProcess, "process"},
	}
	for _, tt := range tests {
		if got := tt.mode.String(); got != tt.want {
			t.Errorf("ModuleLoadMode(%d).String() = %q, want %q", tt.mode, got, tt.want)
		}
	}
}

func TestSelectLoadMode_ExplicitConfig(t *testing.T) {
	// 显式配置应覆盖自动选择
	if got := SelectLoadMode(LoadModePlugin); got != LoadModePlugin {
		t.Errorf("explicit plugin → got %v", got)
	}
	if got := SelectLoadMode(LoadModeProcess); got != LoadModeProcess {
		t.Errorf("explicit process → got %v", got)
	}
}

func TestSelectLoadMode_AutoNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("test only for non-linux platforms")
	}
	// 非 Linux 平台强制进程模式
	if got := SelectLoadMode(LoadModeAuto); got != LoadModeProcess {
		t.Errorf("non-linux auto → got %v, want process", got)
	}
}

func TestSelectLoadMode_AutoLinuxProd(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("test only for linux")
	}
	os.Setenv("JTE_ENV", "production")
	defer os.Unsetenv("JTE_ENV")
	if got := SelectLoadMode(LoadModeAuto); got != LoadModeProcess {
		t.Errorf("linux prod auto → got %v, want process", got)
	}
}

func TestSelectLoadMode_AutoLinuxDev(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("test only for linux")
	}
	os.Unsetenv("JTE_ENV")
	os.Unsetenv("JTE_MODULE_PROCESS")
	if got := SelectLoadMode(LoadModeAuto); got != LoadModePlugin {
		t.Errorf("linux dev auto → got %v, want plugin", got)
	}
}

func TestSelectLoadMode_AutoLinuxDevForceProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("test only for linux")
	}
	os.Unsetenv("JTE_ENV")
	os.Setenv("JTE_MODULE_PROCESS", "1")
	defer os.Unsetenv("JTE_MODULE_PROCESS")
	if got := SelectLoadMode(LoadModeAuto); got != LoadModeProcess {
		t.Errorf("linux dev force process → got %v, want process", got)
	}
}

func TestIsPluginSupported(t *testing.T) {
	got := IsPluginSupported()
	want := runtime.GOOS == "linux"
	if got != want {
		t.Errorf("IsPluginSupported() = %v, want %v", got, want)
	}
}

func TestDefaultLoadModeConfig(t *testing.T) {
	cfg := DefaultLoadModeConfig()
	if cfg.Mode != LoadModeAuto {
		t.Errorf("expected default mode=auto, got %v", cfg.Mode)
	}
	if cfg.ModuleBinDir == "" {
		t.Errorf("expected non-empty bin dir")
	}
	if cfg.StartTimeoutSec <= 0 {
		t.Errorf("expected positive start timeout")
	}
}

func TestDiscoverModuleBinaries_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	modules, err := discoverModuleBinaries(tmpDir)
	if err != nil {
		t.Errorf("expected no error for empty dir, got %v", err)
	}
	if len(modules) != 0 {
		t.Errorf("expected no modules, got %d", len(modules))
	}
}

func TestDiscoverModuleBinaries_NonexistentDir(t *testing.T) {
	modules, err := discoverModuleBinaries("/nonexistent/path/12345")
	if err != nil {
		t.Errorf("expected no error for nonexistent dir, got %v", err)
	}
	if modules != nil {
		t.Errorf("expected nil modules for nonexistent dir")
	}
}

func TestDiscoverModuleBinaries_FindsExecutables(t *testing.T) {
	tmpDir := t.TempDir()

	// 创建模拟的可执行文件
	if runtime.GOOS == "windows" {
		createFile(t, filepath.Join(tmpDir, "module-storage.exe"))
		createFile(t, filepath.Join(tmpDir, "module-ai.exe"))
		// 非可执行文件应被忽略
		createFile(t, filepath.Join(tmpDir, "readme.txt"))
	} else {
		createExecutable(t, filepath.Join(tmpDir, "module-storage"))
		createExecutable(t, filepath.Join(tmpDir, "module-ai"))
		// 非可执行文件应被忽略
		createFile(t, filepath.Join(tmpDir, "readme.txt"))
		// .so 文件应被忽略（plugin 模式用）
		createFile(t, filepath.Join(tmpDir, "module-crypto.so"))
	}

	modules, err := discoverModuleBinaries(tmpDir)
	if err != nil {
		t.Fatalf("discoverModuleBinaries failed: %v", err)
	}

	if len(modules) != 2 {
		t.Fatalf("expected 2 modules, got %d: %v", len(modules), modules)
	}
	if _, ok := modules["module-storage"]; !ok {
		t.Errorf("expected module-storage in results: %v", modules)
	}
	if _, ok := modules["module-ai"]; !ok {
		t.Errorf("expected module-ai in results: %v", modules)
	}
}

func TestProcessModule_NameVersion(t *testing.T) {
	pm := NewProcessModule("test-mod", "/path/to/binary", DefaultLoadModeConfig(), zap.NewNop())
	if pm.Name() != "test-mod" {
		t.Errorf("expected name=test-mod, got %s", pm.Name())
	}
	if pm.Version() != "(process mode)" {
		t.Errorf("expected version=(process mode), got %s", pm.Version())
	}
}

func TestProcessModule_StopWithoutInit(t *testing.T) {
	// Stop 在未 Init 时应安全返回 nil
	pm := NewProcessModule("test-mod", "/path/to/binary", DefaultLoadModeConfig(), zap.NewNop())
	if err := pm.Stop(); err != nil {
		t.Errorf("Stop without Init should return nil, got %v", err)
	}
}

func TestProcessModule_HealthWithoutInit(t *testing.T) {
	pm := NewProcessModule("test-mod", "/path/to/binary", DefaultLoadModeConfig(), zap.NewNop())
	if err := pm.Health(); err == nil {
		t.Errorf("Health without Init should return error")
	}
}

func TestLoader_SetLoadMode(t *testing.T) {
	l := &Loader{
		modules: make(map[string]*LoadedModule),
		logger:  zap.NewNop(),
	}
	cfg := DefaultLoadModeConfig()
	l.SetLoadMode(LoadModeProcess, cfg)

	l.mu.RLock()
	mode := l.loadMode
	modeCfg := l.modeCfg
	l.mu.RUnlock()

	if mode != LoadModeProcess {
		t.Errorf("expected mode=process, got %v", mode)
	}
	if modeCfg.ModuleBinDir != cfg.ModuleBinDir {
		t.Errorf("config not stored correctly")
	}
}

func TestLoader_LoadAll_ProcessModeNoBinaries(t *testing.T) {
	tmpDir := t.TempDir()
	l := &Loader{
		modules:  make(map[string]*LoadedModule),
		logger:   zap.NewNop(),
		dir:      tmpDir,
		loadMode: LoadModeProcess,
		modeCfg: LoadModeConfig{
			Mode:         LoadModeProcess,
			ModuleBinDir: filepath.Join(tmpDir, "bin"),
			SocketDir:    tmpDir,
		},
	}
	// 无二进制目录 → 应返回 nil（不报错）
	if err := l.LoadAll(); err != nil {
		t.Errorf("LoadAll with no binaries should succeed, got %v", err)
	}
	if len(l.modules) != 0 {
		t.Errorf("expected 0 modules, got %d", len(l.modules))
	}
}

// ========== 辅助函数 ==========

func createFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("fake"), 0o644); err != nil {
		t.Fatalf("create file %s: %v", path, err)
	}
}

func createExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("create executable %s: %v", path, err)
	}
}
