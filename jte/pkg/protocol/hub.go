package protocol

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"

	"go.uber.org/zap"
)

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
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(data) == 0 {
		return "", nil, fmt.Errorf("empty data")
	}

	if data[0] == 0x7E {
		if codec, ok := h.codecs[ProtocolJT808]; ok {
			msg, err := h.tryParse808(codec, data)
			if err == nil && msg != nil {
				if h.is1045Message(msg) {
					if c1045, ok2 := h.codecs[ProtocolJT1045]; ok2 {
						if msg1045, err2 := h.tryParse808(c1045, data); err2 == nil && msg1045 != nil {
							return ProtocolJT1045, msg1045, nil
						}
					}
				}
				return ProtocolJT808, msg, nil
			}
		}
		if codec, ok := h.codecs[ProtocolJT1078]; ok {
			msg, err := h.tryParse808(codec, data)
			if err == nil && msg != nil {
				return ProtocolJT1078, msg, nil
			}
		}
		if codec, ok := h.codecs[ProtocolJT1045]; ok {
			msg, err := h.tryParse808(codec, data)
			if err == nil && msg != nil {
				return ProtocolJT1045, msg, nil
			}
		}
		if codec, ok := h.codecs[ProtocolJT905]; ok {
			msg, err := h.tryParse808(codec, data)
			if err == nil && msg != nil {
				return ProtocolJT905, msg, nil
			}
		}
		// AUTO-FIX-2026-06-26: 1253使用0x5B/0x5E分隔符，从0x7E分组移除
	}

	// AUTO-FIX-2026-06-26: 0x5B开头的帧，先尝试809(0x5B...0x5D)，失败再尝试1253(0x5B...0x5E)
	if data[0] == 0x5B {
		if codec, ok := h.codecs[ProtocolJT809]; ok {
			msg, err := h.tryParse809(codec, data)
			if err == nil && msg != nil {
				return ProtocolJT809, msg, nil
			}
		}
		if codec, ok := h.codecs[ProtocolJT1253]; ok {
			msg, err := h.tryParse1253(codec, data)
			if err == nil && msg != nil {
				return ProtocolJT1253, msg, nil
			}
		}
	}

	if len(data) >= 2 && data[0] == 0x23 && data[1] == 0x23 {
		if codec, ok := h.codecs[ProtocolGBT32960]; ok {
			msg, err := h.tryParse32960(codec, data)
			if err == nil && msg != nil {
				return ProtocolGBT32960, msg, nil
			}
		}
	}

	if data[0] == 0x30 {
		if codec, ok := h.codecs[ProtocolJT1045]; ok {
			msg, err := h.tryParse1045(codec, data)
			if err == nil && msg != nil {
				return ProtocolJT1045, msg, nil
			}
		}
	}

	for pt, codec := range h.codecs {
		header, _, err := codec.ParseHeader(data)
		if err == nil && header != nil {
			body, err := codec.ParseBody(header.MsgID, data)
			if err == nil {
				return pt, &Message{Header: *header, Body: body}, nil
			}
		}
	}

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

func (h *Hub) tryParse808(codec Codec, raw []byte) (*Message, error) {
	delimited := raw
	if len(raw) >= 2 && raw[0] == 0x7E && raw[len(raw)-1] == 0x7E {
		delimited = raw[1 : len(raw)-1]
	}

	unescaped := unescape808(delimited)
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
	bodyEnd := len(unescaped) - 1
	if bodyEnd <= bodyStart {
		bodyEnd = len(unescaped)
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

func (h *Hub) tryParse809(codec Codec, raw []byte) (*Message, error) {
	delimited := raw
	// AUTO-FIX-2026-06-26: 结束符由0x5D修正为标准0x5E
	if len(raw) >= 2 && raw[0] == 0x5B && raw[len(raw)-1] == 0x5E {
		delimited = raw[1 : len(raw)-1]
	}

	unescaped := unescape809(delimited)
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
func (h *Hub) tryParse1253(codec Codec, raw []byte) (*Message, error) {
	delimited := raw
	if len(raw) >= 2 && raw[0] == 0x5B && raw[len(raw)-1] == 0x5E {
		delimited = raw[1 : len(raw)-1]
	} else {
		return nil, fmt.Errorf("invalid 1253 frame delimiter")
	}

	unescaped := unescape1253(delimited)
	if len(unescaped) < 22 {
		return nil, fmt.Errorf("1253 unescaped data too short")
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

	// CRC校验
	if !codec.VerifyChecksum(unescaped) {
		return nil, fmt.Errorf("1253 CRC checksum failed")
	}

	return &Message{Header: *header, Body: body, Raw: raw}, nil
}

func (h *Hub) tryParse32960(codec Codec, raw []byte) (*Message, error) {
	if len(raw) < 24 {
		return nil, fmt.Errorf("32960 data too short")
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

func (h *Hub) tryParse1045(codec Codec, raw []byte) (*Message, error) {
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

type FrameBuffer struct {
	buf       bytes.Buffer
	protocol  ProtocolType
	delimiter [2]byte
}

func (fb *FrameBuffer) GetProtocol() ProtocolType {
	return fb.protocol
}

func NewFrameBuffer(protocol ProtocolType) *FrameBuffer {
	fb := &FrameBuffer{
		protocol: protocol,
	}
	switch protocol {
	case ProtocolJT808, ProtocolJT1078, ProtocolJT905:
		fb.delimiter = [2]byte{0x7E, 0x7E}
	// AUTO-FIX-2026-06-26: 1253使用0x5B/0x5E分隔符（与809同组），从0x7E分组移除
	case ProtocolJT809, ProtocolJT1253:
		fb.delimiter = [2]byte{0x5B, 0x5E}
	case ProtocolGBT32960:
		fb.delimiter = [2]byte{0x23, 0x23}
	default:
		fb.delimiter = [2]byte{0x7E, 0x7E}
	}
	return fb
}

func (fb *FrameBuffer) Feed(data []byte) [][]byte {
	fb.buf.Write(data)
	return fb.extractFrames()
}

func (fb *FrameBuffer) extractFrames() [][]byte {
	var frames [][]byte
	data := fb.buf.Bytes()

	switch fb.protocol {
	case ProtocolJT808, ProtocolJT1078, ProtocolJT905:
		frames, data = fb.extractDelimited(data, 0x7E)
	// AUTO-FIX-2026-06-26: 1253使用0x5B/0x5E分隔符（与809同组），从0x7E分组移除
	case ProtocolJT809, ProtocolJT1253:
		frames, data = fb.extractBracketed(data, 0x5B, 0x5E)
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
				frame := make([]byte, i-start+1)
				copy(frame, data[start:i+1])
				frames = append(frames, frame)
				start = -1
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

func (fb *FrameBuffer) extractLengthPrefixed(data []byte) ([][]byte, []byte) {
	var frames [][]byte

	for len(data) >= 24 {
		if data[0] != 0x23 || data[1] != 0x23 {
			data = data[1:]
			continue
		}

		bodyLen := int(binary.BigEndian.Uint16(data[22:24]))
		totalLen := 24 + bodyLen + 1

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

func unescape808(data []byte) []byte {
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

func unescape809(data []byte) []byte {
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
		result = append(result, data[i])
		i++
	}
	return result
}

// AUTO-FIX-2026-06-26: 1253转义还原（0x5B/0x5E分隔符，仅处理0x5A/0x5E转义前缀，不还原0x5D）
func unescape1253(data []byte) []byte {
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
			if data[i+1] == 0x02 {
				result = append(result, 0x5E)
				i++
			} else {
				result = append(result, data[i])
			}
		} else {
			result = append(result, data[i])
		}
	}
	return result
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
