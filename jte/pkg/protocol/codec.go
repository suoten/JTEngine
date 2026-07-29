package protocol

type ProtocolType string

const (
	ProtocolJT808  ProtocolType = "jt808"
	ProtocolJT809  ProtocolType = "jt809"
	ProtocolJT905  ProtocolType = "jt905"
	ProtocolJT1078 ProtocolType = "jt1078"
	ProtocolJT1045 ProtocolType = "jt1045"
	ProtocolJT1253 ProtocolType = "jt1253"
	ProtocolGBT32960 ProtocolType = "gbt32960"
)

type MessageHeader struct {
	MsgID         uint16
	BodyAttr      uint16
	Phone         string
	SeqNum        uint16
	PackTotal     uint16
	PackIndex     uint16
	BodyLen       int
	HasPack       bool
	EncryptMethod uint8 // Bit 10-12 加密方式 (0=不加密,1=RSA,2=SM2)
	// [P1-修复] JT/T 809 帧头中的车牌颜色字段（809专用，808不使用）
	PlateColor    byte  // 809 帧头 Byte5: 车牌颜色 (1=蓝,2=黄,3=黑,4=白,5=其他)
	Version2019   bool  // Bit 15 版本标识 (true=2019, false=2011)
	ProtocolVer   byte  // AUTO-FIX-2026-07-17: 2019版本协议版本号字节 (Bit15=1时存在)
}

type MessageBody interface {
	MsgID() uint16
	Marshal() ([]byte, error)
	Unmarshal(data []byte) error
}

type Message struct {
	Header MessageHeader
	Body   MessageBody
	Raw    []byte
}

type Codec interface {
	ProtocolType() ProtocolType
	ParseHeader(data []byte) (*MessageHeader, int, error)
	EncodeHeader(header *MessageHeader) ([]byte, error)
	ParseBody(msgID uint16, data []byte) (MessageBody, error)
	EncodeBody(body MessageBody) ([]byte, error)
	VerifyChecksum(data []byte) bool
}