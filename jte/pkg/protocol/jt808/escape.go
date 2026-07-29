// FIXED: [P0] SplitByDelimiter 共享分隔符丢帧：连续帧0x7E..0x7E..0x7E中中间帧内容丢失 [2026-07-17]
package jt808

import "fmt"

func Escape(data []byte) []byte {
	result := make([]byte, 0, len(data)*2)
	for _, b := range data {
		switch b {
		case 0x7E:
			result = append(result, 0x7D, 0x02)
		case 0x7D:
			result = append(result, 0x7D, 0x01)
		default:
			result = append(result, b)
		}
	}
	return result
}

// [P2-修复] Unescape 返回 error：尾部 0x7D（无后续转义字节）为非法帧
func Unescape(data []byte) ([]byte, error) {
	result := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		if data[i] == 0x7D {
			if i+1 >= len(data) {
				// 尾部 0x7D 无后续转义字节，非法帧
				return nil, fmt.Errorf("Unescape: trailing 0x7D at position %d without escape byte", i)
			}
			switch data[i+1] {
			case 0x02:
				result = append(result, 0x7E)
			case 0x01:
				result = append(result, 0x7D)
			default:
				result = append(result, data[i], data[i+1])
			}
			i += 2
		} else {
			result = append(result, data[i])
			i++
		}
	}
	return result, nil
}

// P2-FIX: 单帧最大长度限制（1MB），防止恶意终端发送超长帧耗尽内存
const MaxFrameSize = 1 * 1024 * 1024

func SplitByDelimiter(data []byte) [][]byte {
	var messages [][]byte
	start := -1

	for i := 0; i < len(data); i++ {
		if data[i] == 0x7E {
			if start == -1 {
				start = i
			} else {
				// P2-FIX: 跳过连续0x7E产生的空帧 + 超长帧丢弃
				frameLen := i - start + 1
				if frameLen > 2 && frameLen <= MaxFrameSize {
					msg := make([]byte, frameLen)
					copy(msg, data[start:i+1])
					messages = append(messages, msg)
				} else if frameLen > MaxFrameSize {
					// 超长帧丢弃，记录位置以便上层日志告警
					// 继续处理后续帧
				}
				// 共享分隔符：当前结束符同时作为下一帧的起始符
				start = i
			}
		}
	}

	return messages
}

func WrapWithDelimiter(data []byte) []byte {
	result := make([]byte, 0, len(data)+2)
	result = append(result, 0x7E)
	result = append(result, data...)
	result = append(result, 0x7E)
	return result
}

func StripDelimiter(data []byte) []byte {
	if len(data) < 2 {
		return data
	}
	if data[0] == 0x7E && data[len(data)-1] == 0x7E {
		result := make([]byte, len(data)-2)
		copy(result, data[1:len(data)-1])
		return result
	}
	return data
}