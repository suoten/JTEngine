// Package middleware - 文件上传安全中间件
//
// 等保2.0 三级 + 代码安全要求：
//   - 文件类型白名单（扩展名 + 魔数双重校验）
//   - 文件大小限制
//   - 病毒扫描钩子（对接 ClamAV 或第三方扫描服务）
package middleware

import (
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// UploadSecurityConfig 文件上传安全配置
type UploadSecurityConfig struct {
	// MaxFileSize 单文件最大大小（字节），默认 10MB
	MaxFileSize int64
	// AllowedExtensions 允许的扩展名白名单（小写，不含点），如 ["jpg","png","pdf"]
	AllowedExtensions []string
	// AllowedMimeTypes 允许的 MIME 类型白名单，如 ["image/jpeg","image/png"]
	AllowedMimeTypes []string
	// VirusScanEnabled 是否启用病毒扫描（需外部扫描服务）
	VirusScanEnabled bool
	// VirusScanner 病毒扫描回调（返回 true 表示文件安全）
	// 实际实现对接 ClamAV / 第三方 API
	VirusScanner func(filename string, data []byte) error
}

// DefaultUploadConfig 默认上传安全配置（图片 + 文档）
func DefaultUploadConfig() *UploadSecurityConfig {
	return &UploadSecurityConfig{
		MaxFileSize:   10 * 1024 * 1024, // 10MB
		AllowedExtensions: []string{"jpg", "jpeg", "png", "gif", "bmp", "pdf", "doc", "docx", "xls", "xlsx", "txt", "csv"},
		AllowedMimeTypes: []string{
			"image/jpeg", "image/png", "image/gif", "image/bmp",
			"application/pdf", "application/msword",
			"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			"application/vnd.ms-excel",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			"text/plain", "text/csv",
		},
		VirusScanEnabled: false,
	}
}

// ImageUploadConfig 图片上传安全配置（仅图片）
func ImageUploadConfig() *UploadSecurityConfig {
	return &UploadSecurityConfig{
		MaxFileSize:       5 * 1024 * 1024, // 5MB
		AllowedExtensions: []string{"jpg", "jpeg", "png", "gif", "bmp", "webp"},
		AllowedMimeTypes: []string{
			"image/jpeg", "image/png", "image/gif", "image/bmp", "image/webp",
		},
		VirusScanEnabled: false,
	}
}

// FileUploadSecurity 文件上传安全中间件
// 对 multipart/form-data 上传的文件执行：
//  1. 文件大小限制
//  2. 扩展名白名单校验
//  3. MIME 类型白名单校验
//  4. 魔数（Magic Number）校验（防止伪造扩展名）
//  5. 病毒扫描（如配置）
func FileUploadSecurity(cfg *UploadSecurityConfig) gin.HandlerFunc {
	if cfg == nil {
		cfg = DefaultUploadConfig()
	}
	if cfg.MaxFileSize <= 0 {
		cfg.MaxFileSize = 10 * 1024 * 1024
	}

	// 构建扩展名集合（O(1) 查找）
	allowedExtSet := make(map[string]bool, len(cfg.AllowedExtensions))
	for _, ext := range cfg.AllowedExtensions {
		allowedExtSet[strings.ToLower(ext)] = true
	}
	allowedMimeSet := make(map[string]bool, len(cfg.AllowedMimeTypes))
	for _, mime := range cfg.AllowedMimeTypes {
		allowedMimeSet[strings.ToLower(mime)] = true
	}

	return func(c *gin.Context) {
		// 仅检查 multipart 请求
		contentType := c.GetHeader("Content-Type")
		if !strings.HasPrefix(contentType, "multipart/form-data") {
			c.Next()
			return
		}

		// 限制请求体大小（防止大文件 DoS）
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, cfg.MaxFileSize)

		// 解析 multipart 表单
		if err := c.Request.ParseMultipartForm(cfg.MaxFileSize); err != nil {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{
				"code":    413,
				"message": fmt.Sprintf("文件上传失败：%s（最大允许 %d 字节）", err.Error(), cfg.MaxFileSize),
			})
			c.Abort()
			return
		}

		// 校验每个上传的文件
		if c.Request.MultipartForm != nil {
			for _, files := range c.Request.MultipartForm.File {
				for _, fileHeader := range files {
					// 1. 扩展名白名单校验
					ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fileHeader.Filename), "."))
					if ext == "" || !allowedExtSet[ext] {
						c.JSON(http.StatusBadRequest, gin.H{
							"code":    400,
							"message": fmt.Sprintf("不允许的文件类型：%s（允许：%s）", ext, strings.Join(cfg.AllowedExtensions, ", ")),
						})
						c.Abort()
						return
					}

					// 2. 文件大小校验
					if fileHeader.Size > cfg.MaxFileSize {
						c.JSON(http.StatusRequestEntityTooLarge, gin.H{
							"code":    413,
							"message": fmt.Sprintf("文件 %s 超过大小限制（%d 字节）", fileHeader.Filename, cfg.MaxFileSize),
						})
						c.Abort()
						return
					}

					// 3. MIME 类型 + 魔数校验
					file, err := fileHeader.Open()
					if err != nil {
						c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "无法读取上传文件"})
						c.Abort()
						return
					}
					defer file.Close()

					// 读取前 512 字节用于魔数检测
					head := make([]byte, 512)
					n, _ := file.Read(head)
					head = head[:n]

					// MIME 检测（基于内容而非客户端声明）
					detectedMime := detectMimeType(head)
					// INDUSTRIAL-FIX-2026-07-25 [P2-R32]: ZIP-based Office 格式（docx/xlsx）
					// 的魔数为 PK\x03\x04，detectMimeType 只能返回 "application/zip"，
					// 无法区分具体 Office 类型。此处对 docx/xlsx 扩展名放行 "application/zip"，
					// 由后续 validateMagicNumber 做内容级校验，避免合法 DOCX/XLSX 被误拒。
					mimeAccepted := len(allowedMimeSet) == 0 || allowedMimeSet[detectedMime] ||
						(detectedMime == "application/zip" && (ext == "docx" || ext == "xlsx"))
					if !mimeAccepted {
						c.JSON(http.StatusBadRequest, gin.H{
							"code":    400,
							"message": fmt.Sprintf("文件类型不匹配：检测到 %s，不在允许列表中", detectedMime),
						})
						c.Abort()
						return
					}

					// 4. 魔数与扩展名一致性校验（防止 .jpg.php 伪造）
					if !validateMagicNumber(ext, head) {
						c.JSON(http.StatusBadRequest, gin.H{
							"code":    400,
							"message": fmt.Sprintf("文件内容与扩展名 %s 不匹配（可能伪造扩展名）", ext),
						})
						c.Abort()
						return
					}

					// 5. 病毒扫描（如配置）
					if cfg.VirusScanEnabled && cfg.VirusScanner != nil {
						file, err := fileHeader.Open()
						if err != nil {
							c.JSON(http.StatusBadRequest, gin.H{"code": 400, "message": "无法读取文件进行扫描"})
							c.Abort()
							return
						}
						defer file.Close()
						// R57-FIX [P1]: 限制病毒扫描读取大小，防止超大文件导致 OOM
						// 文件大小已通过 fileHeader.Size > cfg.MaxFileSize 校验，此处作为防御性二次保护
						data, err := io.ReadAll(io.LimitReader(file, cfg.MaxFileSize+1))
						if err != nil {
							c.JSON(http.StatusInternalServerError, gin.H{"code": 500, "message": "文件读取失败"})
							c.Abort()
							return
						}
						if err := cfg.VirusScanner(fileHeader.Filename, data); err != nil {
							c.JSON(http.StatusBadRequest, gin.H{
								"code":    400,
								"message": fmt.Sprintf("病毒扫描未通过：%s", err.Error()),
							})
							c.Abort()
							return
						}
					}
				}
			}
		}

		c.Next()
	}
}

// detectMimeType 基于文件头检测 MIME 类型（不依赖客户端声明）
func detectMimeType(head []byte) string {
	if len(head) == 0 {
		return "application/octet-stream"
	}
	// 常见文件魔数
	switch {
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return "image/jpeg"
	case len(head) >= 8 && head[0] == 0x89 && head[1] == 0x50 && head[2] == 0x4E && head[3] == 0x47:
		return "image/png"
	case len(head) >= 6 && head[0] == 0x47 && head[1] == 0x49 && head[2] == 0x46:
		return "image/gif"
	case len(head) >= 2 && head[0] == 0x42 && head[1] == 0x4D:
		return "image/bmp"
	case len(head) >= 5 && string(head[:5]) == "%PDF-":
		return "application/pdf"
	case len(head) >= 4 && string(head[:4]) == "PK\x03\x04":
		// ZIP-based: docx/xlsx/zip — 进一步区分需要解析内部结构
		return "application/zip"
	case len(head) >= 8 && string(head[:8]) == "\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1":
		return "application/msword" // .doc / .xls
	case len(head) >= 4 && string(head[:4]) == "RIFF":
		return "image/webp"
	default:
		// 文本文件
		if isText(head) {
			return "text/plain"
		}
		return "application/octet-stream"
	}
}

// isText 判断是否为文本内容（简单启发式：无不可打印字符）
func isText(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	nonPrintable := 0
	for _, b := range data {
		if b == 0 {
			return false // 二进制文件通常包含 NULL 字节
		}
		if b < 9 || (b > 13 && b < 32) {
			nonPrintable++
		}
	}
	// AUTO-FIX-2026-07-14 [ConvergeLoop-P1]: 修复整数除法导致的短文本误判
	// 原代码 nonPrintable < len(data)/10 在 len(data)<10 时因整数除法为 0，
	// 导致任何含 1 个不可打印字符的短输入返回 false，单个可打印字符也返回 false。
	// 改用乘法避免整数除法精度丢失：nonPrintable*10 < len(data)
	return nonPrintable*10 < len(data) // 允许 10% 以下不可打印字符
}

// validateMagicNumber 校验文件魔数与扩展名是否一致（防伪造）
func validateMagicNumber(ext string, head []byte) bool {
	if len(head) == 0 {
		return false
	}
	switch ext {
	case "jpg", "jpeg":
		return len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF
	case "png":
		return len(head) >= 8 && head[0] == 0x89 && head[1] == 0x50 && head[2] == 0x4E && head[3] == 0x47
	case "gif":
		return len(head) >= 6 && head[0] == 0x47 && head[1] == 0x49 && head[2] == 0x46
	case "bmp":
		return len(head) >= 2 && head[0] == 0x42 && head[1] == 0x4D
	case "pdf":
		return len(head) >= 5 && string(head[:5]) == "%PDF-"
	case "docx", "xlsx":
		return len(head) >= 4 && string(head[:4]) == "PK\x03\x04"
	case "doc", "xls":
		return len(head) >= 8 && string(head[:8]) == "\xD0\xCF\x11\xE0\xA1\xB1\x1A\xE1"
	case "txt", "csv":
		return isText(head)
	case "webp":
		return len(head) >= 4 && string(head[:4]) == "RIFF"
	default:
		return true // 未知扩展名放行（白名单已限制）
	}
}
