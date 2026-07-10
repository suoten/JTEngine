package jt808

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

func Unescape(data []byte) []byte {
	result := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		if data[i] == 0x7D && i+1 < len(data) {
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
	return result
}

func SplitByDelimiter(data []byte) [][]byte {
	var messages [][]byte
	start := -1

	for i := 0; i < len(data); i++ {
		if data[i] == 0x7E {
			if start == -1 {
				start = i
			} else {
				msg := make([]byte, i-start+1)
				copy(msg, data[start:i+1])
				messages = append(messages, msg)
				start = -1
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