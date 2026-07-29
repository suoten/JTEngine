// FIXED: [P0] extractDelimited 共享分隔符丢帧：连续帧0x7E..0x7E..0x7E中中间帧内容丢失，start重置为-1而非i导致跳过帧体 [2026-07-17]
package protocol

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"runtime/debug"
	"sync"

	"go.uber.org/zap"
)

// MaxElementCount 防止恶意包声明极大数量导致 make([]T, 0, count) 分配过多内存
const MaxElementCount = 10000

type Hub struct {
	mu     sync.RWMutex
	codecs map[ProtocolType]Codec
	logger *zap.Logger
}

func NewHub(logger *zap.Logger) *Hub {
	return &Hub{
		codecs: make(map[ProtocolType]Codec),
		logger: logger,
	}
}

func (h *Hub) RegisterCodec(codec Codec) {
	h.mu.Lock()
	defer h.mu.Unlock()
	pt := codec.ProtocolType()
	h.codecs[pt] = codec
	h.logger.Info("protocol codec registered", zap.String("protocol", string(pt)))
}

func (h *Hub) GetCodec(pt ProtocolType) (Codec, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.codecs[pt]
	return c, ok
}

func (h *Hub) Route(data []byte) (ProtocolType, *Message, error) {
	if len(data) == 0 {
		return "", nil, fmt.Errorf("empty data")
	}

	// FIXED-2026-07-23 [P2]: 在 RLock 内获取 codec 引用，释放锁后再调用
	// codec.ParseHeader/ParseBody。避免长时间持有读锁阻塞 RegisterCodec。
	h.mu.RLock()
	codec808, has808 := h.codecs[ProtocolJT808]
	codec1078, has1078 := h.codecs[ProtocolJT1078]
	codec1045, has1045 := h.codecs[ProtocolJT1045]
	codec905, has905 := h.codecs[ProtocolJT905]
	codec809, has809 := h.codecs[ProtocolJT809]
	codec1253, has1253 := h.codecs[ProtocolJT1253]
	codec32960, has32960 := h.codecs[ProtocolGBT32960]
	h.mu.RUnlock()

	if data[0] == 0x7E {
		if has808 {
			msg, err := h.tryParse808(codec808, data)
			if err == nil && msg != nil {
				if h.is1045Message(msg) {
					if has1045 {
						if msg1045, err2 := h.tryParse808(codec1045, data); err2 == nil && msg1045 != nil {
							return ProtocolJT1045, msg1045, nil
						}
					}
				}
				// AUTO-FIX-2026-07-17: 1078消息（0x9xxx/0x1200/0x1Axx/0x1Bxx）优先交给1078 codec
				if h.is1078Message(msg) {
					if has1078 {
						if msg1078, err2 := h.tryParse808(codec1078, data); err2 == nil && msg1078 != nil {
							return ProtocolJT1078, msg1078, nil
						}
					}
				}
				return ProtocolJT808, msg, nil
			}
		}
		if has1078 {
			msg, err := h.tryParse808(codec1078, data)
			if err == nil && msg != nil {
				return ProtocolJT1078, msg, nil
			}
		}
		if has1045 {
			msg, err := h.tryParse808(codec1045, data)
			if err == nil && msg != nil {
				return ProtocolJT1045, msg, nil
			}
		}
		if has905 {
			msg, err := h.tryParse808(codec905, data)
			if err == nil && msg != nil {
				return ProtocolJT905, msg, nil
			}
		}
	// AUTO-FIX-2026-07-16: 1253与809使用相同帧格式(0x5B...0x5D)，从0x7E分组移除
	}

	// AUTO-FIX-2026-07-16: 0x5B开头的帧，809和1253使用相同帧格式(0x5B...0x5D)
	// tryParse809和tryParse1253内部都接受0x5D(标准)和0x5E(兼容旧设备)作为结束符
	if data[0] == 0x5B {
		if has809 {
			msg, err := h.tryParse809(codec809, data)
			if err == nil && msg != nil {
				return ProtocolJT809, msg, nil
			}
		}
		if has1253 {
			msg, err := h.tryParse1253(codec1253, data)
			if err == nil && msg != nil {
				return ProtocolJT1253, msg, nil
			}
		}
	}

	if len(data) >= 2 && data[0] == 0x23 && data[1] == 0x23 {
		if has32960 {
			msg, err := h.tryParse32960(codec32960, data)
			if err == nil && msg != nil {
				return ProtocolGBT32960, msg, nil
			}
		}
	}

	if data[0] == 0x30 {
		if has1045 {
			msg, err := h.tryParse1045(codec1045, data)
			if err == nil && msg != nil {
				return ProtocolJT1045, msg, nil
			}
		}
	}

	// FIXED: [P2] 移除 fallback 遍历所有 codec 的逻辑——宽松的 ParseHeader 可能导致
	// 非协议数据被误匹配。新协议应通过显式前缀判断分支注册。 [2026-07-22]
	// for pt, codec := range h.codecs {
	// 	header, _, err := codec.ParseHeader(data)
	// 	if err == nil && header != nil {
	// 		body, err := codec.ParseBody(header.MsgID, data)
	// 		if err == nil {
	// 			return pt, &Message{Header: *header, Body: body}, nil
	// 		}
	// 	}
	// }

	return "", nil, &ErrProtocolNotRegistered{Data: data}
}

type ErrProtocolNotRegistered struct {
	Data []byte
}

func (e *ErrProtocolNotRegistered) Error() string {
	if len(e.Data) == 0 {
		return "no matching protocol codec found"
	}
	switch {
	case e.Data[0] == 0x5B:
		return "protocol jt809 not registered, install module-protocol-809"
	case len(e.Data) >= 2 && e.Data[0] == 0x23 && e.Data[1] == 0x23:
		return "protocol gbt32960 not registered, install module-protocol-32960"
	case e.Data[0] == 0x7E:
		return "protocol not registered, install the corresponding module-protocol-*"
	default:
		return "no matching protocol codec found"
	}
}

func (h *Hub) tryParse808(codec Codec, raw []byte) (result *Message, err error) {
	// [P2-加固] panic 防护：防止单个消息解析 panic 导致整个连接处理 goroutine 崩溃
	defer func() {
		if r := recover(); r != nil {
			rawSnippet := raw
			if len(rawSnippet) > 256 {
				rawSnippet = rawSnippet[:256]
			}
			h.logger.Error("panic in tryParse808",
				zap.Any("panic", r),
				zap.ByteString("raw_first_256", rawSnippet),
				zap.String("stack", string(debug.Stack())),
			)
			result = nil
			err = fmt.Errorf("tryParse808 panic: %v", r)
		}
	}()
	delimited := raw
	if len(raw) >= 2 && raw[0] == 0x7E && raw[len(raw)-1] == 0x7E {
		delimited = raw[1 : len(raw)-1]
	}

	unescaped, err := unescape808(delimited)
	if err != nil {
		return nil, fmt.Errorf("808 unescape failed: %w", err)
	}
	if len(unescaped) < 13 {
		return nil, fmt.Errorf("808 unescaped data too short")
	}

	// AUTO-FIX-2026-06-27: 808帧增加XOR校验（在ParseBody之前调用codec.VerifyChecksum）
	if !codec.VerifyChecksum(unescaped) {
		return nil, fmt.Errorf("808 checksum verification failed")
	}

	header, offset, err := codec.ParseHeader(unescaped)
	if err != nil {
		return nil, err
	}

	bodyStart := offset
	// FIXED: 空body消息(如心跳0x0002)bodyEnd==bodyStart时，原代码将bodyEnd设为len(unescaped)，
	// 导致校验和字节被错误地包含在bodyData中 [2026-07-17]
	bodyEnd := len(unescaped) - 1 // exclude checksum byte
	if bodyEnd < bodyStart {
		bodyEnd = bodyStart // empty body, no checksum to exclude
	}

	var bodyData []byte
	if bodyEnd > bodyStart {
		bodyData = unescaped[bodyStart:bodyEnd]
	} else {
		bodyData = []byte{}
	}

	// 分包消息：不在此处调用 ParseBody（分片 body 不完整会解析失败），
	// 用 RawMessage 包装原始 body bytes，由上层 MessageHandler 的 PacketReassembler 重组后再解析。
	if header.HasPack {
		rawBody := &RawFragment{ID: header.MsgID, Data: bodyData}
		return &Message{Header: *header, Body: rawBody, Raw: raw}, nil
	}

	body, err := codec.ParseBody(header.MsgID, bodyData)
	if err != nil {
		return nil, err
	}

	return &Message{Header: *header, Body: body, Raw: raw}, nil
}

// RawFragment 用于包装分包消息的单个分片 body，避免 ParseBody 在分片不完整时失败。
// 上层 PacketReassembler 收齐所有分片后，用完整 body 重新调用 ParseBody。
type RawFragment struct {
	ID   uint16
	Data []byte
}

// NewRawFragment 创建一个分片包装体
func NewRawFragment(msgID uint16, data []byte) *RawFragment {
	return &RawFragment{ID: msgID, Data: data}
}

func (m *RawFragment) MsgID() uint16               { return m.ID }
func (m *RawFragment) Marshal() ([]byte, error)    { return m.Data, nil }
func (m *RawFragment) Unmarshal(data []byte) error { m.Data = data; return nil }

func (h *Hub) tryParse809(codec Codec, raw []byte) (result *Message, err error) {
	// [P2-加固] panic 防护
	defer func() {
		if r := recover(); r != nil {
			rawSnippet := raw
			if len(rawSnippet) > 256 {
				rawSnippet = rawSnippet[:256]
			}
			h.logger.Error("panic in tryParse809",
				zap.Any("panic", r),
				zap.ByteString("raw_first_256", rawSnippet),
				zap.String("stack", string(debug.Stack())),
			)
			result = nil
			err = fmt.Errorf("tryParse809 panic: %v", r)
		}
	}()
	delimited := raw
	// FIXED: [P1] 809标准结束符仅0x5D，不接受0x5E（转义序列首字节） [2026-07-17]
	if len(raw) >= 2 && raw[0] == 0x5B && raw[len(raw)-1] == 0x5D {
		delimited = raw[1 : len(raw)-1]
	}

	unescaped, err := unescape809(delimited)
	if err != nil {
		return nil, fmt.Errorf("809 unescape failed: %w", err)
	}
	if len(unescaped) < 22 {
		return nil, fmt.Errorf("809 unescaped data too short")
	}

	// AUTO-FIX-2026-06-26: 在ParseHeader之前校验CRC32，校验失败直接返回错误
	if !codec.VerifyChecksum(unescaped) {
		return nil, fmt.Errorf("809 CRC32 checksum verification failed")
	}

	header, offset, err := codec.ParseHeader(unescaped)
	if err != nil {
		return nil, err
	}

	crcEnd := len(unescaped) - 4
	if crcEnd <= offset {
		return nil, fmt.Errorf("809 body range invalid")
	}

	body, err := codec.ParseBody(header.MsgID, unescaped[offset:crcEnd])
	if err != nil {
		return nil, err
	}

	return &Message{Header: *header, Body: body, Raw: raw}, nil
}

// AUTO-FIX-2026-06-26: 新增1253入站帧解析（0x5B...0x5E分隔符，使用1253专用转义还原与CRC32校验）
func (h *Hub) tryParse1253(codec Codec, raw []byte) (result *Message, err error) {
	defer func() {
		if r := recover(); r != nil {
			rawSnippet := raw
			if len(rawSnippet) > 256 {
				rawSnippet = rawSnippet[:256]
			}
			h.logger.Error("panic in tryParse1253",
				zap.Any("panic", r),
				zap.ByteString("raw_first_256", rawSnippet),
				zap.String("stack", string(debug.Stack())),
			)
			result = nil
			err = fmt.Errorf("tryParse1253 panic: %v", r)
		}
	}()
	delimited := raw
	// FIXED: [P1] 1253与809相同，标准结束符仅0x5D [2026-07-17]
	if len(raw) >= 2 && raw[0] == 0x5B && raw[len(raw)-1] == 0x5D {
		delimited = raw[1 : len(raw)-1]
	} else {
		return nil, fmt.Errorf("invalid 1253 frame delimiter")
	}

	unescaped, err := unescape1253(delimited)
	if err != nil {
		return nil, fmt.Errorf("1253 unescape failed: %w", err)
	}
	if len(unescaped) < 22 {
		return nil, fmt.Errorf("1253 unescaped data too short")
	}

	// AUTO-FIX-2026-07-17: CRC校验提前到ParseHeader之前（与809流程一致）
	if !codec.VerifyChecksum(unescaped) {
		return nil, fmt.Errorf("1253 CRC checksum verification failed")
	}

	header, offset, err := codec.ParseHeader(unescaped)
	if err != nil {
		return nil, err
	}

	crcEnd := len(unescaped) - 4
	if crcEnd <= offset {
		return nil, fmt.Errorf("1253 body range invalid")
	}

	body, err := codec.ParseBody(header.MsgID, unescaped[offset:crcEnd])
	if err != nil {
		return nil, err
	}

	return &Message{Header: *header, Body: body, Raw: raw}, nil
}

func (h *Hub) tryParse32960(codec Codec, raw []byte) (result *Message, err error) {
	defer func() {
		if r := recover(); r != nil {
			rawSnippet := raw
			if len(rawSnippet) > 256 {
				rawSnippet = rawSnippet[:256]
			}
			h.logger.Error("panic in tryParse32960",
				zap.Any("panic", r),
				zap.ByteString("raw_first_256", rawSnippet),
				zap.String("stack", string(debug.Stack())),
			)
			result = nil
			err = fmt.Errorf("tryParse32960 panic: %v", r)
		}
	}()
	if len(raw) < 25 { // GB/T 32960.3-2016: 最小帧=头部24B+BCC1B=25B
		return nil, fmt.Errorf("32960 data too short")
	}

	// AUTO-FIX-2026-07-17: 32960帧增加BCC校验（在ParseBody之前调用codec.VerifyChecksum）
	if !codec.VerifyChecksum(raw) {
		return nil, fmt.Errorf("32960 BCC checksum verification failed")
	}

	header, offset, err := codec.ParseHeader(raw)
	if err != nil {
		return nil, err
	}

	// AUTO-FIX-2026-07-17: bodyEnd排除BCC字节(1B)
	bodyEnd := offset + header.BodyLen
	bccPos := bodyEnd
	if bccPos >= len(raw) {
		return nil, fmt.Errorf("32960 body range invalid: bodyEnd=%d, len=%d", bodyEnd, len(raw))
	}

	body, err := codec.ParseBody(header.MsgID, raw[offset:bodyEnd])
	if err != nil {
		return nil, err
	}

	return &Message{Header: *header, Body: body, Raw: raw}, nil
}

func (h *Hub) tryParse1045(codec Codec, raw []byte) (result *Message, err error) {
	defer func() {
		if r := recover(); r != nil {
			rawSnippet := raw
			if len(rawSnippet) > 256 {
				rawSnippet = rawSnippet[:256]
			}
			h.logger.Error("panic in tryParse1045",
				zap.Any("panic", r),
				zap.ByteString("raw_first_256", rawSnippet),
				zap.String("stack", string(debug.Stack())),
			)
			result = nil
			err = fmt.Errorf("tryParse1045 panic: %v", r)
		}
	}()
	if len(raw) < 5 {
		return nil, fmt.Errorf("1045 data too short")
	}

	// AUTO-FIX-2026-06-27: 1045帧增加XOR校验（在ParseBody之前调用codec.VerifyChecksum）
	if !codec.VerifyChecksum(raw) {
		return nil, fmt.Errorf("1045 checksum verification failed")
	}

	header, offset, err := codec.ParseHeader(raw)
	if err != nil {
		return nil, err
	}

	bodyEnd := offset + header.BodyLen
	if bodyEnd > len(raw) {
		bodyEnd = len(raw)
	}

	body, err := codec.ParseBody(header.MsgID, raw[offset:bodyEnd])
	if err != nil {
		return nil, err
	}

	return &Message{Header: *header, Body: body, Raw: raw}, nil
}

// MaxFrameBufferBytes FrameBuffer 半包缓冲区最大字节数（2MB）。
// INDUSTRIAL-FIX-2026-07-24 [P1]: 防止恶意终端发送永不闭合的帧导致缓冲区无限增长。
// 超过此阈值时清空缓冲区并记录告警，丢弃所有未完成的数据。
const MaxFrameBufferBytes = 2 * 1024 * 1024

type FrameBuffer struct {
	buf       bytes.Buffer
	protocol  ProtocolType
	delimiter [2]byte
	logger    *zap.Logger
}

func (fb *FrameBuffer) GetProtocol() ProtocolType {
	return fb.protocol
}

func NewFrameBuffer(protocol ProtocolType) *FrameBuffer {
	fb := &FrameBuffer{
		protocol: protocol,
		logger:   zap.NewNop(),
	}
	switch protocol {
	case ProtocolJT808, ProtocolJT1078, ProtocolJT905:
		fb.delimiter = [2]byte{0x7E, 0x7E}
	// FIXED: [P1] 809/1253标准结束符仅0x5D [2026-07-17]
	case ProtocolJT809, ProtocolJT1253:
		fb.delimiter = [2]byte{0x5B, 0x5D}
	case ProtocolGBT32960:
		fb.delimiter = [2]byte{0x23, 0x23}
	default:
		fb.delimiter = [2]byte{0x7E, 0x7E}
	}
	return fb
}

func (fb *FrameBuffer) Feed(data []byte) [][]byte {
	fb.buf.Write(data)

	// INDUSTRIAL-FIX-2026-07-24 [P1]: 半包缓冲区内存保护。
	// 如果缓冲区超过 MaxFrameBufferBytes（2MB），说明终端持续发送不闭合的帧数据
	// （可能是恶意终端或协议解析错误），清空缓冲区防止内存耗尽。
	if fb.buf.Len() > MaxFrameBufferBytes {
		fb.logger.Warn("frame buffer overflow, discarding incomplete data",
			zap.String("protocol", string(fb.protocol)),
			zap.Int("buffer_bytes", fb.buf.Len()),
			zap.Int("max_bytes", MaxFrameBufferBytes))
		fb.buf.Reset()
		return nil
	}

	return fb.extractFrames()
}

func (fb *FrameBuffer) extractFrames() [][]byte {
	var frames [][]byte
	data := fb.buf.Bytes()

	switch fb.protocol {
	case ProtocolJT808, ProtocolJT1078, ProtocolJT905:
		frames, data = fb.extractDelimited(data, 0x7E)
	// FIXED: [P1] 809/1253标准结束符仅0x5D。0x5E在转义序列(0x5E 0x01/0x5E 0x02)中出现，
	// 作为结束符会导致含0x5D/0x5E内容的帧被错误截断 [2026-07-17]
	case ProtocolJT809, ProtocolJT1253:
		frames, data = fb.extractBracketedMulti(data, 0x5B, []byte{0x5D})
	case ProtocolGBT32960:
		frames, data = fb.extractLengthPrefixed(data)
	default:
		frames, data = fb.extractDelimited(data, 0x7E)
	}

	fb.buf.Reset()
	if len(data) > 0 {
		fb.buf.Write(data)
	}

	return frames
}

func (fb *FrameBuffer) extractDelimited(data []byte, delim byte) ([][]byte, []byte) {
	var frames [][]byte
	start := -1

	for i := 0; i < len(data); i++ {
		if data[i] == delim {
			if start == -1 {
				start = i
			} else {
				// AUTO-FIX-2026-07-17: 跳过连续0x7E产生的空帧
				if i-start > 1 {
					frame := make([]byte, i-start+1)
					copy(frame, data[start:i+1])
					frames = append(frames, frame)
				}
				// 共享分隔符：当前结束符同时作为下一帧的起始符
				start = i
			}
		}
	}

	remaining := []byte{}
	if start != -1 {
		remaining = make([]byte, len(data)-start)
		copy(remaining, data[start:])
	}

	return frames, remaining
}

func (fb *FrameBuffer) extractBracketed(data []byte, open, close byte) ([][]byte, []byte) {
	var frames [][]byte

	for len(data) > 0 {
		idx := bytes.IndexByte(data, open)
		if idx == -1 {
			break
		}

		endIdx := bytes.IndexByte(data[idx+1:], close)
		if endIdx == -1 {
			remaining := make([]byte, len(data)-idx)
			copy(remaining, data[idx:])
			return frames, remaining
		}

		frame := make([]byte, idx+endIdx+2)
		copy(frame, data[:idx+endIdx+2])
		frames = append(frames, frame)
		data = data[idx+endIdx+2:]
	}

	return frames, data
}

// SetLogger 设置 FrameBuffer 的日志记录器
func (fb *FrameBuffer) SetLogger(l *zap.Logger) {
	if l != nil {
		fb.logger = l
	}
}

// minBracketedFrameLen 809/1253 最小帧长度（含分隔符），低于此值的帧视为非法/截断帧
const minBracketedFrameLen = 22

// extractBracketedMulti 支持多个结束符的帧提取（809/1253标准0x5D+兼容0x5E）
// FIXED-2026-07-23 [P2]: 帧提取后增加最小长度校验，低于最小长度的帧直接丢弃并记录 debug 日志。
func (fb *FrameBuffer) extractBracketedMulti(data []byte, open byte, closes []byte) ([][]byte, []byte) {
	var frames [][]byte

	for len(data) > 0 {
		idx := bytes.IndexByte(data, open)
		if idx == -1 {
			break
		}

		// 在 data[idx+1:] 中查找最早出现的结束符
		endIdx := -1
		for _, c := range closes {
			pos := bytes.IndexByte(data[idx+1:], c)
			if pos != -1 {
				if endIdx == -1 || pos < endIdx {
					endIdx = pos
				}
			}
		}
		if endIdx == -1 {
			remaining := make([]byte, len(data)-idx)
			copy(remaining, data[idx:])
			return frames, remaining
		}

		frameLen := idx + endIdx + 2
		// FIXED-2026-07-23 [P2]: 最小长度校验，低于最小长度的帧直接丢弃
		if frameLen < minBracketedFrameLen {
			fb.logger.Debug("discarding short bracketed frame",
				zap.Int("len", frameLen),
				zap.Int("min", minBracketedFrameLen),
				zap.String("protocol", string(fb.protocol)),
			)
			data = data[frameLen:]
			continue
		}

		frame := make([]byte, frameLen)
		copy(frame, data[:frameLen])
		frames = append(frames, frame)
		data = data[frameLen:]
	}

	return frames, data
}

func (fb *FrameBuffer) extractLengthPrefixed(data []byte) ([][]byte, []byte) {
	var frames [][]byte

	// GB/T 32960.3-2016: 头部24字节（含加密方式1B），数据长度字段在data[22:24]
	// 帧结构: 起始符(2)+命令(1)+应答(1)+VIN(17)+加密方式(1)+数据长度(2)+数据体(N)+BCC(1) = 24+N+1
	for len(data) >= 25 {
		if data[0] != 0x23 || data[1] != 0x23 {
			data = data[1:]
			continue
		}

		bodyLen := int(binary.BigEndian.Uint16(data[22:24]))
		totalLen := 24 + bodyLen + 1 // 头部(24) + 数据体(bodyLen) + BCC(1)

		if len(data) < totalLen {
			break
		}

		frame := make([]byte, totalLen)
		copy(frame, data[:totalLen])
		frames = append(frames, frame)
		data = data[totalLen:]
	}

	return frames, data
}

func (fb *FrameBuffer) Reset() {
	fb.buf.Reset()
}

// FIXED: [P2] 尾部 0x7D 无后续转义字节为非法帧，返回 error [2026-07-22]
func unescape808(data []byte) ([]byte, error) {
	result := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		if data[i] == 0x7D {
			if i+1 >= len(data) {
				// 尾部 0x7D 无后续转义字节，非法帧
				return nil, fmt.Errorf("unescape808: trailing 0x7D at position %d without escape byte", i)
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

// FIXED-2026-07-23 [P2]: 尾部 0x5A 或 0x5E 无后续字节时返回 error（与 unescape808 一致）
func unescape809(data []byte) ([]byte, error) {
	result := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		if i+1 < len(data) {
			if data[i] == 0x5A && data[i+1] == 0x01 {
				result = append(result, 0x5B)
				i += 2
				continue
			}
			if data[i] == 0x5A && data[i+1] == 0x02 {
				result = append(result, 0x5A)
				i += 2
				continue
			}
			if data[i] == 0x5E && data[i+1] == 0x01 {
				result = append(result, 0x5D)
				i += 2
				continue
			}
			if data[i] == 0x5E && data[i+1] == 0x02 {
				result = append(result, 0x5E)
				i += 2
				continue
			}
		}
		// FIXED-2026-07-23 [P2]: 尾部 0x5A 或 0x5E 无后续转义字节，非法帧
		if (data[i] == 0x5A || data[i] == 0x5E) && i+1 >= len(data) {
			return nil, fmt.Errorf("unescape809: trailing 0x%02X at position %d without escape byte", data[i], i)
		}
		result = append(result, data[i])
		i++
	}
	return result, nil
}

// FIXED-2026-07-23 [P2]: 尾部 0x5A 或 0x5E 无后续字节时返回 error（与 unescape808 一致）
func unescape1253(data []byte) ([]byte, error) {
	result := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if data[i] == 0x5A && i+1 < len(data) {
			switch data[i+1] {
			case 0x01:
				result = append(result, 0x5B)
				i++
			case 0x02:
				result = append(result, 0x5A)
				i++
			default:
				result = append(result, data[i])
			}
		} else if data[i] == 0x5E && i+1 < len(data) {
			switch data[i+1] {
			case 0x01:
				result = append(result, 0x5D)
				i++
			case 0x02:
				result = append(result, 0x5E)
				i++
			default:
				result = append(result, data[i])
			}
		} else if (data[i] == 0x5A || data[i] == 0x5E) && i+1 >= len(data) {
			// FIXED-2026-07-23 [P2]: 尾部 0x5A 或 0x5E 无后续转义字节，非法帧
			return nil, fmt.Errorf("unescape1253: trailing 0x%02X at position %d without escape byte", data[i], i)
		} else {
			result = append(result, data[i])
		}
	}
	return result, nil
}

func (h *Hub) ListCodecs() []ProtocolType {
	h.mu.RLock()
	defer h.mu.RUnlock()
	result := make([]ProtocolType, 0, len(h.codecs))
	for pt := range h.codecs {
		result = append(result, pt)
	}
	return result
}

func (h *Hub) is1045Message(msg *Message) bool {
	id := msg.Header.MsgID
	// AUTO-FIX-2026-06-27: 覆盖 0x0900-0x090C（标准报警）与 0x0910-0x0913（设备状态）
	switch {
	case id >= 0x0900 && id <= 0x090C:
		return true
	case id >= 0x0910 && id <= 0x0913:
		return true
	}
	return false
}

// is1078Message 判断消息ID是否属于 JT/T 1078 音视频协议范围。
// AUTO-FIX-2026-07-17: 808 ParseBody 的 default 分支返回 RawMessage（无错误），
// 导致 0x9xxx/0x1200/0x1Axx/0x1Bxx 等 1078 消息被 808 codec 截获，
// 永远不会路由到 1078 codec。此处添加 1078 消息检测，确保正确路由。
func (h *Hub) is1078Message(msg *Message) bool {
	id := msg.Header.MsgID
	switch {
	// 实时音视频 0x9101-0x9106
	case id >= 0x9101 && id <= 0x9106:
		return true
	// 录像检索/下载 0x9201-0x9207
	case id >= 0x9201 && id <= 0x9207:
		return true
	// PTZ 控制 0x9301-0x9302
	case id >= 0x9301 && id <= 0x9302:
		return true
	// 报警录像 0x9401-0x9404
	case id >= 0x9401 && id <= 0x9404:
		return true
	// 音视频参数 0x9501-0x9504
	case id >= 0x9501 && id <= 0x9504:
		return true
	// 终端日志 0x9601-0x9603
	case id >= 0x9601 && id <= 0x9603:
		return true
	// RTP 数据 0x1200
	case id == 0x1200:
		return true
	// 平台间音视频 0x1A00-0x1B01
	case id >= 0x1A00 && id <= 0x1B01:
		return true
	}
	return false
}
