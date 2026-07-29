package middleware

import (
	"testing"
)

// AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: 文件上传安全中间件单元测试
// 覆盖：MIME 类型检测、魔数校验、文本文件判定、扩展名一致性校验
// 这些纯函数是文件上传安全的核心防线，之前零测试覆盖。

func TestDetectMimeType(t *testing.T) {
	tests := []struct {
		name string
		head []byte
		want string
	}{
		{"JPEG", []byte{0xFF, 0xD8, 0xFF, 0xE0}, "image/jpeg"},
		{"PNG", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, "image/png"},
		{"GIF", []byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61}, "image/gif"},
		{"BMP", []byte{0x42, 0x4D}, "image/bmp"},
		{"PDF", []byte("%PDF-1.4"), "application/pdf"},
		{"ZIP/DOCX", []byte{0x50, 0x4B, 0x03, 0x04}, "application/zip"},
		{"OLE/DOC", []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, "application/msword"},
		{"WEBP", []byte("RIFF\x00\x00\x00\x00WEBP"), "image/webp"},
		{"empty", []byte{}, "application/octet-stream"},
		{"unknown binary", []byte{0x00, 0x01, 0x02}, "application/octet-stream"},
		{"plain text", []byte("hello world"), "text/plain"},
		{"JSON text", []byte(`{"key":"value"}`), "text/plain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectMimeType(tt.head)
			if got != tt.want {
				t.Errorf("detectMimeType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateMagicNumber(t *testing.T) {
	tests := []struct {
		name string
		ext  string
		head []byte
		want bool
	}{
		// 正确匹配
		{"jpg correct", "jpg", []byte{0xFF, 0xD8, 0xFF}, true},
		{"jpeg correct", "jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0}, true},
		{"png correct", "png", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, true},
		{"gif correct", "gif", []byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61}, true},
		{"bmp correct", "bmp", []byte{0x42, 0x4D}, true},
		{"pdf correct", "pdf", []byte("%PDF-1.4"), true},
		{"docx correct", "docx", []byte{0x50, 0x4B, 0x03, 0x04}, true},
		{"xlsx correct", "xlsx", []byte{0x50, 0x4B, 0x03, 0x04}, true},
		{"doc correct", "doc", []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, true},
		{"xls correct", "xls", []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}, true},
		{"txt correct", "txt", []byte("hello"), true},
		{"csv correct", "csv", []byte("a,b,c\n1,2,3"), true},
		{"webp correct", "webp", []byte("RIFF"), true},

		// 伪造扩展名（魔数不匹配）
		{"jpg fake (actually PNG)", "jpg", []byte{0x89, 0x50, 0x4E, 0x47}, false},
		{"png fake (actually JPEG)", "png", []byte{0xFF, 0xD8, 0xFF}, false},
		{"pdf fake (actually script)", "pdf", []byte("<script>"), false},
		{"jpg fake (actually EXE)", "jpg", []byte("MZ"), false},

		// 边界情况
		{"empty head", "jpg", []byte{}, false},
		{"short head jpg", "jpg", []byte{0xFF, 0xD8}, false},
		{"unknown ext passes", "xyz", []byte{0x00, 0x01}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validateMagicNumber(tt.ext, tt.head); got != tt.want {
				t.Errorf("validateMagicNumber(%q, ...) = %v, want %v", tt.ext, got, tt.want)
			}
		})
	}
}

func TestIsText(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"empty", []byte{}, false},
		{"plain ASCII", []byte("hello world"), true},
		{"UTF-8 Chinese", []byte("你好世界"), true},
		{"JSON", []byte(`{"name":"test"}`), true},
		{"XML", []byte("<root>data</root>"), true},
		{"binary with null", []byte("hello\x00world"), false},
		{"mostly binary", []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A}, false},
		{"pure binary", []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x0B, 0x0C}, false},
		{"with tab and newline", []byte("col1\tcol2\nrow2"), true},
		{"single byte", []byte("A"), true},
		{"single null byte", []byte{0x00}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isText(tt.data); got != tt.want {
				t.Errorf("isText(%v) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}

func TestDefaultUploadConfig(t *testing.T) {
	cfg := DefaultUploadConfig()
	if cfg.MaxFileSize != 10*1024*1024 {
		t.Errorf("MaxFileSize = %d, want %d", cfg.MaxFileSize, 10*1024*1024)
	}
	if len(cfg.AllowedExtensions) == 0 {
		t.Error("AllowedExtensions is empty")
	}
	if len(cfg.AllowedMimeTypes) == 0 {
		t.Error("AllowedMimeTypes is empty")
	}

	// 验证关键扩展名在白名单中
	extSet := make(map[string]bool, len(cfg.AllowedExtensions))
	for _, ext := range cfg.AllowedExtensions {
		extSet[ext] = true
	}
	for _, required := range []string{"jpg", "png", "pdf", "txt"} {
		if !extSet[required] {
			t.Errorf("required extension %q not in AllowedExtensions", required)
		}
	}
}

func TestImageUploadConfig(t *testing.T) {
	cfg := ImageUploadConfig()
	if cfg.MaxFileSize != 5*1024*1024 {
		t.Errorf("MaxFileSize = %d, want %d", cfg.MaxFileSize, 5*1024*1024)
	}
	// 图片配置不应包含文档类型
	for _, ext := range cfg.AllowedExtensions {
		if ext == "pdf" || ext == "doc" || ext == "txt" {
			t.Errorf("image config should not allow %q", ext)
		}
	}
}
