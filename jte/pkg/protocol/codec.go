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
	MsgID     uint16
	BodyAttr  uint16
	Phone     string
	SeqNum    uint16
	PackTotal uint16
	PackIndex uint16
	BodyLen   int
	HasPack   bool
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