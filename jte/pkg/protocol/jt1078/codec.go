package jt1078

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"

	"github.com/suoten/jt-engine/pkg/protocol"
	"golang.org/x/text/encoding/simplifiedchinese"
)

const (
	MsgIDRealtimeRequest    uint16 = 0x9101
	MsgIDRealtimeResponse   uint16 = 0x9102
	// AUTO-FIX-2026-06-26: 补充终端主动发起实时音视频传输消息（1078-2016）
	MsgIDTermAVRequest      uint16 = 0x9103 // 终端→平台 终端发起实时音视频传输请求
	MsgIDTermAVResponse     uint16 = 0x9104 // 平台→终端 平台应答终端实时音视频传输
	// FIXED-2026-07-17 [P0]: 0x9105/0x9106 按 JT/T 1078-2016 标准修正为实时音视频传输状态通知/应答
	// 原错误映射为"单条音视频检索请求/应答"，实际 0x9105 = 实时音视频传输状态通知（终端→平台），
	// 0x9106 = 实时音视频传输状态通知应答（平台→终端）。
	// 单条音视频检索功能应由 0x9201/0x9202 历史音视频检索承担。
	MsgIDAVStatusNotification         uint16 = 0x9105 // 实时音视频传输状态通知（终端→平台）
	MsgIDAVStatusNotificationResponse uint16 = 0x9106 // 实时音视频传输状态通知应答（平台→终端）
	// 向后兼容别名（已弃用，新代码请使用 MsgIDAVStatusNotification）
	MsgIDSingleAVRetrievalRequest  = MsgIDAVStatusNotification
	MsgIDSingleAVRetrievalResponse = MsgIDAVStatusNotificationResponse
	MsgIDPlaybackRequest    uint16 = 0x9201
	MsgIDPlaybackResponse   uint16 = 0x9202
	MsgIDPlaybackControl    uint16 = 0x9203
	MsgIDPlaybackControlAck uint16 = 0x9204
	MsgIDDownloadRequest    uint16 = 0x9205
	MsgIDDownloadResponse   uint16 = 0x9206
	// AUTO-FIX-2026-06-27: 新增 0x9207 录像下载控制（平台→终端）
	MsgIDDownloadControl    uint16 = 0x9207 // 录像下载控制
	MsgIDRTPData            uint16 = 0x1200
	MsgIDAlarmVideoRequest  uint16 = 0x9401
	MsgIDAlarmVideoResponse uint16 = 0x9402
	// AUTO-FIX-2026-06-27: 新增 0x9403/0x9404 文件上传请求/应答
	MsgIDFileUploadRequest  uint16 = 0x9403 // 文件上传请求（终端→平台）
	MsgIDFileUploadResponse uint16 = 0x9404 // 文件上传应答（平台→终端）
	MsgIDPTZControl         uint16 = 0x9301
	// AUTO-FIX-2026-06-27: 新增 0x9302 PTZ 控制应答（终端→平台）
	MsgIDPTZControlAck      uint16 = 0x9302 // PTZ 控制应答
	MsgIDAVParamSet         uint16 = 0x9501
	MsgIDAVParamQuery       uint16 = 0x9502
	MsgIDAVParamResponse    uint16 = 0x9503
	// AUTO-FIX-2026-06-28: 新增 0x9504 删除音视频参数（平台→终端）
	MsgIDAVParamDel         uint16 = 0x9504 // 删除音视频参数
	MsgIDTerminalLogReq     uint16 = 0x9601 // 终端日志检索请求（平台→终端）
	MsgIDTerminalLogResp    uint16 = 0x9602 // 终端日志检索应答（终端→平台）
	MsgIDTerminalLogUpload  uint16 = 0x9603 // 终端日志上传请求（平台→终端）
	// 以下为 809-2019 新增的平台间视频消息，1078-2022 标准纳入协同体系。
	// 编解码结构体与处理逻辑由 module-protocol-809 实现，1078 侧显式注册为 RawMessage
	// 避免被当作未知消息丢弃，并保持消息ID表完整。
	MsgIDAVNegotiate        uint16 = 0x1A00 // 平台间音视频协商请求
	MsgIDAVNegotiateResp    uint16 = 0x1A01 // 平台间音视频协商应答
	MsgIDAVForward          uint16 = 0x1B00 // 平台间音视频转发请求
	MsgIDAVForwardResp      uint16 = 0x1B01 // 平台间音视频转发应答
	// AUTO-FIX-2026-06-27: PTZ 控制位按 JT/T 1078-2016 标准 5B 格式重新定义。
	// 控制指令 4B = 字节1(光圈/聚焦/变倍位) + 字节2(方向位) + 字节3(水平速度) + 字节4(垂直速度)
	// 字节1 位定义
	PTZLight     = 0x01 // 灯光
	PTZWiper     = 0x02 // 雨刷
	PTZZoomOut   = 0x04 // 变倍-
	PTZZoomIn    = 0x08 // 变倍+
	PTZFocusNear = 0x10 // 聚焦-
	PTZFocusFar  = 0x20 // 聚焦+
	PTZIrisClose = 0x40 // 光圈-
	PTZIrisOpen  = 0x80 // 光圈+
	// 字节2 方向位定义（位于控制指令第2字节）
	PTZDirDown  = 0x01 // 下
	PTZDirUp    = 0x02 // 上
	PTZDirLeft  = 0x04 // 左
	PTZDirRight = 0x08 // 右
)

const (
	StreamTypeMain = 0
	StreamTypeSub  = 1
)

const (
	MediaTypeVideo = 0
	MediaTypeAudio = 1
	MediaTypeAV    = 2
)

type JT1078Codec struct{}

func NewCodec() *JT1078Codec {
	return &JT1078Codec{}
}

func (c *JT1078Codec) ProtocolType() protocol.ProtocolType {
	return protocol.ProtocolJT1078
}

func (c *JT1078Codec) ParseHeader(data []byte) (*protocol.MessageHeader, int, error) {
	if len(data) < 12 {
		return nil, 0, fmt.Errorf("1078 header too short: %d bytes", len(data))
	}

	header := &protocol.MessageHeader{}
	header.MsgID = binary.BigEndian.Uint16(data[0:2])
	header.BodyAttr = binary.BigEndian.Uint16(data[2:4])
	header.BodyLen = int(header.BodyAttr & 0x03FF)
	header.EncryptMethod = uint8((header.BodyAttr >> 10) & 0x07)
	header.Version2019 = (header.BodyAttr & 0x8000) != 0
	header.HasPack = (header.BodyAttr & 0x2000) != 0

	// AUTO-FIX-2026-07-17: Bit15=1 时，data[4] 为协议版本号，Phone 从 data[5] 开始
	phoneStart := 4
	if header.Version2019 {
		if len(data) < 13 {
			return nil, 0, fmt.Errorf("1078 2019 header too short: %d bytes", len(data))
		}
		header.ProtocolVer = data[4]
		phoneStart = 5
	}

	phoneBCD := data[phoneStart : phoneStart+6]
	header.Phone = bcdToStringSafe(phoneBCD)
	header.SeqNum = binary.BigEndian.Uint16(data[phoneStart+6 : phoneStart+8])

	offset := phoneStart + 8
	if header.HasPack && len(data) >= offset+4 {
		header.PackTotal = binary.BigEndian.Uint16(data[offset : offset+2])
		header.PackIndex = binary.BigEndian.Uint16(data[offset+2 : offset+4])
		offset += 4
	}

	return header, offset, nil
}

func (c *JT1078Codec) EncodeHeader(header *protocol.MessageHeader) ([]byte, error) {
	buf := make([]byte, 0, 16)

	msgIDBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(msgIDBytes, header.MsgID)
	buf = append(buf, msgIDBytes...)

	bodyAttr := uint16(header.BodyLen) & 0x03FF
	// AUTO-FIX-2026-07-17: 设置 Bit 10-12 加密方式
	bodyAttr |= (uint16(header.EncryptMethod) & 0x07) << 10
	// AUTO-FIX-2026-07-17: 设置 Bit 15 版本标识
	if header.Version2019 {
		bodyAttr |= 0x8000
	}
	if header.HasPack {
		bodyAttr |= 0x2000
	}
	bodyAttrBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(bodyAttrBytes, bodyAttr)
	buf = append(buf, bodyAttrBytes...)

	// AUTO-FIX-2026-07-17: 2019 版本写入协议版本号字节
	if header.Version2019 {
		buf = append(buf, header.ProtocolVer)
	}

	phoneBCD, err := stringToBCD6(header.Phone)
	if err != nil {
		return nil, fmt.Errorf("encode header: %w", err)
	}
	buf = append(buf, phoneBCD...)

	seqBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(seqBytes, header.SeqNum)
	buf = append(buf, seqBytes...)

	// AUTO-FIX-2026-06-26: 补充分包信息编码。原实现仅设置 bodyAttr 的 0x2000 位，
	// 但未写入4字节分包信息(PackTotal + PackIndex)，导致大消息分包后接收端无法重组。
	// 与 jt808 codec 的 EncodeHeader 行为保持一致。
	if header.HasPack {
		packTotal := make([]byte, 2)
		binary.BigEndian.PutUint16(packTotal, header.PackTotal)
		buf = append(buf, packTotal...)
		packIndex := make([]byte, 2)
		binary.BigEndian.PutUint16(packIndex, header.PackIndex)
		buf = append(buf, packIndex...)
	}

	return buf, nil
}

func (c *JT1078Codec) ParseBody(msgID uint16, data []byte) (protocol.MessageBody, error) {
	var body protocol.MessageBody

	switch msgID {
	case MsgIDRealtimeRequest:
		body = &RealtimeRequestMessage{}
	case MsgIDRealtimeResponse:
		body = &RealtimeResponseMessage{}
	// AUTO-FIX-2026-06-26: 注册终端主动发起实时音视频传输消息
	case MsgIDTermAVRequest:
		body = &TermAVRequestMessage{}
	case MsgIDTermAVResponse:
		body = &TermAVResponseMessage{}
	// FIXED-2026-07-17 [P0]: 0x9105/0x9106 修正为实时音视频传输状态通知/应答
	case MsgIDAVStatusNotification:
		body = &AVStatusNotificationMessage{}
	case MsgIDAVStatusNotificationResponse:
		body = &AVStatusNotificationResponseMessage{}
	case MsgIDPlaybackRequest:
		body = &PlaybackRequestMessage{}
	case MsgIDPlaybackResponse:
		body = &PlaybackResponseMessage{}
	case MsgIDPlaybackControl:
		body = &PlaybackControlMessage{}
	case MsgIDPlaybackControlAck:
		body = &PlaybackControlAckMessage{}
	case MsgIDDownloadRequest:
		body = &DownloadRequestMessage{}
	case MsgIDDownloadResponse:
		body = &DownloadResponseMessage{}
	// AUTO-FIX-2026-06-27: 注册 0x9207 录像下载控制
	case MsgIDDownloadControl:
		body = &DownloadControlMessage{}
	case MsgIDRTPData:
		body = &RTPDataMessage{}
	case MsgIDAlarmVideoRequest:
		body = &AlarmVideoRequestMessage{}
	case MsgIDAlarmVideoResponse:
		body = &AlarmVideoResponseMessage{}
	// AUTO-FIX-2026-06-27: 注册 0x9403/0x9404 文件上传请求/应答
	case MsgIDFileUploadRequest:
		body = &FileUploadRequestMessage{}
	case MsgIDFileUploadResponse:
		body = &FileUploadResponseMessage{}
	case MsgIDPTZControl:
		body = &PTZControlMessage{}
	// AUTO-FIX-2026-06-27: 注册 0x9302 PTZ 控制应答
	case MsgIDPTZControlAck:
		body = &PTZControlAckMessage{}
	case MsgIDAVParamSet:
		body = &AVParamSetMessage{}
	case MsgIDAVParamQuery:
		body = &AVParamQueryMessage{}
	case MsgIDAVParamResponse:
		body = &AVParamResponseMessage{}
	// AUTO-FIX-2026-06-28: 注册 0x9504 删除音视频参数
	case MsgIDAVParamDel:
		body = &AVParamDelMessage{}
	case MsgIDTerminalLogReq:
		body = &TerminalLogRequestMessage{}
	case MsgIDTerminalLogResp:
		body = &TerminalLogResponseMessage{}
	case MsgIDTerminalLogUpload:
		body = &TerminalLogUploadMessage{}
	// AUTO-FIX-2026-06-26: 实现0x1A00/0x1A01/0x1B00/0x1B01平台间消息专用结构体，替换RawMessage占位
	case MsgIDAVNegotiate:
		body = &PlatformNegotiateMessage{}
	case MsgIDAVNegotiateResp:
		body = &PlatformNegotiateResponse{}
	case MsgIDAVForward:
		body = &PlatformForwardMessage{}
	case MsgIDAVForwardResp:
		body = &PlatformForwardResponse{}
	default:
		body = &RawMessage{ID: msgID, Data: data}
	}

	if err := body.Unmarshal(data); err != nil {
		return nil, fmt.Errorf("unmarshal 1078 msg 0x%04X: %w", msgID, err)
	}

	return body, nil
}

func (c *JT1078Codec) EncodeBody(body protocol.MessageBody) ([]byte, error) {
	return body.Marshal()
}

func (c *JT1078Codec) VerifyChecksum(data []byte) bool {
	if len(data) < 2 {
		return false
	}
	var xor byte
	for i := 0; i < len(data)-1; i++ {
		xor ^= data[i]
	}
	return xor == data[len(data)-1]
}

type RawMessage struct {
	ID   uint16
	Data []byte
}

func (m *RawMessage) MsgID() uint16            { return m.ID }
func (m *RawMessage) Marshal() ([]byte, error) { return m.Data, nil }
func (m *RawMessage) Unmarshal(data []byte) error {
	m.Data = make([]byte, len(data))
	copy(m.Data, data)
	return nil
}

// AUTO-FIX-2026-06-26: 0x9101 实时音视频传输请求补全 IP地址(16B ASCII) + 端口(2B) 字段。
// 标准消息体: IP地址(16B,不足补0x00) + 端口(2B) + 逻辑通道号(1B) + 音视频资源类型(1B) + 码流类型(1B) = 21B
// 原实现仅含末尾3字节，缺失前置的IP与Port，导致平台无法告知终端流媒体地址。
//
// AUTO-FIX-2026-06-29 [P1-7]: 新增 TransportMode 字段（0=UDP, 1=TCP）。
// JT/T 1078-2016 标准 0x9101 消息体为 21 字节，不含传输协议标识位。
// 此处作为非标准扩展：TransportMode > 0 时 Marshal 追加 1 字节（共 22B），
// 标准 21B 报文仍兼容（Unmarshal 在 len<22 时默认 UDP）。
//
// AUTO-FIX-2026-06-30 [P2-8]: 新增 SRTP 加密参数字段。
// 启用后平台在 0x9101 中下发 SRTP 配置（加密套件 + 主密钥），终端使用该密钥
// 对 RTP 流进行 SRTP 加密。MasterKey 由平台 GenerateSRTPMasterKey() 生成，
// 仅存在内存与本次 0x9101 报文中；会话结束销毁。
// AUTO-FIX-2026-07-02 [P2-1.3.2]: 新增 MasterKeyEncrypted 标志位。
// 当 MasterKeyEncrypted=true 时，MasterKey 字段携带的是 RSA-OAEP 加密后的密文，
// 终端需用自身 RSA 私钥解密获得真实主密钥；false 时为明文（向后兼容，仅用于测试）。
// 序列化格式（追加在 TransportMode 之后）：
//   SRTPEnabled(1B) + MasterKeyEncrypted(1B) + CipherSuiteLen(1B) + CipherSuite(NB) + MasterKeyLen(1B) + MasterKey(MB)
// 国密场景 CipherSuite="SM4-CBC"，密钥交换可叠加 SM2 加密 MasterKey（此处简化为明文下发，
// 实际部署应走带外或 SM2 加密通道）。
type RealtimeRequestMessage struct {
	IPAddress     string
	Port          uint16
	LogicChannel  byte
	MediaType     byte
	StreamType    byte
	TransportMode byte // 0=UDP(默认), 1=TCP；非标准扩展字段，仅 TransportMode>0 时序列化
	// P2-8 SRTP 加密参数（可选，SRTPEnabled=true 时生效）
	SRTPEnabled       bool
	MasterKeyEncrypted bool   // P2-1.3.2: true=MasterKey 已 RSA 加密, false=明文(向后兼容)
	CipherSuite       string // "AES-128-CM" 或 "SM4-CBC"
	MasterKey         []byte // 16 字节主密钥（明文或 RSA-OAEP 加密后）
}

func (m *RealtimeRequestMessage) MsgID() uint16 { return MsgIDRealtimeRequest }

func (m *RealtimeRequestMessage) Marshal() ([]byte, error) {
	// [P1-6] IP 地址格式校验：非空时必须为合法 IPv4
	if m.IPAddress != "" {
		if ip := net.ParseIP(m.IPAddress); ip == nil || ip.To4() == nil {
			return nil, fmt.Errorf("invalid IPv4 address: %q", m.IPAddress)
		}
	}
	buf := make([]byte, 0, 22)
	ipBuf := make([]byte, 16)
	copy(ipBuf, []byte(m.IPAddress))
	buf = append(buf, ipBuf...)
	portBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(portBuf, m.Port)
	buf = append(buf, portBuf...)
	buf = append(buf, m.LogicChannel, m.MediaType, m.StreamType)
	// TransportMode > 0 时追加传输协议标识位（非标准扩展），
	// TransportMode == 0 时保持 21B 标准格式，确保对标准终端向后兼容。
	if m.TransportMode > 0 {
		buf = append(buf, m.TransportMode)
	}
	// AUTO-FIX-2026-06-30 [P2-8]: SRTP 参数（可选，SRTPEnabled 时追加）。
	// AUTO-FIX-2026-07-02 [P2-1.3.2]: 新增 MasterKeyEncrypted 标志位。
	// FIXED-2026-07-22 [P0]: SRTP 要求 TransportMode > 0，移除 TransportMode=0 占位逻辑。
	// 原 TransportMode=0 时追加 0 字节占位，但 Unmarshal 在 TransportMode=0 时不解析 SRTP，
	// 导致 SRTP 参数无法被正确接收。现要求 SRTP 场景必须 TransportMode > 0。
	if m.SRTPEnabled {
		if m.TransportMode == 0 {
			return nil, fmt.Errorf("srtp requires TransportMode > 0 (UDP mode does not support SRTP)")
		}
		if len(m.MasterKey) > 255 || len(m.CipherSuite) > 255 {
			return nil, fmt.Errorf("srtp param too long")
		}
		buf = append(buf, 1) // SRTPEnabled = true
		// P2-1.3.2: MasterKeyEncrypted 标志位 (1=加密, 0=明文)
		if m.MasterKeyEncrypted {
			buf = append(buf, 1)
		} else {
			buf = append(buf, 0)
		}
		buf = append(buf, byte(len(m.CipherSuite)))
		buf = append(buf, []byte(m.CipherSuite)...)
		buf = append(buf, byte(len(m.MasterKey)))
		buf = append(buf, m.MasterKey...)
	}
	return buf, nil
}

func (m *RealtimeRequestMessage) Unmarshal(data []byte) error {
	if len(data) < 21 {
		return fmt.Errorf("realtime request too short")
	}
	m.IPAddress = string(bytes.TrimRight(data[0:16], "\x00"))
	m.Port = binary.BigEndian.Uint16(data[16:18])
	m.LogicChannel = data[18]
	m.MediaType = data[19]
	m.StreamType = data[20]
	// AUTO-FIX-2026-06-29 [P1-7]: 解析可选的 TCP/UDP 标识位（第 22 字节）。
	// 标准 21B 报文无此字段，默认 UDP(0)；22B 扩展报文读取 data[21]。
	m.TransportMode = 0
	if len(data) >= 22 {
		m.TransportMode = data[21]
	}
	// AUTO-FIX-2026-06-30 [P2-8]: 解析可选的 SRTP 参数（紧跟 TransportMode 之后）。
	// AUTO-FIX-2026-07-02 [P2-1.3.2]: 新增 MasterKeyEncrypted 标志位解析。
	// FIXED-2026-07-22 [P0]: 移除 srtpStart=21 回退逻辑。
	// 原 TransportMode==0 时 srtpStart 回退到 21，与 TransportMode 字段位置冲突，
	// 导致标准 22B 报文被误判为 SRTP 启用（data[21]=TransportMode 被当作 SRTPEnabled）。
	// 修复：SRTP 解析仅在 TransportMode > 0 且 len(data) > 22 时触发。
	// 标准 21B 报文不含 SRTP，TransportMode==0（UDP）不支持 SRTP。
	m.SRTPEnabled = false
	m.MasterKeyEncrypted = false
	srtpStart := 22
	// 仅当 TransportMode > 0（TCP 模式扩展）且数据超过 22B 时才尝试解析 SRTP
	if m.TransportMode > 0 && len(data) > srtpStart && data[srtpStart] == 1 {
		// 检查是否有 MasterKeyEncrypted 字段（新格式至少 4B: enabled + encrypted + csLen + ...）
		if len(data) < srtpStart+3 {
			return fmt.Errorf("srtp cipher suite length missing")
		}
		// P2-1.3.2: 第二字节为 MasterKeyEncrypted 标志
		m.SRTPEnabled = true
		m.MasterKeyEncrypted = data[srtpStart+1] == 1
		csLen := int(data[srtpStart+2])
		if len(data) < srtpStart+3+csLen+1 {
			return fmt.Errorf("srtp params truncated")
		}
		m.CipherSuite = string(data[srtpStart+3 : srtpStart+3+csLen])
		mkLen := int(data[srtpStart+3+csLen])
		if len(data) < srtpStart+3+csLen+1+mkLen {
			return fmt.Errorf("srtp master key truncated")
		}
		m.MasterKey = make([]byte, mkLen)
		copy(m.MasterKey, data[srtpStart+3+csLen+1:srtpStart+3+csLen+1+mkLen])
	}
	return nil
}

type RealtimeResponseMessage struct {
	SeqNum       uint16
	LogicChannel byte
	Result       byte
}

func (m *RealtimeResponseMessage) MsgID() uint16 { return MsgIDRealtimeResponse }

func (m *RealtimeResponseMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint16(buf[0:2], m.SeqNum)
	buf[2] = m.LogicChannel
	buf[3] = m.Result
	return buf, nil
}

func (m *RealtimeResponseMessage) Unmarshal(data []byte) error {
	if len(data) < 4 {
		return fmt.Errorf("realtime response too short")
	}
	m.SeqNum = binary.BigEndian.Uint16(data[0:2])
	m.LogicChannel = data[2]
	m.Result = data[3]
	return nil
}

// AUTO-FIX-2026-06-26: 终端主动发起实时音视频传输请求 0x9103（终端→平台）
// 消息体与 0x9101 一致：逻辑通道号 + 音视频资源类型 + 实时流标记
type TermAVRequestMessage struct {
	LogicChannel byte
	MediaType    byte
	StreamType   byte
}

func (m *TermAVRequestMessage) MsgID() uint16 { return MsgIDTermAVRequest }

func (m *TermAVRequestMessage) Marshal() ([]byte, error) {
	return []byte{m.LogicChannel, m.MediaType, m.StreamType}, nil
}

func (m *TermAVRequestMessage) Unmarshal(data []byte) error {
	if len(data) < 3 {
		return fmt.Errorf("terminal av request too short")
	}
	m.LogicChannel = data[0]
	m.MediaType = data[1]
	m.StreamType = data[2]
	return nil
}

// AUTO-FIX-2026-06-26: 平台应答终端实时音视频传输 0x9104（平台→终端）
// 消息体与 0x9102 一致：流水号 + 逻辑通道号 + 应答结果
type TermAVResponseMessage struct {
	SeqNum       uint16
	LogicChannel byte
	Result       byte
}

func (m *TermAVResponseMessage) MsgID() uint16 { return MsgIDTermAVResponse }

func (m *TermAVResponseMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint16(buf[0:2], m.SeqNum)
	buf[2] = m.LogicChannel
	buf[3] = m.Result
	return buf, nil
}

func (m *TermAVResponseMessage) Unmarshal(data []byte) error {
	if len(data) < 4 {
		return fmt.Errorf("terminal av response too short")
	}
	m.SeqNum = binary.BigEndian.Uint16(data[0:2])
	m.LogicChannel = data[2]
	m.Result = data[3]
	return nil
}

// FIXED-2026-07-17 [P0]: 0x9105 实时音视频传输状态通知（终端→平台）
// JT/T 1078-2016 标准: 流水号(2B) + 逻辑通道号(1B) + 丢失包数(2B) + 乱序包数(2B) +
//   RTP丢包率(2B uint16, 0-1000 表示 0.00%-100.00%) + 当前码率(4B uint32, bps) + 终端异常状态(2B) = 15B
// 终端在实时音视频传输过程中周期性上报传输质量状态，平台据此监控视频流健康度。
type AVStatusNotificationMessage struct {
	SeqNum         uint16 // 流水号（与 0x9101 请求中的流水号对应）
	LogicChannel   byte   // 逻辑通道号
	LostPackets    uint16 // 丢失包数
	DisorderPackets uint16 // 乱序包数
	LossRate       uint16 // RTP丢包率（0-1000，实际值 = 值 / 10.0，如 50 = 5.0%）
	CurrentBitrate uint32 // 当前码率（bps）
	TerminalStatus uint16 // 终端异常状态（位标志：bit0=视频丢帧 bit1=音频断续 bit2=网络抖动 等）
}

func (m *AVStatusNotificationMessage) MsgID() uint16 { return MsgIDAVStatusNotification }

func (m *AVStatusNotificationMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 15)
	binary.BigEndian.PutUint16(buf[0:2], m.SeqNum)
	buf[2] = m.LogicChannel
	binary.BigEndian.PutUint16(buf[3:5], m.LostPackets)
	binary.BigEndian.PutUint16(buf[5:7], m.DisorderPackets)
	binary.BigEndian.PutUint16(buf[7:9], m.LossRate)
	binary.BigEndian.PutUint32(buf[9:13], m.CurrentBitrate)
	binary.BigEndian.PutUint16(buf[13:15], m.TerminalStatus)
	return buf, nil
}

func (m *AVStatusNotificationMessage) Unmarshal(data []byte) error {
	if len(data) < 15 {
		return fmt.Errorf("av status notification too short: %d, need 15", len(data))
	}
	m.SeqNum = binary.BigEndian.Uint16(data[0:2])
	m.LogicChannel = data[2]
	m.LostPackets = binary.BigEndian.Uint16(data[3:5])
	m.DisorderPackets = binary.BigEndian.Uint16(data[5:7])
	m.LossRate = binary.BigEndian.Uint16(data[7:9])
	m.CurrentBitrate = binary.BigEndian.Uint32(data[9:13])
	m.TerminalStatus = binary.BigEndian.Uint16(data[13:15])
	return nil
}

// FIXED-2026-07-17 [P0]: 0x9106 实时音视频传输状态通知应答（平台→终端）
// JT/T 1078-2016 标准: 流水号(2B) + 逻辑通道号(1B) = 3B
type AVStatusNotificationResponseMessage struct {
	SeqNum       uint16 // 流水号（与 0x9105 通知中的流水号对应）
	LogicChannel byte   // 逻辑通道号
}

func (m *AVStatusNotificationResponseMessage) MsgID() uint16 { return MsgIDAVStatusNotificationResponse }

func (m *AVStatusNotificationResponseMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 3)
	binary.BigEndian.PutUint16(buf[0:2], m.SeqNum)
	buf[2] = m.LogicChannel
	return buf, nil
}

func (m *AVStatusNotificationResponseMessage) Unmarshal(data []byte) error {
	if len(data) < 3 {
		return fmt.Errorf("av status notification response too short: %d, need 3", len(data))
	}
	m.SeqNum = binary.BigEndian.Uint16(data[0:2])
	m.LogicChannel = data[2]
	return nil
}

// AUTO-FIX-2026-06-26: 修复0x9201历史音视频检索请求。
// 标准字段顺序: 逻辑通道号(1B) + 开始时间(BCD6) + 结束时间(BCD6) + 主子码流(1B) + 媒体类型(1B) + 回放方式(1B) + 速度(1B) = 17B
// 修复点:
//  1. 调整结构体字段顺序为标准顺序(原为 LogicChannel/MediaType/StreamType/PlaybackMode 在前, 时间在后)
//  2. 添加 Speed byte 字段(原缺失)
//  3. Marshal 时间改用 stringToBCD (原为 []byte 直接写ASCII，与BCD标准不符)
//  4. Unmarshal 时间改用 bcdToString (原为 string，会读取ASCII而非BCD)
//  5. 最小长度改为16(任务指定)；Speed 在 data[16]，仅当 len>=17 时读取以避免越界panic。
//     注: 字段总和为17B(1+6+6+1+1+1+1)，任务中"16字节"为字段和笔误，此处min=16兼容旧版16B报文。
// AUTO-FIX-2026-06-27: 0x9201 增加 StorageType（存储器类型 1B），最小长度 16→18。
// 字段顺序: 逻辑通道号(1B) + 开始时间(BCD6) + 结束时间(BCD6) + 主子码流(1B) + 媒体类型(1B) +
//           回放方式(1B) + 速度(1B) + 存储器类型(1B) = 18B
type PlaybackRequestMessage struct {
	LogicChannel byte
	StartTime    string
	EndTime      string
	StreamType   byte
	MediaType    byte
	PlaybackMode byte
	Speed        byte
	StorageType  byte
}

func (m *PlaybackRequestMessage) MsgID() uint16 { return MsgIDPlaybackRequest }

func (m *PlaybackRequestMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 18)
	buf = append(buf, m.LogicChannel)
	startBCD, err := stringToBCD6(m.StartTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, startBCD...)
	endBCD, err := stringToBCD6(m.EndTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, endBCD...)
	buf = append(buf, m.StreamType, m.MediaType, m.PlaybackMode, m.Speed, m.StorageType)
	return buf, nil
}

func (m *PlaybackRequestMessage) Unmarshal(data []byte) error {
	if len(data) < 18 {
		return fmt.Errorf("playback request too short")
	}
	m.LogicChannel = data[0]
	m.StartTime = bcdToStringSafe(data[1:7])
	m.EndTime = bcdToStringSafe(data[7:13])
	m.StreamType = data[13]
	m.MediaType = data[14]
	m.PlaybackMode = data[15]
	m.Speed = data[16]
	m.StorageType = data[17]
	return nil
}

// bcdToString 将 BCD 字节转换为字符串，不剥除前导零。
// AUTO-FIX-2026-06-29 [P0]: 原 bcdToString 剥除前导零，导致 SIM/Phone（6字节 BCD = 12 位
// 定长数字，常以 0 开头如 013800000000）解码后丢失前导零（→ 13800000000），引发：
//   1) RTP 会话 streamID 不匹配，视频流被丢弃（视频不通）
//   2) byPhone 索引与 API 传入的完整 SIM 不一致，指令下发失败
//   3) DB 存储的 SIM 与协议解码不一致，跨系统对账错误
// [P2-修复] bcdToStringFixed 已与 bcdToString 实现相同，已合并。
// FIXED-2026-07-23 [P2]: bcdToString 添加 BCD 合法性校验，高位或低位 > 9 时返回 error。
// 字符串始终完整返回（best-effort），error 仅表示存在非法 BCD 字节。
func bcdToString(bcd []byte) (string, error) {
	result := make([]byte, 0, len(bcd)*2)
	var firstErr error
	for i, b := range bcd {
		high := b >> 4
		low := b & 0x0F
		if high > 9 || low > 9 {
			if firstErr == nil {
				firstErr = fmt.Errorf("bcdToString: invalid BCD byte 0x%02X at position %d", b, i)
			}
		}
		result = append(result, high+'0', low+'0')
	}
	return string(result), firstErr
}

// bcdToStringSafe 调用 bcdToString 并忽略 error，返回 best-effort 字符串。
// 用于 Unmarshal/ParseHeader 等 BCD 校验错误不应中断主流程的场景。
func bcdToStringSafe(bcd []byte) string {
	s, _ := bcdToString(bcd)
	return s
}


// [P1-修复] stringToBCD 添加输入校验：先过滤非数字字符，再验证
// FIXED-2026-07-23 [P2]: stringToBCD 增加 targetLen 参数，支持不同长度的 BCD 字段。
func stringToBCD(s string, targetLen int) ([]byte, error) {
	if targetLen <= 0 {
		targetLen = 6
	}
	filtered := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			filtered = append(filtered, s[i])
		}
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("stringToBCD: no digit characters in input %q", s)
	}
	s = string(filtered)
	targetChars := targetLen * 2
	for len(s) < targetChars {
		s = "0" + s
	}
	if len(s) > targetChars {
		s = s[len(s)-targetChars:]
	}
	bcd := make([]byte, targetLen)
	for i := 0; i < targetLen; i++ {
		high := s[i*2] - '0'
		low := s[i*2+1] - '0'
		bcd[i] = (high << 4) | low
	}
	return bcd, nil
}

// stringToBCD6 是 stringToBCD(s, 6) 的包装函数。
func stringToBCD6(s string) ([]byte, error) {
	return stringToBCD(s, 6)
}

var _ protocol.MessageBody = (*RawMessage)(nil)
var _ protocol.MessageBody = (*RealtimeRequestMessage)(nil)
var _ protocol.MessageBody = (*RealtimeResponseMessage)(nil)
// AUTO-FIX-2026-06-28: 补全 TermAVRequestMessage/TermAVResponseMessage 接口断言（原缺失）
var _ protocol.MessageBody = (*TermAVRequestMessage)(nil)
var _ protocol.MessageBody = (*TermAVResponseMessage)(nil)
// AUTO-FIX-2026-06-27: ControlRequestMessage/ControlResponseMessage 已重命名为
// AVStatusNotificationMessage/AVStatusNotificationResponseMessage (FIXED-2026-07-17 [P0])
var _ protocol.MessageBody = (*AVStatusNotificationMessage)(nil)
var _ protocol.MessageBody = (*AVStatusNotificationResponseMessage)(nil)
var _ protocol.MessageBody = (*PlaybackRequestMessage)(nil)
var _ protocol.MessageBody = (*PlaybackControlMessage)(nil)
var _ protocol.MessageBody = (*DownloadRequestMessage)(nil)
var _ protocol.MessageBody = (*DownloadResponseMessage)(nil)
// AUTO-FIX-2026-06-27: 新增 0x9207/0x9302/0x9403/0x9404 消息断言
var _ protocol.MessageBody = (*DownloadControlMessage)(nil)
var _ protocol.MessageBody = (*PTZControlAckMessage)(nil)
var _ protocol.MessageBody = (*FileUploadRequestMessage)(nil)
var _ protocol.MessageBody = (*FileUploadResponseMessage)(nil)
var _ protocol.MessageBody = (*RTPDataMessage)(nil)
var _ protocol.MessageBody = (*AlarmVideoRequestMessage)(nil)
var _ protocol.MessageBody = (*AlarmVideoResponseMessage)(nil)
var _ protocol.MessageBody = (*PTZControlMessage)(nil)
var _ protocol.MessageBody = (*AVParamSetMessage)(nil)
var _ protocol.MessageBody = (*AVParamQueryMessage)(nil)
var _ protocol.MessageBody = (*AVParamResponseMessage)(nil)
// AUTO-FIX-2026-06-28: 新增 0x9504 删除音视频参数消息断言
var _ protocol.MessageBody = (*AVParamDelMessage)(nil)

type PlaybackControlMessage struct {
	LogicChannel byte
	Command      byte
	Speed        byte
}

func (m *PlaybackControlMessage) MsgID() uint16 { return MsgIDPlaybackControl }

func (m *PlaybackControlMessage) Marshal() ([]byte, error) {
	return []byte{m.LogicChannel, m.Command, m.Speed}, nil
}

func (m *PlaybackControlMessage) Unmarshal(data []byte) error {
	if len(data) < 3 {
		return fmt.Errorf("playback control too short")
	}
	m.LogicChannel = data[0]
	m.Command = data[1]
	m.Speed = data[2]
	return nil
}

type RTPDataMessage struct {
	LogicChannel byte
	DataType     byte
	RTPHeader    []byte
	RTPPayload   []byte
}

func (m *RTPDataMessage) MsgID() uint16 { return MsgIDRTPData }

func (m *RTPDataMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 4+len(m.RTPHeader)+len(m.RTPPayload))
	buf = append(buf, m.LogicChannel)
	buf = append(buf, m.DataType)
	buf = append(buf, byte(len(m.RTPHeader)>>8), byte(len(m.RTPHeader)))
	buf = append(buf, m.RTPHeader...)
	buf = append(buf, m.RTPPayload...)
	return buf, nil
}

func (m *RTPDataMessage) Unmarshal(data []byte) error {
	if len(data) < 4 {
		return fmt.Errorf("rtp data too short")
	}
	m.LogicChannel = data[0]
	m.DataType = data[1]
	headerLen := int(data[2])<<8 | int(data[3])
	offset := 4
	if offset+headerLen > len(data) {
		return fmt.Errorf("rtp header truncated")
	}
	m.RTPHeader = make([]byte, headerLen)
	copy(m.RTPHeader, data[offset:offset+headerLen])
	offset += headerLen
	if offset < len(data) {
		m.RTPPayload = make([]byte, len(data)-offset)
		copy(m.RTPPayload, data[offset:])
	}
	return nil
}

type AlarmVideoRequestMessage struct {
	SeqNum         uint16
	LogicChannel   byte
	AlarmFlag      uint32
	AlarmType      uint16
	AlarmTime      string
	AlarmLongitude uint32
	AlarmLatitude  uint32
	AlarmAltitude  uint16
	AlarmSpeed     uint16
	AlarmDirection uint16
	VideoLen       uint16
}

func (m *AlarmVideoRequestMessage) MsgID() uint16 { return MsgIDAlarmVideoRequest }

func (m *AlarmVideoRequestMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 32)
	buf = append(buf, byte(m.SeqNum>>8), byte(m.SeqNum))
	buf = append(buf, m.LogicChannel)
	buf = append(buf, byte(m.AlarmFlag>>24), byte(m.AlarmFlag>>16), byte(m.AlarmFlag>>8), byte(m.AlarmFlag))
	buf = append(buf, byte(m.AlarmType>>8), byte(m.AlarmType))
	timeBCD, err := stringToBCD6(m.AlarmTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, timeBCD...)
	buf = append(buf, byte(m.AlarmLongitude>>24), byte(m.AlarmLongitude>>16), byte(m.AlarmLongitude>>8), byte(m.AlarmLongitude))
	buf = append(buf, byte(m.AlarmLatitude>>24), byte(m.AlarmLatitude>>16), byte(m.AlarmLatitude>>8), byte(m.AlarmLatitude))
	buf = append(buf, byte(m.AlarmAltitude>>8), byte(m.AlarmAltitude))
	buf = append(buf, byte(m.AlarmSpeed>>8), byte(m.AlarmSpeed))
	buf = append(buf, byte(m.AlarmDirection>>8), byte(m.AlarmDirection))
	buf = append(buf, byte(m.VideoLen>>8), byte(m.VideoLen))
	return buf, nil
}

// AUTO-FIX-2026-06-26: 修复边界panic。原 `len(data) < 28` 允许28字节数据，
// 但随后 `data[27:29]` 在28字节缓冲(下标0-27)上切片越界 panic。
// 标准0x9401完整消息长度为31字节(含末尾VideoLen)，将边界检查提升至 <31，
// 并将 VideoLen 读取改为无条件读取(已被长度检查保证)。
func (m *AlarmVideoRequestMessage) Unmarshal(data []byte) error {
	if len(data) < 31 {
		return fmt.Errorf("alarm video request too short")
	}
	m.SeqNum = binary.BigEndian.Uint16(data[0:2])
	m.LogicChannel = data[2]
	m.AlarmFlag = uint32(data[3])<<24 | uint32(data[4])<<16 | uint32(data[5])<<8 | uint32(data[6])
	m.AlarmType = binary.BigEndian.Uint16(data[7:9])
	m.AlarmTime = bcdToStringSafe(data[9:15])
	m.AlarmLongitude = uint32(data[15])<<24 | uint32(data[16])<<16 | uint32(data[17])<<8 | uint32(data[18])
	m.AlarmLatitude = uint32(data[19])<<24 | uint32(data[20])<<16 | uint32(data[21])<<8 | uint32(data[22])
	m.AlarmAltitude = binary.BigEndian.Uint16(data[23:25])
	m.AlarmSpeed = binary.BigEndian.Uint16(data[25:27])
	m.AlarmDirection = binary.BigEndian.Uint16(data[27:29])
	m.VideoLen = binary.BigEndian.Uint16(data[29:31])
	return nil
}

type AlarmVideoResponseMessage struct {
	SeqNum       uint16
	LogicChannel byte
	Result       byte
}

func (m *AlarmVideoResponseMessage) MsgID() uint16 { return MsgIDAlarmVideoResponse }

func (m *AlarmVideoResponseMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint16(buf[0:2], m.SeqNum)
	buf[2] = m.LogicChannel
	buf[3] = m.Result
	return buf, nil
}

func (m *AlarmVideoResponseMessage) Unmarshal(data []byte) error {
	if len(data) < 4 {
		return fmt.Errorf("alarm video response too short")
	}
	m.SeqNum = binary.BigEndian.Uint16(data[0:2])
	m.LogicChannel = data[2]
	m.Result = data[3]
	return nil
}

// AUTO-FIX-2026-06-27: 0x9301 PTZ 改为 5B（原 3B Channel+Direction+Speed 不符合标准）
// 标准 5B: 逻辑通道号(1B) + 控制指令(4B)
// 控制指令 4B = 字节1(光圈/聚焦/变倍位) + 字节2(方向位) + 字节3(水平速度) + 字节4(垂直速度)
type PTZControlMessage struct {
	LogicChannel       byte
	ControlInstruction [4]byte
}

func (m *PTZControlMessage) MsgID() uint16 { return MsgIDPTZControl }

func (m *PTZControlMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 5)
	buf[0] = m.LogicChannel
	buf[1] = m.ControlInstruction[0]
	buf[2] = m.ControlInstruction[1]
	buf[3] = m.ControlInstruction[2]
	buf[4] = m.ControlInstruction[3]
	return buf, nil
}

func (m *PTZControlMessage) Unmarshal(data []byte) error {
	if len(data) < 5 {
		return fmt.Errorf("ptz control too short")
	}
	m.LogicChannel = data[0]
	m.ControlInstruction[0] = data[1]
	m.ControlInstruction[1] = data[2]
	m.ControlInstruction[2] = data[3]
	m.ControlInstruction[3] = data[4]
	return nil
}

// AUTO-FIX-2026-06-27: BuildPTZControlInstruction 由方向/速度参数构造 4B 控制指令。
// byte1 位: PTZLight/PTZWiper/PTZZoomOut/PTZZoomIn/PTZFocusNear/PTZFocusFar/PTZIrisClose/PTZIrisOpen
// byte2 位: PTZDirDown/PTZDirUp/PTZDirLeft/PTZDirRight
// byte3=水平速度 byte4=垂直速度
func BuildPTZControlInstruction(byte1Flags, byte2Flags, hSpeed, vSpeed byte) [4]byte {
	return [4]byte{byte1Flags, byte2Flags, hSpeed, vSpeed}
}

// AUTO-FIX-2026-06-27: 0x9501 改为变长列表（原固定 9B 平铺结构不符合 JT/T 1078-2016 标准）
// 标准: 逻辑通道号(1B) +
//       音频参数个数(1B) + N×{音频类型(1B)+采样位(1B)+采样率(1B)} +
//       视频参数个数(1B) + M×{视频类型(1B)+分辨率(1B)+帧率(1B)+码率(2B uint16)}
type AVParamSetMessage struct {
	LogicChannel byte
	AudioParams  []AVAudioParam
	VideoParams  []AVVideoParam
}

// AVAudioParam 音频参数项（3B）
type AVAudioParam struct {
	AudioType   byte
	AudioBit    byte
	AudioSample byte
}

// AVVideoParam 视频参数项（5B）
type AVVideoParam struct {
	VideoType   byte
	Resolution  byte
	FrameRate   byte
	BitRate     uint16
}

func (m *AVParamSetMessage) MsgID() uint16 { return MsgIDAVParamSet }

func (m *AVParamSetMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 2+len(m.AudioParams)*3+len(m.VideoParams)*5)
	buf = append(buf, m.LogicChannel)
	buf = append(buf, byte(len(m.AudioParams)))
	for _, a := range m.AudioParams {
		buf = append(buf, a.AudioType, a.AudioBit, a.AudioSample)
	}
	buf = append(buf, byte(len(m.VideoParams)))
	for _, v := range m.VideoParams {
		buf = append(buf, v.VideoType, v.Resolution, v.FrameRate)
		var brBytes [2]byte
		binary.BigEndian.PutUint16(brBytes[:], v.BitRate)
		buf = append(buf, brBytes[:]...)
	}
	return buf, nil
}

func (m *AVParamSetMessage) Unmarshal(data []byte) error {
	if len(data) < 2 {
		return fmt.Errorf("av param set too short")
	}
	m.LogicChannel = data[0]
	audioCount := int(data[1])
	offset := 2
	if offset+audioCount*3 > len(data) {
		return fmt.Errorf("av param set audio list out of bounds")
	}
	m.AudioParams = make([]AVAudioParam, 0, audioCount)
	for i := 0; i < audioCount; i++ {
		var a AVAudioParam
		a.AudioType = data[offset]
		a.AudioBit = data[offset+1]
		a.AudioSample = data[offset+2]
		m.AudioParams = append(m.AudioParams, a)
		offset += 3
	}
	if offset >= len(data) {
		return fmt.Errorf("av param set missing video count")
	}
	videoCount := int(data[offset])
	offset++
	if offset+videoCount*5 > len(data) {
		return fmt.Errorf("av param set video list out of bounds")
	}
	m.VideoParams = make([]AVVideoParam, 0, videoCount)
	for i := 0; i < videoCount; i++ {
		var v AVVideoParam
		v.VideoType = data[offset]
		v.Resolution = data[offset+1]
		v.FrameRate = data[offset+2]
		v.BitRate = binary.BigEndian.Uint16(data[offset+3 : offset+5])
		m.VideoParams = append(m.VideoParams, v)
		offset += 5
	}
	return nil
}

type AVParamQueryMessage struct {
	LogicChannel byte
}

func (m *AVParamQueryMessage) MsgID() uint16 { return MsgIDAVParamQuery }

func (m *AVParamQueryMessage) Marshal() ([]byte, error) {
	return []byte{m.LogicChannel}, nil
}

func (m *AVParamQueryMessage) Unmarshal(data []byte) error {
	if len(data) < 1 {
		return fmt.Errorf("av param query too short")
	}
	m.LogicChannel = data[0]
	return nil
}

type AVParamResponseMessage struct {
	LogicChannel byte
	AudioType    byte
	AudioBit     byte
	AudioSample  byte
	VideoType    byte
	Resolution   byte
	FrameRate    byte
	BitRate      uint16
}

func (m *AVParamResponseMessage) MsgID() uint16 { return MsgIDAVParamResponse }

func (m *AVParamResponseMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 9)
	buf[0] = m.LogicChannel
	buf[1] = m.AudioType
	buf[2] = m.AudioBit
	buf[3] = m.AudioSample
	buf[4] = m.VideoType
	buf[5] = m.Resolution
	buf[6] = m.FrameRate
	binary.BigEndian.PutUint16(buf[7:9], m.BitRate)
	return buf, nil
}

func (m *AVParamResponseMessage) Unmarshal(data []byte) error {
	if len(data) < 9 {
		return fmt.Errorf("av param response too short")
	}
	m.LogicChannel = data[0]
	m.AudioType = data[1]
	m.AudioBit = data[2]
	m.AudioSample = data[3]
	m.VideoType = data[4]
	m.Resolution = data[5]
	m.FrameRate = data[6]
	m.BitRate = binary.BigEndian.Uint16(data[7:9])
	return nil
}

// AUTO-FIX-2026-06-28: 0x9504 删除音视频参数（平台→终端）
// 参考 0x9501 AVParamSetMessage 的结构，删除消息更简洁：仅指定要删除的通道及音视频/码流类型。
// 消息体: 逻辑通道号(1B) + 音视频类型(1B) + 码流类型(1B) = 3B
// 注: Phone 由帧头携带（1078 所有终端消息均如此），不放入消息体。
type AVParamDelMessage struct {
	LogicChannel byte
	AVType       byte
	StreamType   byte
}

func (m *AVParamDelMessage) MsgID() uint16 { return MsgIDAVParamDel }

func (m *AVParamDelMessage) Marshal() ([]byte, error) {
	return []byte{m.LogicChannel, m.AVType, m.StreamType}, nil
}

func (m *AVParamDelMessage) Unmarshal(data []byte) error {
	if len(data) < 3 {
		return fmt.Errorf("av param del too short")
	}
	m.LogicChannel = data[0]
	m.AVType = data[1]
	m.StreamType = data[2]
	return nil
}

// DownloadRequestMessage 0x9205 录像下载请求（平台→终端）
// AUTO-FIX-2026-06-27: 字段重整
//  - AlarmFlag uint64(8B) → uint32(4B)
//  - 新增 UdpPort uint16(2B) 字段
//  - Username/Password 改用 GBK 编码（simplifiedchinese.GBK）
//  - 字段顺序: Channel+Start+End+AlarmFlag(uint32)+MediaType+StreamType+StorageType+
//             DownloadType+IP16+TcpPort+UdpPort+UserGBK(12B)+PassGBK(12B)+FilePath
//  - 最小长度 67→65
type DownloadRequestMessage struct {
	LogicChannel byte
	StartTime    string // BCD YYMMDDHHMMSS
	EndTime      string // BCD YYMMDDHHMMSS
	AlarmFlag    uint32
	MediaType    byte
	StreamType   byte
	StorageType  byte
	DownloadType byte
	IPAddress    string // 16B ASCII
	TcpPort      uint16
	UdpPort      uint16
	Username     string // GBK 编码，12B
	Password     string // GBK 编码，12B
	FilePath     string // 变长
}

func (m *DownloadRequestMessage) MsgID() uint16 { return MsgIDDownloadRequest }

func (m *DownloadRequestMessage) Marshal() ([]byte, error) {
	// [P2-3] 校验 IP 地址格式：非空时必须为合法 IPv4
	if m.IPAddress != "" {
		if ip := net.ParseIP(m.IPAddress); ip == nil || ip.To4() == nil {
			return nil, fmt.Errorf("invalid IPv4 address: %q", m.IPAddress)
		}
	}
	buf := make([]byte, 0, 65+len(m.FilePath))
	buf = append(buf, m.LogicChannel)
	startBCD, err := stringToBCD6(m.StartTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, startBCD...)
	endBCD, err := stringToBCD6(m.EndTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, endBCD...)
	buf = append(buf, byte(m.AlarmFlag>>24), byte(m.AlarmFlag>>16), byte(m.AlarmFlag>>8), byte(m.AlarmFlag))
	buf = append(buf, m.MediaType, m.StreamType, m.StorageType, m.DownloadType)
	// IP 16B ASCII
	ipBuf := make([]byte, 16)
	copy(ipBuf, []byte(m.IPAddress))
	buf = append(buf, ipBuf...)
	buf = append(buf, byte(m.TcpPort>>8), byte(m.TcpPort))
	buf = append(buf, byte(m.UdpPort>>8), byte(m.UdpPort))
	// Username/Password 12B GBK
	userBuf, err := encodeGBKFixed(m.Username, 12)
	if err != nil {
		return nil, fmt.Errorf("encode username gbk: %w", err)
	}
	buf = append(buf, userBuf...)
	passBuf, err := encodeGBKFixed(m.Password, 12)
	if err != nil {
		return nil, fmt.Errorf("encode password gbk: %w", err)
	}
	buf = append(buf, passBuf...)
	buf = append(buf, m.FilePath...)
	return buf, nil
}

func (m *DownloadRequestMessage) Unmarshal(data []byte) error {
	if len(data) < 65 {
		return fmt.Errorf("download request too short")
	}
	m.LogicChannel = data[0]
	m.StartTime = bcdToStringSafe(data[1:7])
	m.EndTime = bcdToStringSafe(data[7:13])
	m.AlarmFlag = binary.BigEndian.Uint32(data[13:17])
	m.MediaType = data[17]
	m.StreamType = data[18]
	m.StorageType = data[19]
	m.DownloadType = data[20]
	m.IPAddress = string(bytes.TrimRight(data[21:37], "\x00"))
	m.TcpPort = binary.BigEndian.Uint16(data[37:39])
	m.UdpPort = binary.BigEndian.Uint16(data[39:41])
	var err error
	if m.Username, err = decodeGBKFixed(data[41:53]); err != nil {
		return fmt.Errorf("decode username gbk: %w", err)
	}
	if m.Password, err = decodeGBKFixed(data[53:65]); err != nil {
		return fmt.Errorf("decode password gbk: %w", err)
	}
	if len(data) > 65 {
		m.FilePath = string(data[65:])
	}
	return nil
}

// AUTO-FIX-2026-06-27: encodeGBKFixed 将字符串以 GBK 编码并填充/截断到固定长度。
func encodeGBKFixed(s string, size int) ([]byte, error) {
	enc := simplifiedchinese.GBK.NewEncoder()
	encoded, err := enc.Bytes([]byte(s))
	if err != nil {
		return nil, err
	}
	buf := make([]byte, size)
	if len(encoded) > size {
		// [P1-2] 截断时检查截断点是否在 GBK 双字节字符中间。
		// GBK lead byte 范围: 0x81-0xFE，若截断点前一字节是 lead byte，
		// 则回退 1 字节截断（少 1 字节而非半字符），用 0x00 填充末尾。
		truncateAt := size
		if truncateAt > 0 && isGBKLeadByte(encoded[truncateAt-1]) {
			truncateAt--
		}
		copy(buf, encoded[:truncateAt])
		// 截断回退后剩余字节用 0x00 填充（buf 已初始化为零值，无需额外操作）
	} else {
		copy(buf, encoded)
	}
	return buf, nil
}

// isGBKLeadByte 判断字节是否为 GBK 双字节字符的首字节（lead byte）。
// GBK lead byte 范围: 0x81-0xFE
func isGBKLeadByte(b byte) bool {
	return b >= 0x81 && b <= 0xFE
}

// AUTO-FIX-2026-06-27: decodeGBKFixed 将固定长度 GBK 字节流解码为字符串，并 TrimRight \x00。
func decodeGBKFixed(data []byte) (string, error) {
	dec := simplifiedchinese.GBK.NewDecoder()
	trimmed := bytes.TrimRight(data, "\x00")
	if len(trimmed) == 0 {
		return "", nil
	}
	decoded, err := dec.Bytes(trimmed)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// DownloadResponseMessage 0x9206 录像下载应答（终端→平台）
type DownloadResponseMessage struct {
	RespSeqNum   uint16
	LogicChannel byte
	Result       byte
}

func (m *DownloadResponseMessage) MsgID() uint16 { return MsgIDDownloadResponse }

func (m *DownloadResponseMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint16(buf[0:2], m.RespSeqNum)
	buf[2] = m.LogicChannel
	buf[3] = m.Result
	return buf, nil
}

func (m *DownloadResponseMessage) Unmarshal(data []byte) error {
	if len(data) < 4 {
		return fmt.Errorf("download response too short")
	}
	m.RespSeqNum = binary.BigEndian.Uint16(data[0:2])
	m.LogicChannel = data[2]
	m.Result = data[3]
	return nil
}

// TerminalLogRequestMessage 0x9601 终端日志检索请求（平台→终端）
type TerminalLogRequestMessage struct {
	LogicChannel byte
	StartTime    string // BCD YYMMDDHHMMSS
	EndTime      string // BCD YYMMDDHHMMSS
}

func (m *TerminalLogRequestMessage) MsgID() uint16 { return MsgIDTerminalLogReq }

func (m *TerminalLogRequestMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 13)
	buf = append(buf, m.LogicChannel)
	startBCD, err := stringToBCD6(m.StartTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, startBCD...)
	endBCD, err := stringToBCD6(m.EndTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, endBCD...)
	return buf, nil
}

func (m *TerminalLogRequestMessage) Unmarshal(data []byte) error {
	if len(data) < 13 {
		return fmt.Errorf("terminal log request too short")
	}
	m.LogicChannel = data[0]
	m.StartTime = bcdToStringSafe(data[1:7])
	m.EndTime = bcdToStringSafe(data[7:13])
	return nil
}

// TerminalLogResponseMessage 0x9602 终端日志检索应答（终端→平台）
type TerminalLogResponseMessage struct {
	RespSeqNum   uint16
	LogicChannel byte
	Result       byte
	LogCount     byte
}

func (m *TerminalLogResponseMessage) MsgID() uint16 { return MsgIDTerminalLogResp }

func (m *TerminalLogResponseMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 5)
	binary.BigEndian.PutUint16(buf[0:2], m.RespSeqNum)
	buf[2] = m.LogicChannel
	buf[3] = m.Result
	buf[4] = m.LogCount
	return buf, nil
}

func (m *TerminalLogResponseMessage) Unmarshal(data []byte) error {
	if len(data) < 5 {
		return fmt.Errorf("terminal log response too short")
	}
	m.RespSeqNum = binary.BigEndian.Uint16(data[0:2])
	m.LogicChannel = data[2]
	m.Result = data[3]
	m.LogCount = data[4]
	return nil
}

// TerminalLogUploadMessage 0x9603 终端日志上传请求（平台→终端）
// AUTO-FIX-2026-06-27: IP 编码统一改为 16B ASCII（与0x9101/0x9205一致），TrimRight \x00；
//                      Username/Password 改用 GBK 编码（simplifiedchinese.GBK）。
type TerminalLogUploadMessage struct {
	LogicChannel byte
	IPAddress    string // 16B ASCII
	Port         uint16
	Username     string // GBK 12B
	Password     string // GBK 12B
	StartTime    string // BCD YYMMDDHHMMSS
	EndTime      string // BCD YYMMDDHHMMSS
}

func (m *TerminalLogUploadMessage) MsgID() uint16 { return MsgIDTerminalLogUpload }

func (m *TerminalLogUploadMessage) Marshal() ([]byte, error) {
	// [P2-3] 校验 IP 地址格式：非空时必须为合法 IPv4
	if m.IPAddress != "" {
		if ip := net.ParseIP(m.IPAddress); ip == nil || ip.To4() == nil {
			return nil, fmt.Errorf("invalid IPv4 address: %q", m.IPAddress)
		}
	}
	buf := make([]byte, 0, 55)
	buf = append(buf, m.LogicChannel)
	// IP 16B ASCII（不足补0x00）
	ipBuf := make([]byte, 16)
	copy(ipBuf, []byte(m.IPAddress))
	buf = append(buf, ipBuf...)
	buf = append(buf, byte(m.Port>>8), byte(m.Port))
	// Username/Password 12B GBK
	userBuf, err := encodeGBKFixed(m.Username, 12)
	if err != nil {
		return nil, fmt.Errorf("encode username gbk: %w", err)
	}
	buf = append(buf, userBuf...)
	passBuf, err := encodeGBKFixed(m.Password, 12)
	if err != nil {
		return nil, fmt.Errorf("encode password gbk: %w", err)
	}
	buf = append(buf, passBuf...)
	startBCD, err := stringToBCD6(m.StartTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, startBCD...)
	endBCD, err := stringToBCD6(m.EndTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, endBCD...)
	return buf, nil
}

func (m *TerminalLogUploadMessage) Unmarshal(data []byte) error {
	if len(data) < 43 {
		return fmt.Errorf("terminal log upload too short")
	}
	m.LogicChannel = data[0]
	m.IPAddress = string(bytes.TrimRight(data[1:17], "\x00"))
	m.Port = binary.BigEndian.Uint16(data[17:19])
	var err error
	if m.Username, err = decodeGBKFixed(data[19:31]); err != nil {
		return fmt.Errorf("decode username gbk: %w", err)
	}
	if m.Password, err = decodeGBKFixed(data[31:43]); err != nil {
		return fmt.Errorf("decode password gbk: %w", err)
	}
	if len(data) >= 49 {
		m.StartTime = bcdToStringSafe(data[43:49])
	}
	if len(data) >= 55 {
		m.EndTime = bcdToStringSafe(data[49:55])
	}
	return nil
}

var _ protocol.MessageBody = (*TerminalLogRequestMessage)(nil)
var _ protocol.MessageBody = (*TerminalLogResponseMessage)(nil)
var _ protocol.MessageBody = (*TerminalLogUploadMessage)(nil)

// AUTO-FIX-2026-06-26: 补充1078协议缺失消息体（2项），按文档第一轮.txt要求实现 [2026-06-26]
// AUTO-FIX-2026-06-27: 0x9202 资源项结构重写为 28B（原 18B 不符合 JT/T 1078-2016 标准）

// PlaybackResponseMessage 0x9202 回放应答（终端→平台）
// 应答流水号(2B) + 逻辑通道号(1B) + 结果(1B)
// 结果=成功时继续：资源总数(2B) + 资源项列表(N × 28B)
type PlaybackResponseMessage struct {
	RespSeqNum   uint16
	LogicChannel byte
	Result       byte
	Items        []PlaybackResourceItem
}

// PlaybackResourceItem 回放资源项（28B）
// AUTO-FIX-2026-06-27: 重写为标准 28B 结构
// 通道号(1B) + 媒体类型(1B) + 码流类型(1B) + 存储器类型(1B) +
// 开始时间BCD(6B) + 结束时间BCD(6B) + 报警标志(4B uint32) + 文件大小(8B uint64) = 28B
type PlaybackResourceItem struct {
	ChannelID   byte
	MediaType   byte
	StreamType  byte
	StorageType byte
	StartTime   string // BCD YYMMDDHHMMSS
	EndTime     string // BCD YYMMDDHHMMSS
	AlarmFlag   uint32
	FileSize    uint64
}

func (m *PlaybackResponseMessage) MsgID() uint16 { return MsgIDPlaybackResponse }

func (m *PlaybackResponseMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 4+len(m.Items)*28)
	buf = append(buf, byte(m.RespSeqNum>>8), byte(m.RespSeqNum))
	buf = append(buf, m.LogicChannel)
	buf = append(buf, m.Result)
	if m.Result != 0 {
		return buf, nil
	}
	buf = append(buf, byte(len(m.Items)>>8), byte(len(m.Items)))
	for _, it := range m.Items {
		buf = append(buf, it.ChannelID, it.MediaType, it.StreamType, it.StorageType)
		itStartBCD, err := stringToBCD6(it.StartTime)
		if err != nil {
			return nil, err
		}
		buf = append(buf, itStartBCD...)
		itEndBCD, err := stringToBCD6(it.EndTime)
		if err != nil {
			return nil, err
		}
		buf = append(buf, itEndBCD...)
		buf = append(buf, byte(it.AlarmFlag>>24), byte(it.AlarmFlag>>16), byte(it.AlarmFlag>>8), byte(it.AlarmFlag))
		var szBytes [8]byte
		binary.BigEndian.PutUint64(szBytes[:], it.FileSize)
		buf = append(buf, szBytes[:]...)
	}
	return buf, nil
}

func (m *PlaybackResponseMessage) Unmarshal(data []byte) error {
	if len(data) < 4 {
		return fmt.Errorf("playback response too short")
	}
	m.RespSeqNum = binary.BigEndian.Uint16(data[0:2])
	m.LogicChannel = data[2]
	m.Result = data[3]
	if m.Result != 0 || len(data) < 6 {
		return nil
	}
	count := int(binary.BigEndian.Uint16(data[4:6]))
	m.Items = make([]PlaybackResourceItem, 0, count)
	offset := 6
	for i := 0; i < count && offset+28 <= len(data); i++ {
		var it PlaybackResourceItem
		it.ChannelID = data[offset]
		it.MediaType = data[offset+1]
		it.StreamType = data[offset+2]
		it.StorageType = data[offset+3]
		it.StartTime = bcdToStringSafe(data[offset+4 : offset+10])
		it.EndTime = bcdToStringSafe(data[offset+10 : offset+16])
		it.AlarmFlag = binary.BigEndian.Uint32(data[offset+16 : offset+20])
		it.FileSize = binary.BigEndian.Uint64(data[offset+20 : offset+28])
		m.Items = append(m.Items, it)
		offset += 28
	}
	return nil
}

// PlaybackControlAckMessage 0x9204 回放控制应答（终端→平台）
// 应答流水号(2B) + 逻辑通道号(1B) + 结果(1B)
type PlaybackControlAckMessage struct {
	RespSeqNum   uint16
	LogicChannel byte
	Result       byte
}

func (m *PlaybackControlAckMessage) MsgID() uint16 { return MsgIDPlaybackControlAck }

func (m *PlaybackControlAckMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint16(buf[0:2], m.RespSeqNum)
	buf[2] = m.LogicChannel
	buf[3] = m.Result
	return buf, nil
}

func (m *PlaybackControlAckMessage) Unmarshal(data []byte) error {
	if len(data) < 4 {
		return fmt.Errorf("playback control ack too short")
	}
	m.RespSeqNum = binary.BigEndian.Uint16(data[0:2])
	m.LogicChannel = data[2]
	m.Result = data[3]
	return nil
}

var _ protocol.MessageBody = (*PlaybackResponseMessage)(nil)
var _ protocol.MessageBody = (*PlaybackControlAckMessage)(nil)

// AUTO-FIX-2026-06-26: 补充1078平台间消息专用结构体（0x1A00/0x1A01/0x1B00/0x1B01），按第一轮.txt要求 [2026-06-26]

// PlatformNegotiateMessage 0x1A00 平台间音视频协商请求
// 手机号(6B BCD) + 逻辑通道号(1B) + 音视频类型(1B) + 码流类型(1B) + 协议类型(1B) + IP地址(变长) + 端口(2B)
type PlatformNegotiateMessage struct {
	Phone        string
	LogicChannel byte
	AVType       byte
	StreamType   byte
	ProtocolType byte
	IPAddress    string
	Port         uint16
}

func (m *PlatformNegotiateMessage) MsgID() uint16 { return MsgIDAVNegotiate }

func (m *PlatformNegotiateMessage) Marshal() ([]byte, error) {
	// [P1-3] IP 地址长度校验：IPv4 最长 15 字符（如 255.255.255.255）
	if len(m.IPAddress) > 15 {
		return nil, fmt.Errorf("IP address too long: %d (max 15)", len(m.IPAddress))
	}
	buf := make([]byte, 0, 12)
	phoneBCD, err := stringToBCD6(m.Phone)
	if err != nil {
		return nil, err
	}
	buf = append(buf, phoneBCD...)
	buf = append(buf, m.LogicChannel, m.AVType, m.StreamType, m.ProtocolType)
	buf = append(buf, []byte(m.IPAddress)...)
	buf = append(buf, byte(m.Port>>8), byte(m.Port))
	return buf, nil
}

func (m *PlatformNegotiateMessage) Unmarshal(data []byte) error {
	if len(data) < 10 {
		return fmt.Errorf("platform negotiate too short")
	}
	m.Phone = bcdToStringSafe(data[0:6])
	m.LogicChannel = data[6]
	m.AVType = data[7]
	m.StreamType = data[8]
	m.ProtocolType = data[9]
	offset := 10
	// IP地址为点分十进制字符串，端口为最后2字节
	// FIXED-2026-07-22 [P1]: len(data)==offset+2 时 Port 未解析。
	// 将条件从 > 改为 >=，确保恰好 IP(0B)+Port(2B) 时 Port 字段被正确解析。
	if len(data) >= offset+2 {
		m.IPAddress = string(data[offset : len(data)-2])
		portBytes := data[len(data)-2:]
		m.Port = binary.BigEndian.Uint16(portBytes)
	}
	return nil
}

// PlatformNegotiateResponse 0x1A01 平台间音视频协商应答
// 手机号(6B BCD) + 逻辑通道号(1B) + 结果(1B) + IP地址(变长) + 端口(2B)
type PlatformNegotiateResponse struct {
	Phone        string
	LogicChannel byte
	Result       byte
	IPAddress    string
	Port         uint16
}

func (m *PlatformNegotiateResponse) MsgID() uint16 { return MsgIDAVNegotiateResp }

func (m *PlatformNegotiateResponse) Marshal() ([]byte, error) {
	// [P1-3] IP 地址长度校验：IPv4 最长 15 字符
	if len(m.IPAddress) > 15 {
		return nil, fmt.Errorf("IP address too long: %d (max 15)", len(m.IPAddress))
	}
	buf := make([]byte, 0, 12)
	phoneBCD, err := stringToBCD6(m.Phone)
	if err != nil {
		return nil, err
	}
	buf = append(buf, phoneBCD...)
	buf = append(buf, m.LogicChannel, m.Result)
	buf = append(buf, []byte(m.IPAddress)...)
	buf = append(buf, byte(m.Port>>8), byte(m.Port))
	return buf, nil
}

func (m *PlatformNegotiateResponse) Unmarshal(data []byte) error {
	if len(data) < 10 {
		return fmt.Errorf("platform negotiate resp too short")
	}
	m.Phone = bcdToStringSafe(data[0:6])
	m.LogicChannel = data[6]
	m.Result = data[7]
	offset := 8
	// FIXED-2026-07-22 [P1]: len(data)==offset+2 时 Port 未解析。
	// 将条件从 > 改为 >=，确保恰好 IP(0B)+Port(2B) 时 Port 字段被正确解析。
	if len(data) >= offset+2 {
		m.IPAddress = string(data[offset : len(data)-2])
		portBytes := data[len(data)-2:]
		m.Port = binary.BigEndian.Uint16(portBytes)
	}
	return nil
}

// PlatformForwardMessage 0x1B00 平台间音视频转发请求
// 手机号(6B BCD) + 逻辑通道号(1B) + 音视频类型(1B) + 码流类型(1B) + 开始时间(6B BCD) + 结束时间(6B BCD)
type PlatformForwardMessage struct {
	Phone        string
	LogicChannel byte
	AVType       byte
	StreamType   byte
	StartTime    string
	EndTime      string
}

func (m *PlatformForwardMessage) MsgID() uint16 { return MsgIDAVForward }

func (m *PlatformForwardMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 22)
	phoneBCD, err := stringToBCD6(m.Phone)
	if err != nil {
		return nil, err
	}
	buf = append(buf, phoneBCD...)
	buf = append(buf, m.LogicChannel, m.AVType, m.StreamType)
	startBCD, err := stringToBCD6(m.StartTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, startBCD...)
	endBCD, err := stringToBCD6(m.EndTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, endBCD...)
	return buf, nil
}

func (m *PlatformForwardMessage) Unmarshal(data []byte) error {
	if len(data) < 21 {
		return fmt.Errorf("platform forward too short")
	}
	m.Phone = bcdToStringSafe(data[0:6])
	m.LogicChannel = data[6]
	m.AVType = data[7]
	m.StreamType = data[8]
	m.StartTime = bcdToStringSafe(data[9:15])
	m.EndTime = bcdToStringSafe(data[15:21])
	return nil
}

// PlatformForwardResponse 0x1B01 平台间音视频转发应答
// 手机号(6B BCD) + 逻辑通道号(1B) + 结果(1B)
type PlatformForwardResponse struct {
	Phone        string
	LogicChannel byte
	Result       byte
}

func (m *PlatformForwardResponse) MsgID() uint16 { return MsgIDAVForwardResp }

func (m *PlatformForwardResponse) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 8)
	phoneBCD, err := stringToBCD6(m.Phone)
	if err != nil {
		return nil, err
	}
	buf = append(buf, phoneBCD...)
	buf = append(buf, m.LogicChannel, m.Result)
	return buf, nil
}

func (m *PlatformForwardResponse) Unmarshal(data []byte) error {
	if len(data) < 8 {
		return fmt.Errorf("platform forward resp too short")
	}
	m.Phone = bcdToStringSafe(data[0:6])
	m.LogicChannel = data[6]
	m.Result = data[7]
	return nil
}

var _ protocol.MessageBody = (*PlatformNegotiateMessage)(nil)
var _ protocol.MessageBody = (*PlatformNegotiateResponse)(nil)
var _ protocol.MessageBody = (*PlatformForwardMessage)(nil)
var _ protocol.MessageBody = (*PlatformForwardResponse)(nil)

// AUTO-FIX-2026-06-27: 新增 4 个缺失消息（0x9207/0x9302/0x9403/0x9404）

// DownloadControlMessage 0x9207 录像下载控制（平台→终端）
// 逻辑通道号(1B) + 控制指令(1B) + 速度(1B) = 3B
type DownloadControlMessage struct {
	LogicChannel byte
	Command      byte
	Speed        byte
}

func (m *DownloadControlMessage) MsgID() uint16 { return MsgIDDownloadControl }

func (m *DownloadControlMessage) Marshal() ([]byte, error) {
	return []byte{m.LogicChannel, m.Command, m.Speed}, nil
}

func (m *DownloadControlMessage) Unmarshal(data []byte) error {
	if len(data) < 3 {
		return fmt.Errorf("download control too short")
	}
	m.LogicChannel = data[0]
	m.Command = data[1]
	m.Speed = data[2]
	return nil
}

// PTZControlAckMessage 0x9302 PTZ 控制应答（终端→平台）
// 流水号(2B) + 逻辑通道号(1B) + 结果(1B) = 4B
type PTZControlAckMessage struct {
	SeqNum       uint16
	LogicChannel byte
	Result       byte
}

func (m *PTZControlAckMessage) MsgID() uint16 { return MsgIDPTZControlAck }

func (m *PTZControlAckMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint16(buf[0:2], m.SeqNum)
	buf[2] = m.LogicChannel
	buf[3] = m.Result
	return buf, nil
}

func (m *PTZControlAckMessage) Unmarshal(data []byte) error {
	if len(data) < 4 {
		return fmt.Errorf("ptz control ack too short")
	}
	m.SeqNum = binary.BigEndian.Uint16(data[0:2])
	m.LogicChannel = data[2]
	m.Result = data[3]
	return nil
}

// FileUploadRequestMessage 0x9403 文件上传请求（终端→平台）
// AUTO-FIX-2026-06-27: 结构对称 0x9205 修复后版本（与 DownloadRequestMessage 字段一致）。
// 字段顺序: Channel+Start+End+AlarmFlag(uint32)+MediaType+StreamType+StorageType+
//          DownloadType+IP16+TcpPort+UdpPort+UserGBK(12B)+PassGBK(12B)+FilePath
// 最小长度 65B
type FileUploadRequestMessage struct {
	LogicChannel byte
	StartTime    string // BCD YYMMDDHHMMSS
	EndTime      string // BCD YYMMDDHHMMSS
	AlarmFlag    uint32
	MediaType    byte
	StreamType   byte
	StorageType  byte
	DownloadType byte
	IPAddress    string // 16B ASCII
	TcpPort      uint16
	UdpPort      uint16
	Username     string // GBK 12B
	Password     string // GBK 12B
	FilePath     string // 变长
}

func (m *FileUploadRequestMessage) MsgID() uint16 { return MsgIDFileUploadRequest }

func (m *FileUploadRequestMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 65+len(m.FilePath))
	buf = append(buf, m.LogicChannel)
	startBCD, err := stringToBCD6(m.StartTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, startBCD...)
	endBCD, err := stringToBCD6(m.EndTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, endBCD...)
	buf = append(buf, byte(m.AlarmFlag>>24), byte(m.AlarmFlag>>16), byte(m.AlarmFlag>>8), byte(m.AlarmFlag))
	buf = append(buf, m.MediaType, m.StreamType, m.StorageType, m.DownloadType)
	ipBuf := make([]byte, 16)
	copy(ipBuf, []byte(m.IPAddress))
	buf = append(buf, ipBuf...)
	buf = append(buf, byte(m.TcpPort>>8), byte(m.TcpPort))
	buf = append(buf, byte(m.UdpPort>>8), byte(m.UdpPort))
	userBuf, err := encodeGBKFixed(m.Username, 12)
	if err != nil {
		return nil, fmt.Errorf("encode username gbk: %w", err)
	}
	buf = append(buf, userBuf...)
	passBuf, err := encodeGBKFixed(m.Password, 12)
	if err != nil {
		return nil, fmt.Errorf("encode password gbk: %w", err)
	}
	buf = append(buf, passBuf...)
	buf = append(buf, m.FilePath...)
	return buf, nil
}

func (m *FileUploadRequestMessage) Unmarshal(data []byte) error {
	if len(data) < 65 {
		return fmt.Errorf("file upload request too short")
	}
	m.LogicChannel = data[0]
	m.StartTime = bcdToStringSafe(data[1:7])
	m.EndTime = bcdToStringSafe(data[7:13])
	m.AlarmFlag = binary.BigEndian.Uint32(data[13:17])
	m.MediaType = data[17]
	m.StreamType = data[18]
	m.StorageType = data[19]
	m.DownloadType = data[20]
	m.IPAddress = string(bytes.TrimRight(data[21:37], "\x00"))
	m.TcpPort = binary.BigEndian.Uint16(data[37:39])
	m.UdpPort = binary.BigEndian.Uint16(data[39:41])
	var err error
	if m.Username, err = decodeGBKFixed(data[41:53]); err != nil {
		return fmt.Errorf("decode username gbk: %w", err)
	}
	if m.Password, err = decodeGBKFixed(data[53:65]); err != nil {
		return fmt.Errorf("decode password gbk: %w", err)
	}
	if len(data) > 65 {
		m.FilePath = string(data[65:])
	}
	return nil
}

// FileUploadResponseMessage 0x9404 文件上传应答（平台→终端）
// 应答流水号(2B) + 逻辑通道号(1B) + 结果(1B) = 4B
type FileUploadResponseMessage struct {
	RespSeqNum   uint16
	LogicChannel byte
	Result       byte
}

func (m *FileUploadResponseMessage) MsgID() uint16 { return MsgIDFileUploadResponse }

func (m *FileUploadResponseMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint16(buf[0:2], m.RespSeqNum)
	buf[2] = m.LogicChannel
	buf[3] = m.Result
	return buf, nil
}

func (m *FileUploadResponseMessage) Unmarshal(data []byte) error {
	if len(data) < 4 {
		return fmt.Errorf("file upload response too short")
	}
	m.RespSeqNum = binary.BigEndian.Uint16(data[0:2])
	m.LogicChannel = data[2]
	m.Result = data[3]
	return nil
}
