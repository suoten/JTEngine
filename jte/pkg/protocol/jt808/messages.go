// FIXED: [P0] 0x0200位置解析：经纬度未按StatusFlag位2/3应用正负号，南纬/西经坐标变正值 [2026-07-17]
package jt808

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/suoten/jt-engine/pkg/protocol"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// JT808CoordScaleFactor JT/T 808 协议坐标缩放因子（AUTO-FIX-2026-07-07 [code_quality]：消除魔术数字）。
// 协议规定经纬度以 uint32 存储，精度 6 位小数（即度 × 10^6）。
// 编码时 float64 → uint32 乘以该因子；解码时 uint32 → float64 除以该因子。
const JT808CoordScaleFactor = 1000000.0

type RawMessage struct {
	ID   uint16
	Data []byte
}

func (m *RawMessage) MsgID() uint16 { return m.ID }

func (m *RawMessage) Marshal() ([]byte, error) { return m.Data, nil }

func (m *RawMessage) Unmarshal(data []byte) error {
	m.Data = make([]byte, len(data))
	copy(m.Data, data)
	return nil
}

type RegisterMessage struct {
	ProvinceID    int
	CityID        int
	Manufacturer  string
	TerminalModel string
	TerminalID    string
	PlateColor    byte
	PlateNumber   string // 车牌号（GBK编码，PlateColor=0时为空）
}

func (m *RegisterMessage) MsgID() uint16 { return MsgIDRegister }

func (m *RegisterMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 50)
	buf = append(buf, byte(m.ProvinceID>>8), byte(m.ProvinceID))
	buf = append(buf, byte(m.CityID>>8), byte(m.CityID))
	manu := []byte(m.Manufacturer)
	for len(manu) < 5 {
		manu = append(manu, 0)
	}
	buf = append(buf, manu[:5]...)
	model := []byte(m.TerminalModel)
	for len(model) < 20 {
		model = append(model, 0)
	}
	buf = append(buf, model[:20]...)
	tid := []byte(m.TerminalID)
	for len(tid) < 7 {
		tid = append(tid, 0)
	}
	buf = append(buf, tid[:7]...)
	buf = append(buf, m.PlateColor)
	// AUTO-FIX-2026-07-03: 车牌号（PlateColor!=0 时跟车牌号，GBK编码变长字段）
	if m.PlateColor != 0 && m.PlateNumber != "" {
		enc := simplifiedchinese.GBK.NewEncoder()
		plateBytes, err := enc.Bytes([]byte(m.PlateNumber))
		if err != nil {
			// GBK编码失败时使用原始字节
			plateBytes = []byte(m.PlateNumber)
		}
		buf = append(buf, plateBytes...)
	}
	return buf, nil
}

func (m *RegisterMessage) Unmarshal(data []byte) error {
	if len(data) < 36 {
		return ErrDataTooShort
	}
	m.ProvinceID = int(data[0])<<8 | int(data[1])
	m.CityID = int(data[2])<<8 | int(data[3])
	m.Manufacturer = trimNull(data[4:9])
	m.TerminalModel = trimNull(data[9:29])
	m.TerminalID = trimNull(data[29:36])
	// AUTO-FIX-2026-07-03: PlateColor + PlateNumber 解析
	// 808-2019 标准：PlateColor(1B) 必选，PlateNumber(变长GBK) 在 PlateColor!=0 时可选
	if len(data) > 36 {
		m.PlateColor = data[36]
		// PlateColor!=0 时，data[37:] 为 GBK 编码的车牌号
		if len(data) > 37 && m.PlateColor != 0 {
			rawPlate := data[37:]
			dec := simplifiedchinese.GBK.NewDecoder()
			if decoded, err := dec.String(string(rawPlate)); err == nil {
				m.PlateNumber = strings.TrimRight(decoded, "\x00")
			} else {
				m.PlateNumber = strings.TrimRight(string(rawPlate), "\x00")
			}
		}
	}
	return nil
}

type AuthMessage struct {
	AuthCode string
	IMEI     string
	SoftwareVersion string
}

func (m *AuthMessage) MsgID() uint16 { return MsgIDAuth }

// Marshal 808-2019 0x0102 鉴权消息编解码
// 标准体仅鉴权码；IMEI/SoftwareVersion 为厂商扩展字段，可选。
// 编码顺序：鉴权码 + IMEI(15B，可选) + SoftwareVersion(变长，可选)
func (m *AuthMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, len(m.AuthCode)+15+len(m.SoftwareVersion))
	buf = append(buf, []byte(m.AuthCode)...)
	// IMEI 可选：非空时按 15 字节输出（不足补 0，超长截断）
	if m.IMEI != "" {
		imei := make([]byte, 15)
		copy(imei, m.IMEI)
		buf = append(buf, imei...)
	}
	// SoftwareVersion 可选：非空时追加原始字节
	if m.SoftwareVersion != "" {
		buf = append(buf, []byte(m.SoftwareVersion)...)
	}
	return buf, nil
}

// allowIMEIHeuristic 控制是否启用启发式 IMEI 检测。
// FIXED-2026-07-22 [P0]: 启发式 IMEI 检测（末尾15字节全数字）会误截断以纯数字结尾的鉴权码。
// 默认 false（关闭），标准 0x0102 body 全部作为鉴权码。
// 仅当调用方明确知道终端使用厂商扩展（body = 鉴权码 + 15B IMEI）时，
// 通过 SetIMEIHeuristic(true) 启用。
var allowIMEIHeuristic = false

// SetIMEIHeuristic 设置是否启用启发式 IMEI 检测（非线程安全，应在初始化阶段调用）。
func SetIMEIHeuristic(enabled bool) {
	allowIMEIHeuristic = enabled
}

func (m *AuthMessage) Unmarshal(data []byte) error {
	// 标准 0x0102 body 仅鉴权码（向后兼容：IMEI/SoftwareVersion 为可选扩展）
	// FIXED-2026-07-22 [P0]: 默认关闭启发式 IMEI 检测，避免误截断纯数字结尾的鉴权码。
	// 仅当 allowIMEIHeuristic=true 时，才尝试从 body 末尾剥离 15B IMEI 扩展。
	if allowIMEIHeuristic && len(data) > 15 && isAllDigits(data[len(data)-15:]) {
		m.IMEI = string(data[len(data)-15:])
		remaining := data[:len(data)-15]
		// SoftwareVersion 无法在没有长度前缀的情况下可靠区分，留空
		m.AuthCode = string(remaining)
	} else {
		// 标准格式：整个 body 为鉴权码
		m.AuthCode = string(data)
	}
	return nil
}

// isAllDigits 判断字节切片是否全为 ASCII 数字 0-9
func isAllDigits(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

type HeartbeatMessage struct{}

func (m *HeartbeatMessage) MsgID() uint16 { return MsgIDHeartbeat }

func (m *HeartbeatMessage) Marshal() ([]byte, error) { return nil, nil }

func (m *HeartbeatMessage) Unmarshal(data []byte) error { return nil }

type LocationMessage struct {
	AlarmFlag  uint32
	StatusFlag uint32
	Latitude   float64
	Longitude  float64
	Altitude   uint16
	Speed      uint16
	Direction  uint16
	Time       string
	ExtraData  []byte
	Mileage    uint32
	Fuel       uint16
	Speed2     uint16
	// AUTO-FIX-2026-06-26: 补充0x04+扩展附加信息项字段（按第一轮.txt要求）[2026-06-26]
	OverspeedAlarmState uint32 // 0x04 超速报警附加状态
	AnalogValue         uint16 // 0x06 模拟量
	TirePressure        []byte // 0x05 胎压信息（原始字节）
	// FIXED: [P1] 0x11 从 TirePressure 分离，按 JT/T 808-2019 标准为路线行驶报警附加信息 [2026-07-17]
	RouteAlarmID        uint32 // 0x11 路线ID
	RouteAlarmTime      uint16 // 0x11 路线行驶时间（秒）
	RouteAlarmResult    byte   // 0x11 路线行驶结果
	SignalStrength      uint16 // 0x12 信号强度（GSM 模块）
	IOState             uint16 // 0x13 IO 状态
	ExtVehicleState     uint32 // 0x25 扩展车辆信号状态
	IOStateBits         uint32 // 0x2A IO 状态位
	CustomData0x30      []byte // 0x30 自定义数据（变长）
	CustomData0x31      []byte // 0x31 自定义数据（变长）
	ExtraItems          map[byte][]byte // 其他未识别附加项（原始数据保留）
}

func (m *LocationMessage) MsgID() uint16 { return MsgIDLocation }

func (m *LocationMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 30)

	alarmFlag := make([]byte, 4)
	alarmFlag[0] = byte(m.AlarmFlag >> 24)
	alarmFlag[1] = byte(m.AlarmFlag >> 16)
	alarmFlag[2] = byte(m.AlarmFlag >> 8)
	alarmFlag[3] = byte(m.AlarmFlag)
	buf = append(buf, alarmFlag...)

	statusFlag := make([]byte, 4)
	statusFlag[0] = byte(m.StatusFlag >> 24)
	statusFlag[1] = byte(m.StatusFlag >> 16)
	statusFlag[2] = byte(m.StatusFlag >> 8)
	statusFlag[3] = byte(m.StatusFlag)
	buf = append(buf, statusFlag...)

	// FIXED: [P0] 编码时取绝对值，N/S 由 StatusFlag bit2 指示 [2026-07-17]
	absLat := m.Latitude
	if absLat < 0 {
		absLat = -absLat
	}
	if absLat > 90.0 {
		return nil, fmt.Errorf("latitude %.6f exceeds ±90 range", absLat)
	}
	lat := uint32(absLat * JT808CoordScaleFactor)
	latBytes := make([]byte, 4)
	latBytes[0] = byte(lat >> 24)
	latBytes[1] = byte(lat >> 16)
	latBytes[2] = byte(lat >> 8)
	latBytes[3] = byte(lat)
	buf = append(buf, latBytes...)

	// FIXED: [P0] 编码时取绝对值，E/W 由 StatusFlag bit3 指示 [2026-07-17]
	absLon := m.Longitude
	if absLon < 0 {
		absLon = -absLon
	}
	if absLon > 180.0 {
		return nil, fmt.Errorf("longitude %.6f exceeds ±180 range", absLon)
	}
	lon := uint32(absLon * JT808CoordScaleFactor)
	lonBytes := make([]byte, 4)
	lonBytes[0] = byte(lon >> 24)
	lonBytes[1] = byte(lon >> 16)
	lonBytes[2] = byte(lon >> 8)
	lonBytes[3] = byte(lon)
	buf = append(buf, lonBytes...)

	altBytes := make([]byte, 2)
	altBytes[0] = byte(m.Altitude >> 8)
	altBytes[1] = byte(m.Altitude)
	buf = append(buf, altBytes...)

	speedBytes := make([]byte, 2)
	speedBytes[0] = byte(m.Speed >> 8)
	speedBytes[1] = byte(m.Speed)
	buf = append(buf, speedBytes...)

	dirBytes := make([]byte, 2)
	dirBytes[0] = byte(m.Direction >> 8)
	dirBytes[1] = byte(m.Direction)
	buf = append(buf, dirBytes...)

	timeBCD, err := StringToBCD6(m.Time)
	if err != nil {
		return nil, err
	}
	buf = append(buf, timeBCD...)

	// 附加信息项（TLV格式），按 JT/T 808-2019 标准
	// 0x01 里程
	if m.Mileage > 0 {
		buf = append(buf, 0x01, 0x04)
		buf = append(buf, byte(m.Mileage>>24), byte(m.Mileage>>16), byte(m.Mileage>>8), byte(m.Mileage))
	}
	// 0x02 油量
	if m.Fuel > 0 {
		buf = append(buf, 0x02, 0x02)
		buf = append(buf, byte(m.Fuel>>8), byte(m.Fuel))
	}
	// 0x03 速度2
	if m.Speed2 > 0 {
		buf = append(buf, 0x03, 0x02)
		buf = append(buf, byte(m.Speed2>>8), byte(m.Speed2))
	}
	// 0x04 超速报警附加状态
	if m.OverspeedAlarmState > 0 {
		buf = append(buf, 0x04, 0x04)
		buf = append(buf, byte(m.OverspeedAlarmState>>24), byte(m.OverspeedAlarmState>>16), byte(m.OverspeedAlarmState>>8), byte(m.OverspeedAlarmState))
	}
	// 0x06 模拟量
	if m.AnalogValue > 0 {
		buf = append(buf, 0x06, 0x02)
		buf = append(buf, byte(m.AnalogValue>>8), byte(m.AnalogValue))
	}
	// 0x05 胎压信息
	if len(m.TirePressure) > 0 {
		buf = append(buf, 0x05, byte(len(m.TirePressure)))
		buf = append(buf, m.TirePressure...)
	}
	// FIXED: [P1] 0x11 路线行驶报警附加信息（7字节: 路线ID 4B + 行驶时间 2B + 结果 1B） [2026-07-17]
	if m.RouteAlarmResult != 0 || m.RouteAlarmID != 0 {
		buf = append(buf, 0x11, 0x07)
		buf = append(buf, byte(m.RouteAlarmID>>24), byte(m.RouteAlarmID>>16), byte(m.RouteAlarmID>>8), byte(m.RouteAlarmID))
		buf = append(buf, byte(m.RouteAlarmTime>>8), byte(m.RouteAlarmTime))
		buf = append(buf, m.RouteAlarmResult)
	}
	// 0x12 信号强度
	if m.SignalStrength > 0 {
		buf = append(buf, 0x12, 0x02)
		buf = append(buf, byte(m.SignalStrength>>8), byte(m.SignalStrength))
	}
	// 0x13 IO状态
	if m.IOState > 0 {
		buf = append(buf, 0x13, 0x02)
		buf = append(buf, byte(m.IOState>>8), byte(m.IOState))
	}
	// 0x25 扩展车辆信号状态
	if m.ExtVehicleState > 0 {
		buf = append(buf, 0x25, 0x04)
		buf = append(buf, byte(m.ExtVehicleState>>24), byte(m.ExtVehicleState>>16), byte(m.ExtVehicleState>>8), byte(m.ExtVehicleState))
	}
	// 0x2A IO状态位
	if m.IOStateBits > 0 {
		buf = append(buf, 0x2A, 0x04)
		buf = append(buf, byte(m.IOStateBits>>24), byte(m.IOStateBits>>16), byte(m.IOStateBits>>8), byte(m.IOStateBits))
	}
	// 0x30 自定义数据
	if len(m.CustomData0x30) > 0 {
		buf = append(buf, 0x30, byte(len(m.CustomData0x30)))
		buf = append(buf, m.CustomData0x30...)
	}
	// 0x31 自定义数据
	if len(m.CustomData0x31) > 0 {
		buf = append(buf, 0x31, byte(len(m.CustomData0x31)))
		buf = append(buf, m.CustomData0x31...)
	}
	// 其他未识别附加项
	// FIXED: [P0] map 迭代顺序随机，改为按 itemID 排序后遍历，确保编码确定性 [2026-07-22]
	if len(m.ExtraItems) > 0 {
		itemIDs := make([]byte, 0, len(m.ExtraItems))
		for id := range m.ExtraItems {
			itemIDs = append(itemIDs, id)
		}
		sort.Slice(itemIDs, func(i, j int) bool { return itemIDs[i] < itemIDs[j] })
		for _, id := range itemIDs {
			val := m.ExtraItems[id]
			if len(val) > 0 {
				buf = append(buf, id, byte(len(val)))
				buf = append(buf, val...)
			}
		}
	}

	if len(m.ExtraData) > 0 {
		buf = append(buf, m.ExtraData...)
	}

	return buf, nil
}

func (m *LocationMessage) Unmarshal(data []byte) error {
	if len(data) < 28 {
		return ErrDataTooShort
	}

	m.AlarmFlag = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	m.StatusFlag = uint32(data[4])<<24 | uint32(data[5])<<16 | uint32(data[6])<<8 | uint32(data[7])

	latRaw := uint32(data[8])<<24 | uint32(data[9])<<16 | uint32(data[10])<<8 | uint32(data[11])
	m.Latitude = float64(latRaw) / JT808CoordScaleFactor
	// FIXED: [P0] StatusFlag bit2=1 表示南纬，Latitude 取负 [2026-07-17]
	if m.StatusFlag&0x04 != 0 {
		m.Latitude = -m.Latitude
	}

	lonRaw := uint32(data[12])<<24 | uint32(data[13])<<16 | uint32(data[14])<<8 | uint32(data[15])
	m.Longitude = float64(lonRaw) / JT808CoordScaleFactor
	// FIXED: [P0] StatusFlag bit3=1 表示西经，Longitude 取负 [2026-07-17]
	if m.StatusFlag&0x08 != 0 {
		m.Longitude = -m.Longitude
	}

	m.Altitude = uint16(data[16])<<8 | uint16(data[17])
	m.Speed = uint16(data[18])<<8 | uint16(data[19])
	m.Direction = uint16(data[20])<<8 | uint16(data[21])

	m.Time = BCDToStringSafe(data[22:28]) // AUTO-FIX-2026-06-26: 时间字段保留前导零

	if len(data) > 28 {
		m.ExtraData = make([]byte, len(data)-28)
		copy(m.ExtraData, data[28:])
		m.parseExtraItems(m.ExtraData)
	}

	return nil
}

// maxExtraItems 附加信息项最大数量上限。
// FIXED-2026-07-22 [P1]: 防止恶意终端构造大量小附加项导致 CPU 耗尽。
const maxExtraItems = 100

// maxExtraItemLen 单个附加项最大长度。
// JT/T 808-2019 标准附加项长度字段为 1 字节（最大 255），但实际附加项不会超过 256B。
// 超过此值视为异常数据（可能是帧解析错位）。
const maxExtraItemLen = 256

func (m *LocationMessage) parseExtraItems(data []byte) {
	offset := 0
	itemCount := 0
	for offset+2 <= len(data) {
		// FIXED-2026-07-22 [P1]: 附加项数量上限检查
		if itemCount >= maxExtraItems {
			break
		}

		itemID := data[offset]
		itemLen := int(data[offset+1])
		offset += 2

		// FIXED-2026-07-22 [P1]: itemLen 合理性检查
		if itemLen > maxExtraItemLen {
			break
		}

		if offset+itemLen > len(data) {
			break
		}

		itemData := data[offset : offset+itemLen]

		switch itemID {
		case 0x01:
			if len(itemData) >= 4 {
				m.Mileage = uint32(itemData[0])<<24 | uint32(itemData[1])<<16 | uint32(itemData[2])<<8 | uint32(itemData[3])
			}
		case 0x02:
			if len(itemData) >= 2 {
				m.Fuel = uint16(itemData[0])<<8 | uint16(itemData[1])
			}
		case 0x03:
			if len(itemData) >= 2 {
				m.Speed2 = uint16(itemData[0])<<8 | uint16(itemData[1])
			}
		// AUTO-FIX-2026-06-26: 补充0x04+扩展附加信息项解析（按第一轮.txt要求）[2026-06-26]
		case 0x04:
			if len(itemData) >= 4 {
				m.OverspeedAlarmState = uint32(itemData[0])<<24 | uint32(itemData[1])<<16 | uint32(itemData[2])<<8 | uint32(itemData[3])
			}
		case 0x05:
			// 0x05 胎压信息：保留原始字节（结构因车型而异）
			m.TirePressure = make([]byte, len(itemData))
			copy(m.TirePressure, itemData)
		// FIXED: [P1] 0x11 从 0x05 分离，按 JT/T 808-2019 标准为路线行驶报警附加信息 [2026-07-17]
		// 格式：路线ID(4B) + 行驶时间(2B,秒) + 结果(1B)
		case 0x11:
			if len(itemData) >= 7 {
				m.RouteAlarmID = uint32(itemData[0])<<24 | uint32(itemData[1])<<16 | uint32(itemData[2])<<8 | uint32(itemData[3])
				m.RouteAlarmTime = uint16(itemData[4])<<8 | uint16(itemData[5])
				m.RouteAlarmResult = itemData[6]
			} else {
				// 数据不足7字节，保留原始数据到 ExtraItems
				if m.ExtraItems == nil {
					m.ExtraItems = make(map[byte][]byte)
				}
				itemCopy := make([]byte, len(itemData))
				copy(itemCopy, itemData)
				m.ExtraItems[itemID] = itemCopy
			}
		// 0x12 信号强度（GSM 模块）
		case 0x12:
			if len(itemData) >= 2 {
				m.SignalStrength = uint16(itemData[0])<<8 | uint16(itemData[1])
			}
		// 0x13 IO 状态
		case 0x13:
			if len(itemData) >= 2 {
				m.IOState = uint16(itemData[0])<<8 | uint16(itemData[1])
			}
		// 0x25 扩展车辆信号状态
		case 0x25:
			if len(itemData) >= 4 {
				m.ExtVehicleState = uint32(itemData[0])<<24 | uint32(itemData[1])<<16 | uint32(itemData[2])<<8 | uint32(itemData[3])
			}
		// 0x2A IO 状态位
		case 0x2A:
			if len(itemData) >= 4 {
				m.IOStateBits = uint32(itemData[0])<<24 | uint32(itemData[1])<<16 | uint32(itemData[2])<<8 | uint32(itemData[3])
			}
		// 0x30 自定义数据（变长）
		case 0x30:
			m.CustomData0x30 = make([]byte, len(itemData))
			copy(m.CustomData0x30, itemData)
		// 0x31 自定义数据（变长）
		case 0x31:
			m.CustomData0x31 = make([]byte, len(itemData))
			copy(m.CustomData0x31, itemData)
		case 0x06:
			if len(itemData) >= 2 {
				m.AnalogValue = uint16(itemData[0])<<8 | uint16(itemData[1])
			}
		default:
			// 保留未识别的附加项原始数据，便于后续扩展
			if m.ExtraItems == nil {
				m.ExtraItems = make(map[byte][]byte)
			}
			itemCopy := make([]byte, len(itemData))
			copy(itemCopy, itemData)
			m.ExtraItems[itemID] = itemCopy
		}

		offset += itemLen
		itemCount++
	}
}

type GeneralResponse struct {
	RespSeqNum uint16
	RespMsgID  uint16
	Result     byte
}

func (m *GeneralResponse) MsgID() uint16 { return MsgIDGeneralResp }

func (m *GeneralResponse) Marshal() ([]byte, error) {
	buf := make([]byte, 5)
	buf[0] = byte(m.RespSeqNum >> 8)
	buf[1] = byte(m.RespSeqNum)
	buf[2] = byte(m.RespMsgID >> 8)
	buf[3] = byte(m.RespMsgID)
	buf[4] = m.Result
	return buf, nil
}

func (m *GeneralResponse) Unmarshal(data []byte) error {
	if len(data) < 5 {
		return ErrDataTooShort
	}
	m.RespSeqNum = uint16(data[0])<<8 | uint16(data[1])
	m.RespMsgID = uint16(data[2])<<8 | uint16(data[3])
	m.Result = data[4]
	return nil
}

type RegisterResponse struct {
	RespSeqNum uint16
	Result     byte
	AuthCode   string
}

func (m *RegisterResponse) MsgID() uint16 { return MsgIDRegisterResp }

func (m *RegisterResponse) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 20)
	buf = append(buf, byte(m.RespSeqNum>>8), byte(m.RespSeqNum))
	buf = append(buf, m.Result)
	buf = append(buf, []byte(m.AuthCode)...)
	return buf, nil
}

func (m *RegisterResponse) Unmarshal(data []byte) error {
	if len(data) < 3 {
		return ErrDataTooShort
	}
	m.RespSeqNum = uint16(data[0])<<8 | uint16(data[1])
	m.Result = data[2]
	if len(data) > 3 {
		m.AuthCode = string(data[3:])
	}
	return nil
}

type CommandMessage struct {
	SeqNum uint16
	Params map[uint32][]byte
}

func (m *CommandMessage) MsgID() uint16 { return MsgIDCommand }

func (m *CommandMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 100)
	// AUTO-FIX-2026-06-26: 首部改为1B参数总数（标准0x8103），原2B SeqNum不符规范
	buf = append(buf, byte(len(m.Params)))
	// [P1-修复] 确定性编码：按 paramID 排序后遍历，确保同一消息多次编码产生相同字节序列
	paramIDs := make([]uint32, 0, len(m.Params))
	for id := range m.Params {
		paramIDs = append(paramIDs, id)
	}
	sort.Slice(paramIDs, func(i, j int) bool { return paramIDs[i] < paramIDs[j] })
	for _, id := range paramIDs {
		val := m.Params[id]
		idBytes := make([]byte, 4)
		idBytes[0] = byte(id >> 24)
		idBytes[1] = byte(id >> 16)
		idBytes[2] = byte(id >> 8)
		idBytes[3] = byte(id)
		buf = append(buf, idBytes...)
		buf = append(buf, byte(len(val)))
		buf = append(buf, val...)
	}
	return buf, nil
}

func (m *CommandMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-06-26: 首部改为1B参数总数（标准0x8103），原读2B SeqNum不符规范
	if len(data) < 1 {
		return ErrDataTooShort
	}
	count := int(data[0])
	if count > protocol.MaxElementCount {
		return fmt.Errorf("CommandMessage: param count %d exceeds max %d", count, protocol.MaxElementCount)
	}
	m.Params = make(map[uint32][]byte)
	offset := 1
	for i := 0; i < count; i++ {
		if offset+5 > len(data) {
			return fmt.Errorf("CommandMessage: expected %d params, got %d: %w", count, i, ErrDataTooShort)
		}
		paramID := uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
		paramLen := int(data[offset+4])
		offset += 5
		if offset+paramLen > len(data) {
			return fmt.Errorf("CommandMessage param %d: data too short: %w", i, ErrDataTooShort)
		}
		val := make([]byte, paramLen)
		copy(val, data[offset:offset+paramLen])
		m.Params[paramID] = val
		offset += paramLen
	}
	return nil
}

type ParamQueryMessage struct {
	SeqNum  uint16
	ParamIDs []uint32
}

func (m *ParamQueryMessage) MsgID() uint16 { return MsgIDParamQuery }

func (m *ParamQueryMessage) Marshal() ([]byte, error) {
	// AUTO-FIX-2026-06-26: 标准消息体为空（0x8104查询终端参数无消息体）
	return nil, nil
}

func (m *ParamQueryMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-06-26: 标准消息体为空，接受空体或任意体
	return nil
}

type ParamSetMessage struct {
	// AUTO-FIX-2026-06-27: 0x8106 移除 SeqNum(2B)，参数项改为仅ID(4B)×N，体为 参数总数(1B)+参数ID列表(4B×N)
	ParamIDs []uint32
}

type ParamRespMessage struct {
	SeqNum uint16
	Params map[uint32][]byte
}

func (m *ParamRespMessage) MsgID() uint16 { return MsgIDParamResp }

func (m *ParamRespMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 100)
	buf = append(buf, byte(m.SeqNum>>8), byte(m.SeqNum))
	// AUTO-FIX-2026-06-26: 追加1B参数总数（标准0x0104格式: SeqNum+参数总数+参数项）
	buf = append(buf, byte(len(m.Params)))
	// [P1-修复] 确定性编码：按 paramID 排序后遍历
	paramIDs := make([]uint32, 0, len(m.Params))
	for id := range m.Params {
		paramIDs = append(paramIDs, id)
	}
	sort.Slice(paramIDs, func(i, j int) bool { return paramIDs[i] < paramIDs[j] })
	for _, id := range paramIDs {
		val := m.Params[id]
		idBytes := make([]byte, 4)
		idBytes[0] = byte(id >> 24)
		idBytes[1] = byte(id >> 16)
		idBytes[2] = byte(id >> 8)
		idBytes[3] = byte(id)
		buf = append(buf, idBytes...)
		buf = append(buf, byte(len(val)))
		buf = append(buf, val...)
	}
	return buf, nil
}

func (m *ParamRespMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-06-26: 读取1B参数总数（标准0x0104格式: SeqNum+参数总数+参数项）
	if len(data) < 3 {
		return ErrDataTooShort
	}
	m.SeqNum = uint16(data[0])<<8 | uint16(data[1])
	count := int(data[2])
	if count > protocol.MaxElementCount {
		return fmt.Errorf("ParamResp: param count %d exceeds max %d", count, protocol.MaxElementCount)
	}
	m.Params = make(map[uint32][]byte)
	offset := 3
	for i := 0; i < count; i++ {
		if offset+5 > len(data) {
			return fmt.Errorf("ParamResp: expected %d params, got %d: %w", count, i, ErrDataTooShort)
		}
		paramID := uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
		paramLen := int(data[offset+4])
		offset += 5
		if offset+paramLen > len(data) {
			return fmt.Errorf("ParamResp param %d: data too short: %w", i, ErrDataTooShort)
		}
		val := make([]byte, paramLen)
		copy(val, data[offset:offset+paramLen])
		m.Params[paramID] = val
		offset += paramLen
	}
	return nil
}


func (m *ParamSetMessage) MsgID() uint16 { return MsgIDParamSet }

// AUTO-FIX-2026-06-27: 0x8106 体改为 参数总数(1B)+参数ID列表(4B×N)
func (m *ParamSetMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 1+len(m.ParamIDs)*4)
	buf = append(buf, byte(len(m.ParamIDs)))
	for _, id := range m.ParamIDs {
		buf = append(buf, byte(id>>24), byte(id>>16), byte(id>>8), byte(id))
	}
	return buf, nil
}

func (m *ParamSetMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-06-27: 0x8106 体改为 参数总数(1B)+参数ID列表(4B×N)
	if len(data) < 1 {
		return ErrDataTooShort
	}
	count := int(data[0])
	m.ParamIDs = make([]uint32, 0, count)
	offset := 1
	for i := 0; i < count && offset+4 <= len(data); i++ {
		id := uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
		m.ParamIDs = append(m.ParamIDs, id)
		offset += 4
	}
	return nil
}

type TerminalCancelMessage struct{}

func (m *TerminalCancelMessage) MsgID() uint16 { return MsgIDTerminalCancel }

func (m *TerminalCancelMessage) Marshal() ([]byte, error) { return nil, nil }

func (m *TerminalCancelMessage) Unmarshal(data []byte) error { return nil }

type TerminalCancelResponse struct {
	Result byte
}

func (m *TerminalCancelResponse) MsgID() uint16 { return MsgIDTerminalCancelResp }

func (m *TerminalCancelResponse) Marshal() ([]byte, error) {
	return []byte{m.Result}, nil
}

func (m *TerminalCancelResponse) Unmarshal(data []byte) error {
	if len(data) < 1 {
		return ErrDataTooShort
	}
	m.Result = data[0]
	return nil
}

type LocationBatchMessage struct {
	LocationType byte   // AUTO-FIX-2026-06-26: 修正字段顺序，标准为Type在前、Count在后
	Count        uint16
	Locations    []*LocationMessage
}

func (m *LocationBatchMessage) MsgID() uint16 { return MsgIDLocationBatch }

func (m *LocationBatchMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 4+len(m.Locations)*30)
	// AUTO-FIX-2026-06-26: 标准顺序为 Type(1字节) + Count(2字节)
	buf = append(buf, m.LocationType)
	buf = append(buf, byte(m.Count>>8), byte(m.Count))
	for _, loc := range m.Locations {
		locData, err := loc.Marshal()
		if err != nil {
			return nil, err
		}
		locLen := uint16(len(locData))
		buf = append(buf, byte(locLen>>8), byte(locLen))
		buf = append(buf, locData...)
	}
	return buf, nil
}

func (m *LocationBatchMessage) Unmarshal(data []byte) error {
	if len(data) < 3 {
		return ErrDataTooShort
	}
	// AUTO-FIX-2026-06-26: 标准顺序为 Type(1字节) + Count(2字节)
	m.LocationType = data[0]
	m.Count = uint16(data[1])<<8 | uint16(data[2])
	if int(m.Count) > protocol.MaxElementCount {
		return fmt.Errorf("LocationBatch: count %d exceeds max %d", m.Count, protocol.MaxElementCount)
	}
	m.Locations = make([]*LocationMessage, 0, m.Count)
	offset := 3
	for i := 0; i < int(m.Count); i++ {
		if offset+2 > len(data) {
			return fmt.Errorf("LocationBatch: expected %d locations, got %d: %w", m.Count, i, ErrDataTooShort)
		}
		locLen := int(uint16(data[offset])<<8 | uint16(data[offset+1]))
		offset += 2
		if offset+locLen > len(data) {
			return fmt.Errorf("LocationBatch item %d: data too short: %w", i, ErrDataTooShort)
		}
		loc := &LocationMessage{}
		if err := loc.Unmarshal(data[offset : offset+locLen]); err != nil {
			return err
		}
		m.Locations = append(m.Locations, loc)
		offset += locLen
	}
	return nil
}

type LocationQueryResponse struct {
	Location LocationMessage
}

func (m *LocationQueryResponse) MsgID() uint16 { return MsgIDLocationQueryResp }

func (m *LocationQueryResponse) Marshal() ([]byte, error) {
	return m.Location.Marshal()
}

func (m *LocationQueryResponse) Unmarshal(data []byte) error {
	return m.Location.Unmarshal(data)
}

// AUTO-FIX-2026-06-26: 补充0x8201位置查询请求消息体（标准规定消息体为空）
// 平台→终端，请求终端上报一次位置。
type LocationQueryMessage struct{}

func (m *LocationQueryMessage) MsgID() uint16 { return MsgIDLocationQuery }

func (m *LocationQueryMessage) Marshal() ([]byte, error)  { return nil, nil }
func (m *LocationQueryMessage) Unmarshal(data []byte) error { return nil }

type MultimediaMessage struct {
	MultimediaID   uint32
	MultimediaType byte
	MultimediaFmt  byte
	EventItem      byte
	ChannelID      byte
	Location       LocationMessage
	MediaLen       uint32
}

func (m *MultimediaMessage) MsgID() uint16 { return MsgIDMultimedia }

func (m *MultimediaMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 40)
	buf = append(buf, byte(m.MultimediaID>>24), byte(m.MultimediaID>>16), byte(m.MultimediaID>>8), byte(m.MultimediaID))
	buf = append(buf, m.MultimediaType)
	buf = append(buf, m.MultimediaFmt)
	buf = append(buf, m.EventItem)
	buf = append(buf, m.ChannelID)
	locData, err := m.Location.Marshal()
	if err != nil {
		return nil, err
	}
	buf = append(buf, locData...)
	buf = append(buf, byte(m.MediaLen>>24), byte(m.MediaLen>>16), byte(m.MediaLen>>8), byte(m.MediaLen))
	return buf, nil
}

func (m *MultimediaMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-06-26: 完整消息含MediaLen(4B)共40B，边界由<36收紧为<40
	if len(data) < 40 {
		return ErrDataTooShort
	}
	m.MultimediaID = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	m.MultimediaType = data[4]
	m.MultimediaFmt = data[5]
	m.EventItem = data[6]
	m.ChannelID = data[7]
	// FIXED-2026-07-22 [P1]: locEnd 与 MediaLen 读取位置统一。
	// locEnd = len(data) - 4，Location 区域为 data[8:locEnd]，MediaLen 位于 data[locEnd:locEnd+4]。
	// 当 body == 40B 时 locEnd=36，与原逻辑一致；
	// 当 body > 40B 时 locEnd=len(data)-4，正确排除 MediaLen。
	locEnd := len(data) - 4
	if locEnd < 36 {
		locEnd = 36 // 最小 28 字节位置信息
	}
	if err := m.Location.Unmarshal(data[8:locEnd]); err != nil {
		return err
	}
	if len(data) >= locEnd+4 {
		m.MediaLen = uint32(data[locEnd])<<24 | uint32(data[locEnd+1])<<16 | uint32(data[locEnd+2])<<8 | uint32(data[locEnd+3])
	}
	return nil
}

type MultimediaUploadMessage struct {
	// AUTO-FIX-2026-06-27: 0x0802 移除 DataType(1B) 字段，标准体为 多媒体ID(4B)+包序号(2B)+包总数(2B)+数据体
	MultimediaID uint32
	PacketIndex  uint16 // 包序号
	PacketTotal  uint16 // 包总数
	MediaData    []byte
}

func (m *MultimediaUploadMessage) MsgID() uint16 { return MsgIDMultimediaUpload }

// AUTO-FIX-2026-06-27: 字段顺序改为 多媒体ID(4B)+包序号(2B)+包总数(2B)+数据体，最小长度由 9 收紧为 8
func (m *MultimediaUploadMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 8+len(m.MediaData))
	buf = append(buf, byte(m.MultimediaID>>24), byte(m.MultimediaID>>16), byte(m.MultimediaID>>8), byte(m.MultimediaID))
	buf = append(buf, byte(m.PacketIndex>>8), byte(m.PacketIndex))
	buf = append(buf, byte(m.PacketTotal>>8), byte(m.PacketTotal))
	buf = append(buf, m.MediaData...)
	return buf, nil
}

func (m *MultimediaUploadMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-06-27: 字段顺序改为 多媒体ID(4B)+包序号(2B)+包总数(2B)+数据体
	if len(data) < 8 {
		return ErrDataTooShort
	}
	m.MultimediaID = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	m.PacketIndex = uint16(data[4])<<8 | uint16(data[5])
	m.PacketTotal = uint16(data[6])<<8 | uint16(data[7])
	if len(data) > 8 {
		m.MediaData = make([]byte, len(data)-8)
		copy(m.MediaData, data[8:])
	}
	return nil
}

type CircularAreaSetMessage struct {
	SetType byte
	Areas   []CircularArea
}

type CircularArea struct {
	AreaID   uint32
	CenterLat float64
	CenterLon float64
	Radius   uint32
	SpeedLimit uint16
	Duration  uint16
	MaxSpeed  uint16
	NightMaxSpeed uint16
}

func (m *CircularAreaSetMessage) MsgID() uint16 { return MsgIDCircularAreaSet }

func (m *CircularAreaSetMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 1+len(m.Areas)*27)
	buf = append(buf, m.SetType)
	buf = append(buf, byte(len(m.Areas)>>8), byte(len(m.Areas)))
	for _, area := range m.Areas {
		buf = append(buf, byte(area.AreaID>>24), byte(area.AreaID>>16), byte(area.AreaID>>8), byte(area.AreaID))
		// [P0-修复] 负坐标处理：南纬/西经为负值，uint32 转换前取绝对值
		absLat := area.CenterLat
		if absLat < 0 {
			absLat = -absLat
		}
		absLon := area.CenterLon
		if absLon < 0 {
			absLon = -absLon
		}
		if absLat > 90.0 {
		return nil, fmt.Errorf("latitude %.6f exceeds ±90 range", absLat)
	}
	lat := uint32(absLat * JT808CoordScaleFactor)
		if absLon > 180.0 {
		return nil, fmt.Errorf("longitude %.6f exceeds ±180 range", absLon)
	}
	lon := uint32(absLon * JT808CoordScaleFactor)
		buf = append(buf, byte(lat>>24), byte(lat>>16), byte(lat>>8), byte(lat))
		buf = append(buf, byte(lon>>24), byte(lon>>16), byte(lon>>8), byte(lon))
		buf = append(buf, byte(area.Radius>>24), byte(area.Radius>>16), byte(area.Radius>>8), byte(area.Radius))
		buf = append(buf, byte(area.SpeedLimit>>8), byte(area.SpeedLimit))
		buf = append(buf, byte(area.Duration>>8), byte(area.Duration))
		buf = append(buf, byte(area.MaxSpeed>>8), byte(area.MaxSpeed))
		buf = append(buf, byte(area.NightMaxSpeed>>8), byte(area.NightMaxSpeed))
	}
	return buf, nil
}

func (m *CircularAreaSetMessage) Unmarshal(data []byte) error {
	if len(data) < 3 {
		return ErrDataTooShort
	}
	m.SetType = data[0]
	areaCount := int(uint16(data[1])<<8 | uint16(data[2]))
	if areaCount > protocol.MaxElementCount {
		return fmt.Errorf("CircularAreaSet: area count %d exceeds max %d", areaCount, protocol.MaxElementCount)
	}
	m.Areas = make([]CircularArea, 0, areaCount)
	offset := 3
	for i := 0; i < areaCount; i++ {
		if offset+24 > len(data) {
			return fmt.Errorf("CircularAreaSet: expected %d areas, got %d: %w", areaCount, i, ErrDataTooShort)
		}
		var area CircularArea
		area.AreaID = uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
		latRaw := uint32(data[offset+4])<<24 | uint32(data[offset+5])<<16 | uint32(data[offset+6])<<8 | uint32(data[offset+7])
		area.CenterLat = float64(latRaw) / JT808CoordScaleFactor
		lonRaw := uint32(data[offset+8])<<24 | uint32(data[offset+9])<<16 | uint32(data[offset+10])<<8 | uint32(data[offset+11])
		area.CenterLon = float64(lonRaw) / JT808CoordScaleFactor
		area.Radius = uint32(data[offset+12])<<24 | uint32(data[offset+13])<<16 | uint32(data[offset+14])<<8 | uint32(data[offset+15])
		area.SpeedLimit = uint16(data[offset+16])<<8 | uint16(data[offset+17])
		area.Duration = uint16(data[offset+18])<<8 | uint16(data[offset+19])
		area.MaxSpeed = uint16(data[offset+20])<<8 | uint16(data[offset+21])
		area.NightMaxSpeed = uint16(data[offset+22])<<8 | uint16(data[offset+23])
		m.Areas = append(m.Areas, area)
		offset += 24
	}
	return nil
}

type CircularAreaDelMessage struct {
	AreaIDs []uint32
}

func (m *CircularAreaDelMessage) MsgID() uint16 { return MsgIDCircularAreaDel }

func (m *CircularAreaDelMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 2+len(m.AreaIDs)*4)
	buf = append(buf, byte(len(m.AreaIDs)>>8), byte(len(m.AreaIDs)))
	for _, id := range m.AreaIDs {
		buf = append(buf, byte(id>>24), byte(id>>16), byte(id>>8), byte(id))
	}
	return buf, nil
}

func (m *CircularAreaDelMessage) Unmarshal(data []byte) error {
	if len(data) < 2 {
		return ErrDataTooShort
	}
	count := int(uint16(data[0])<<8 | uint16(data[1]))
	m.AreaIDs = make([]uint32, 0, count)
	offset := 2
	for i := 0; i < count && offset+4 <= len(data); i++ {
		id := uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
		m.AreaIDs = append(m.AreaIDs, id)
		offset += 4
	}
	return nil
}

type RectAreaSetMessage struct {
	SetType byte
	Areas   []RectArea
}

type RectArea struct {
	AreaID   uint32
	TopLat   float64
	TopLon   float64
	BottomLat float64
	BottomLon float64
	SpeedLimit uint16
	Duration  uint16
	MaxSpeed  uint16
	NightMaxSpeed uint16
}

func (m *RectAreaSetMessage) MsgID() uint16 { return MsgIDRectAreaSet }

func (m *RectAreaSetMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 1+len(m.Areas)*35)
	buf = append(buf, m.SetType)
	buf = append(buf, byte(len(m.Areas)>>8), byte(len(m.Areas)))
	for _, area := range m.Areas {
		buf = append(buf, byte(area.AreaID>>24), byte(area.AreaID>>16), byte(area.AreaID>>8), byte(area.AreaID))
		// [P0-修复] 负坐标处理：矩形区域四角坐标可能为南纬/西经负值
		absTopLat := area.TopLat
		if absTopLat < 0 {
			absTopLat = -absTopLat
		}
		absTopLon := area.TopLon
		if absTopLon < 0 {
			absTopLon = -absTopLon
		}
		absBotLat := area.BottomLat
		if absBotLat < 0 {
			absBotLat = -absBotLat
		}
		absBotLon := area.BottomLon
		if absBotLon < 0 {
			absBotLon = -absBotLon
		}
		if absTopLat > 90.0 {
		return nil, fmt.Errorf("top latitude %.6f exceeds ±90 range", absTopLat)
	}
	topLat := uint32(absTopLat * JT808CoordScaleFactor)
	if absTopLon > 180.0 {
		return nil, fmt.Errorf("top longitude %.6f exceeds ±180 range", absTopLon)
	}
	topLon := uint32(absTopLon * JT808CoordScaleFactor)
	if absBotLat > 90.0 {
		return nil, fmt.Errorf("bottom latitude %.6f exceeds ±90 range", absBotLat)
	}
	botLat := uint32(absBotLat * JT808CoordScaleFactor)
	if absBotLon > 180.0 {
		return nil, fmt.Errorf("bottom longitude %.6f exceeds ±180 range", absBotLon)
	}
	botLon := uint32(absBotLon * JT808CoordScaleFactor)
		buf = append(buf, byte(topLat>>24), byte(topLat>>16), byte(topLat>>8), byte(topLat))
		buf = append(buf, byte(topLon>>24), byte(topLon>>16), byte(topLon>>8), byte(topLon))
		buf = append(buf, byte(botLat>>24), byte(botLat>>16), byte(botLat>>8), byte(botLat))
		buf = append(buf, byte(botLon>>24), byte(botLon>>16), byte(botLon>>8), byte(botLon))
		buf = append(buf, byte(area.SpeedLimit>>8), byte(area.SpeedLimit))
		buf = append(buf, byte(area.Duration>>8), byte(area.Duration))
		buf = append(buf, byte(area.MaxSpeed>>8), byte(area.MaxSpeed))
		buf = append(buf, byte(area.NightMaxSpeed>>8), byte(area.NightMaxSpeed))
	}
	return buf, nil
}

func (m *RectAreaSetMessage) Unmarshal(data []byte) error {
	if len(data) < 3 {
		return ErrDataTooShort
	}
	m.SetType = data[0]
	areaCount := int(uint16(data[1])<<8 | uint16(data[2]))
	if areaCount > protocol.MaxElementCount {
		return fmt.Errorf("RectAreaSet: area count %d exceeds max %d", areaCount, protocol.MaxElementCount)
	}
	m.Areas = make([]RectArea, 0, areaCount)
	offset := 3
	// AUTO-FIX-2026-06-26: 修正偏移错位，每区28B（与Marshal一致），原35导致越界与跳区
	for i := 0; i < areaCount; i++ {
		if offset+28 > len(data) {
			return fmt.Errorf("RectAreaSet: expected %d areas, got %d: %w", areaCount, i, ErrDataTooShort)
		}
		var area RectArea
		area.AreaID = uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
		topLatRaw := uint32(data[offset+4])<<24 | uint32(data[offset+5])<<16 | uint32(data[offset+6])<<8 | uint32(data[offset+7])
		area.TopLat = float64(topLatRaw) / JT808CoordScaleFactor
		topLonRaw := uint32(data[offset+8])<<24 | uint32(data[offset+9])<<16 | uint32(data[offset+10])<<8 | uint32(data[offset+11])
		area.TopLon = float64(topLonRaw) / JT808CoordScaleFactor
		botLatRaw := uint32(data[offset+12])<<24 | uint32(data[offset+13])<<16 | uint32(data[offset+14])<<8 | uint32(data[offset+15])
		area.BottomLat = float64(botLatRaw) / JT808CoordScaleFactor
		botLonRaw := uint32(data[offset+16])<<24 | uint32(data[offset+17])<<16 | uint32(data[offset+18])<<8 | uint32(data[offset+19])
		area.BottomLon = float64(botLonRaw) / JT808CoordScaleFactor
		area.SpeedLimit = uint16(data[offset+20])<<8 | uint16(data[offset+21])
		area.Duration = uint16(data[offset+22])<<8 | uint16(data[offset+23])
		area.MaxSpeed = uint16(data[offset+24])<<8 | uint16(data[offset+25])
		area.NightMaxSpeed = uint16(data[offset+26])<<8 | uint16(data[offset+27])
		m.Areas = append(m.Areas, area)
		offset += 28
	}
	return nil
}

type RectAreaDelMessage struct {
	AreaIDs []uint32
}

func (m *RectAreaDelMessage) MsgID() uint16 { return MsgIDRectAreaDel }

func (m *RectAreaDelMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 2+len(m.AreaIDs)*4)
	buf = append(buf, byte(len(m.AreaIDs)>>8), byte(len(m.AreaIDs)))
	for _, id := range m.AreaIDs {
		buf = append(buf, byte(id>>24), byte(id>>16), byte(id>>8), byte(id))
	}
	return buf, nil
}

func (m *RectAreaDelMessage) Unmarshal(data []byte) error {
	if len(data) < 2 {
		return ErrDataTooShort
	}
	count := int(uint16(data[0])<<8 | uint16(data[1]))
	m.AreaIDs = make([]uint32, 0, count)
	offset := 2
	for i := 0; i < count && offset+4 <= len(data); i++ {
		id := uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
		m.AreaIDs = append(m.AreaIDs, id)
		offset += 4
	}
	return nil
}

type PolygonAreaSetMessage struct {
	AreaID    uint32
	SpeedLimit uint16
	Duration  uint16
	MaxSpeed  uint16
	NightMaxSpeed uint16
	Points    []PolygonPoint
}

type PolygonPoint struct {
	Latitude  float64
	Longitude float64
}

func (m *PolygonAreaSetMessage) MsgID() uint16 { return MsgIDPolygonAreaSet }

func (m *PolygonAreaSetMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 14+len(m.Points)*8)
	buf = append(buf, byte(m.AreaID>>24), byte(m.AreaID>>16), byte(m.AreaID>>8), byte(m.AreaID))
	buf = append(buf, byte(m.SpeedLimit>>8), byte(m.SpeedLimit))
	buf = append(buf, byte(m.Duration>>8), byte(m.Duration))
	buf = append(buf, byte(m.MaxSpeed>>8), byte(m.MaxSpeed))
	buf = append(buf, byte(m.NightMaxSpeed>>8), byte(m.NightMaxSpeed))
	buf = append(buf, byte(len(m.Points)>>8), byte(len(m.Points)))
	for _, pt := range m.Points {
		// [P0-修复] 负坐标处理：多边形顶点可能为南纬/西经负值
		absLat := pt.Latitude
		if absLat < 0 {
			absLat = -absLat
		}
		absLon := pt.Longitude
		if absLon < 0 {
			absLon = -absLon
		}
		if absLat > 90.0 {
		return nil, fmt.Errorf("latitude %.6f exceeds ±90 range", absLat)
	}
	lat := uint32(absLat * JT808CoordScaleFactor)
		if absLon > 180.0 {
		return nil, fmt.Errorf("longitude %.6f exceeds ±180 range", absLon)
	}
	lon := uint32(absLon * JT808CoordScaleFactor)
		buf = append(buf, byte(lat>>24), byte(lat>>16), byte(lat>>8), byte(lat))
		buf = append(buf, byte(lon>>24), byte(lon>>16), byte(lon>>8), byte(lon))
	}
	return buf, nil
}

func (m *PolygonAreaSetMessage) Unmarshal(data []byte) error {
	if len(data) < 14 {
		return ErrDataTooShort
	}
	m.AreaID = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	m.SpeedLimit = uint16(data[4])<<8 | uint16(data[5])
	m.Duration = uint16(data[6])<<8 | uint16(data[7])
	m.MaxSpeed = uint16(data[8])<<8 | uint16(data[9])
	m.NightMaxSpeed = uint16(data[10])<<8 | uint16(data[11])
	ptCount := int(uint16(data[12])<<8 | uint16(data[13]))
	if ptCount > protocol.MaxElementCount {
		return fmt.Errorf("PolygonAreaSet: point count %d exceeds max %d", ptCount, protocol.MaxElementCount)
	}
	m.Points = make([]PolygonPoint, 0, ptCount)
	offset := 14
	for i := 0; i < ptCount; i++ {
		if offset+8 > len(data) {
			return fmt.Errorf("PolygonAreaSet: expected %d points, got %d: %w", ptCount, i, ErrDataTooShort)
		}
		var pt PolygonPoint
		latRaw := uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
		pt.Latitude = float64(latRaw) / JT808CoordScaleFactor
		lonRaw := uint32(data[offset+4])<<24 | uint32(data[offset+5])<<16 | uint32(data[offset+6])<<8 | uint32(data[offset+7])
		pt.Longitude = float64(lonRaw) / JT808CoordScaleFactor
		m.Points = append(m.Points, pt)
		offset += 8
	}
	return nil
}

type PolygonAreaDelMessage struct {
	AreaIDs []uint32
}

func (m *PolygonAreaDelMessage) MsgID() uint16 { return MsgIDPolygonAreaDel }

func (m *PolygonAreaDelMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 2+len(m.AreaIDs)*4)
	buf = append(buf, byte(len(m.AreaIDs)>>8), byte(len(m.AreaIDs)))
	for _, id := range m.AreaIDs {
		buf = append(buf, byte(id>>24), byte(id>>16), byte(id>>8), byte(id))
	}
	return buf, nil
}

func (m *PolygonAreaDelMessage) Unmarshal(data []byte) error {
	if len(data) < 2 {
		return ErrDataTooShort
	}
	count := int(uint16(data[0])<<8 | uint16(data[1]))
	m.AreaIDs = make([]uint32, 0, count)
	offset := 2
	for i := 0; i < count && offset+4 <= len(data); i++ {
		id := uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
		m.AreaIDs = append(m.AreaIDs, id)
		offset += 4
	}
	return nil
}

type RouteSetMessage struct {
	RouteID    uint32
	RouteName  string
	DepartTime uint16
	DrivingTime uint16
	Points     []RoutePoint
}

type RoutePoint struct {
	PointID    uint32
	RouteID    uint32
	Latitude   float64
	Longitude  float64
	Width      uint32
	Attr       byte
	SpeedLimit uint16
	Duration   uint16
	MaxSpeed   uint16
	NightMaxSpeed uint16
}

func (m *RouteSetMessage) MsgID() uint16 { return MsgIDRouteSet }

func (m *RouteSetMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 20+len(m.Points)*30)
	buf = append(buf, byte(m.RouteID>>24), byte(m.RouteID>>16), byte(m.RouteID>>8), byte(m.RouteID))
	nameBytes := []byte(m.RouteName)
	buf = append(buf, byte(len(nameBytes)))
	buf = append(buf, nameBytes...)
	buf = append(buf, byte(m.DepartTime>>8), byte(m.DepartTime))
	buf = append(buf, byte(m.DrivingTime>>8), byte(m.DrivingTime))
	buf = append(buf, byte(len(m.Points)>>8), byte(len(m.Points)))
	for _, pt := range m.Points {
		buf = append(buf, byte(pt.PointID>>24), byte(pt.PointID>>16), byte(pt.PointID>>8), byte(pt.PointID))
		buf = append(buf, byte(pt.RouteID>>24), byte(pt.RouteID>>16), byte(pt.RouteID>>8), byte(pt.RouteID))
		// [P0-修复] 负坐标处理
		absLat := pt.Latitude
		if absLat < 0 {
			absLat = -absLat
		}
		absLon := pt.Longitude
		if absLon < 0 {
			absLon = -absLon
		}
		if absLat > 90.0 {
		return nil, fmt.Errorf("latitude %.6f exceeds ±90 range", absLat)
	}
	lat := uint32(absLat * JT808CoordScaleFactor)
		if absLon > 180.0 {
		return nil, fmt.Errorf("longitude %.6f exceeds ±180 range", absLon)
	}
	lon := uint32(absLon * JT808CoordScaleFactor)
		buf = append(buf, byte(lat>>24), byte(lat>>16), byte(lat>>8), byte(lat))
		buf = append(buf, byte(lon>>24), byte(lon>>16), byte(lon>>8), byte(lon))
		buf = append(buf, byte(pt.Width>>24), byte(pt.Width>>16), byte(pt.Width>>8), byte(pt.Width))
		buf = append(buf, pt.Attr)
		buf = append(buf, byte(pt.SpeedLimit>>8), byte(pt.SpeedLimit))
		buf = append(buf, byte(pt.Duration>>8), byte(pt.Duration))
		buf = append(buf, byte(pt.MaxSpeed>>8), byte(pt.MaxSpeed))
		buf = append(buf, byte(pt.NightMaxSpeed>>8), byte(pt.NightMaxSpeed))
	}
	return buf, nil
}

func (m *RouteSetMessage) Unmarshal(data []byte) error {
	if len(data) < 10 {
		return ErrDataTooShort
	}
	m.RouteID = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	nameLen := int(data[4])
	offset := 5
	if offset+nameLen > len(data) {
		return ErrDataTooShort
	}
	m.RouteName = string(data[offset : offset+nameLen])
	offset += nameLen
	if offset+4 > len(data) {
		return ErrDataTooShort
	}
	m.DepartTime = uint16(data[offset])<<8 | uint16(data[offset+1])
	m.DrivingTime = uint16(data[offset+2])<<8 | uint16(data[offset+3])
	offset += 4
	if offset+2 > len(data) {
		return ErrDataTooShort
	}
	ptCount := int(uint16(data[offset])<<8 | uint16(data[offset+1]))
	if ptCount > protocol.MaxElementCount {
		return fmt.Errorf("RouteSet: point count %d exceeds max %d", ptCount, protocol.MaxElementCount)
	}
	offset += 2
	m.Points = make([]RoutePoint, 0, ptCount)
	// AUTO-FIX-2026-06-26: 修正路段偏移off-by-one，每路段29B（与Marshal一致），原30导致跳段
	for i := 0; i < ptCount; i++ {
		if offset+29 > len(data) {
			return fmt.Errorf("RouteSet: expected %d points, got %d: %w", ptCount, i, ErrDataTooShort)
		}
		var pt RoutePoint
		pt.PointID = uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
		pt.RouteID = uint32(data[offset+4])<<24 | uint32(data[offset+5])<<16 | uint32(data[offset+6])<<8 | uint32(data[offset+7])
		latRaw := uint32(data[offset+8])<<24 | uint32(data[offset+9])<<16 | uint32(data[offset+10])<<8 | uint32(data[offset+11])
		pt.Latitude = float64(latRaw) / JT808CoordScaleFactor
		lonRaw := uint32(data[offset+12])<<24 | uint32(data[offset+13])<<16 | uint32(data[offset+14])<<8 | uint32(data[offset+15])
		pt.Longitude = float64(lonRaw) / JT808CoordScaleFactor
		pt.Width = uint32(data[offset+16])<<24 | uint32(data[offset+17])<<16 | uint32(data[offset+18])<<8 | uint32(data[offset+19])
		pt.Attr = data[offset+20]
		pt.SpeedLimit = uint16(data[offset+21])<<8 | uint16(data[offset+22])
		pt.Duration = uint16(data[offset+23])<<8 | uint16(data[offset+24])
		pt.MaxSpeed = uint16(data[offset+25])<<8 | uint16(data[offset+26])
		pt.NightMaxSpeed = uint16(data[offset+27])<<8 | uint16(data[offset+28])
		m.Points = append(m.Points, pt)
		offset += 29
	}
	return nil
}

type RouteDelMessage struct {
	RouteIDs []uint32
}

func (m *RouteDelMessage) MsgID() uint16 { return MsgIDRouteDel }

func (m *RouteDelMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 2+len(m.RouteIDs)*4)
	buf = append(buf, byte(len(m.RouteIDs)>>8), byte(len(m.RouteIDs)))
	for _, id := range m.RouteIDs {
		buf = append(buf, byte(id>>24), byte(id>>16), byte(id>>8), byte(id))
	}
	return buf, nil
}

func (m *RouteDelMessage) Unmarshal(data []byte) error {
	if len(data) < 2 {
		return ErrDataTooShort
	}
	count := int(uint16(data[0])<<8 | uint16(data[1]))
	m.RouteIDs = make([]uint32, 0, count)
	offset := 2
	for i := 0; i < count && offset+4 <= len(data); i++ {
		id := uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
		m.RouteIDs = append(m.RouteIDs, id)
		offset += 4
	}
	return nil
}


type DriverIDMessage struct {
	Status    byte
	Time      string
	DriverID  string
}

func (m *DriverIDMessage) MsgID() uint16 { return MsgIDDriverID }

func (m *DriverIDMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 30)
	buf = append(buf, m.Status)
	timeBCD, err := StringToBCD6(m.Time)
	if err != nil {
		return nil, err
	}
	buf = append(buf, timeBCD...)
	buf = append(buf, []byte(m.DriverID)...)
	return buf, nil
}

func (m *DriverIDMessage) Unmarshal(data []byte) error {
	if len(data) < 7 {
		return ErrDataTooShort
	}
	m.Status = data[0]
	m.Time = BCDToStringSafe(data[1:7]) // AUTO-FIX-2026-06-26: 时间字段保留前导零
	if len(data) > 7 {
		m.DriverID = string(data[7:])
	}
	return nil
}

type CanDataMessage struct {
	ReceiveTime string
	CanCount    byte
	CanItems    []CanItem
}

type CanItem struct {
	CANID  uint32
	Data   []byte
}

func (m *CanDataMessage) MsgID() uint16 { return MsgIDCanData }

func (m *CanDataMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 6+len(m.CanItems)*10)
	timeBCD, err := StringToBCD6(m.ReceiveTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, timeBCD...)
	buf = append(buf, m.CanCount)
	for _, item := range m.CanItems {
		buf = append(buf, byte(item.CANID>>24), byte(item.CANID>>16), byte(item.CANID>>8), byte(item.CANID))
		buf = append(buf, byte(len(item.Data)))
		buf = append(buf, item.Data...)
	}
	return buf, nil
}

func (m *CanDataMessage) Unmarshal(data []byte) error {
	if len(data) < 7 {
		return ErrDataTooShort
	}
	m.ReceiveTime = BCDToStringSafe(data[0:6]) // AUTO-FIX-2026-06-26: 时间字段保留前导零
	m.CanCount = data[6]
	if int(m.CanCount) > protocol.MaxElementCount {
		return fmt.Errorf("CanData: count %d exceeds max %d", m.CanCount, protocol.MaxElementCount)
	}
	m.CanItems = make([]CanItem, 0, m.CanCount)
	offset := 7
	for i := 0; i < int(m.CanCount); i++ {
		if offset+5 > len(data) {
			return fmt.Errorf("CanData: expected %d items, got %d: %w", m.CanCount, i, ErrDataTooShort)
		}
		var item CanItem
		item.CANID = uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
		dataLen := int(data[offset+4])
		offset += 5
		if offset+dataLen > len(data) {
			return fmt.Errorf("CanData item %d: data too short: %w", i, ErrDataTooShort)
		}
		item.Data = make([]byte, dataLen)
		copy(item.Data, data[offset:offset+dataLen])
		m.CanItems = append(m.CanItems, item)
		offset += dataLen
	}
	return nil
}

type ElectronicWaybillMessage struct {
	WaybillData []byte
	// AUTO-FIX-2026-06-28: 新增 Content 字段，Unmarshal 时将 WaybillData 按 GBK 解码为字符串
	// Marshal 时若 Content 非空则按 GBK 编码覆盖 WaybillData（保留原字段以向后兼容）
	Content string
}

func (m *ElectronicWaybillMessage) MsgID() uint16 { return MsgIDElectronicWaybill }

func (m *ElectronicWaybillMessage) Marshal() ([]byte, error) {
	// AUTO-FIX-2026-06-26: 标准格式为 4B长度前缀 + 内容
	// AUTO-FIX-2026-06-28: 若 Content 非空，按 GBK 编码生成 WaybillData
	payload := m.WaybillData
	if m.Content != "" {
		enc := simplifiedchinese.GBK.NewEncoder()
		encoded, err := enc.Bytes([]byte(m.Content))
		if err != nil {
			return nil, fmt.Errorf("gbk encode waybill content: %w", err)
		}
		payload = encoded
	}
	buf := make([]byte, 0, 4+len(payload))
	l := uint32(len(payload))
	buf = append(buf, byte(l>>24), byte(l>>16), byte(l>>8), byte(l))
	buf = append(buf, payload...)
	return buf, nil
}

func (m *ElectronicWaybillMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-06-26: 先读4B长度再按长度读取内容
	if len(data) < 4 {
		return ErrDataTooShort
	}
	l := uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	if len(data) < 4+int(l) {
		return ErrDataTooShort
	}
	m.WaybillData = make([]byte, l)
	copy(m.WaybillData, data[4:4+l])
	// AUTO-FIX-2026-06-28: 将原始字节按 GBK 解码为字符串
	if len(m.WaybillData) > 0 {
		dec := simplifiedchinese.GBK.NewDecoder()
		if decoded, err := dec.String(string(m.WaybillData)); err == nil {
			m.Content = decoded
		} else {
			// GBK 解码失败时保留原始字符串（兼容非 GBK 内容）
			m.Content = string(m.WaybillData)
		}
	}
	return nil
}

type InfoMenuRespMessage struct {
	InfoType byte
	InfoID   uint32
	InfoData []byte
}

func (m *InfoMenuRespMessage) MsgID() uint16 { return MsgIDInfoMenuResp }

func (m *InfoMenuRespMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 5+len(m.InfoData))
	buf = append(buf, m.InfoType)
	buf = append(buf, byte(m.InfoID>>24), byte(m.InfoID>>16), byte(m.InfoID>>8), byte(m.InfoID))
	buf = append(buf, m.InfoData...)
	return buf, nil
}

func (m *InfoMenuRespMessage) Unmarshal(data []byte) error {
	if len(data) < 5 {
		return ErrDataTooShort
	}
	m.InfoType = data[0]
	m.InfoID = uint32(data[1])<<24 | uint32(data[2])<<16 | uint32(data[3])<<8 | uint32(data[4])
	if len(data) > 5 {
		m.InfoData = make([]byte, len(data)-5)
		copy(m.InfoData, data[5:])
	}
	return nil
}

type TerminalCtrlMessage struct {
	CtrlType byte
	Param    []byte
}

func (m *TerminalCtrlMessage) MsgID() uint16 { return MsgIDTerminalCtrl }

func (m *TerminalCtrlMessage) Marshal() ([]byte, error) {
	buf := []byte{m.CtrlType}
	buf = append(buf, m.Param...)
	return buf, nil
}

func (m *TerminalCtrlMessage) Unmarshal(data []byte) error {
	if len(data) < 1 {
		return ErrDataTooShort
	}
	m.CtrlType = data[0]
	if len(data) > 1 {
		m.Param = make([]byte, len(data)-1)
		copy(m.Param, data[1:])
	}
	return nil
}

// AUTO-FIX-2026-06-26: 补充车辆控制消息 0x8500 结构体与编解码方法
// 车辆控制（平台→终端）消息体仅 1 字节控制类型：
//   0x01 车辆锁定 / 0x02 车辆解锁 / 0x03 断油断电 / 0x04 恢复油电
type VehicleControlMessage struct {
	ControlType byte
}

func (m *VehicleControlMessage) MsgID() uint16 { return MsgIDVehicleControl }

func (m *VehicleControlMessage) Marshal() ([]byte, error) {
	return []byte{m.ControlType}, nil
}

func (m *VehicleControlMessage) Unmarshal(data []byte) error {
	if len(data) < 1 {
		return ErrDataTooShort
	}
	m.ControlType = data[0]
	return nil
}

type TerminalPropRespMessage struct {
	PropType  byte
	Manufacturer string
	Model     string
	ID        string
	ICCID     string
	HardwareVer string
	FirmwareVer string
	GNSSProp  byte
	CommProp  byte
}

func (m *TerminalPropRespMessage) MsgID() uint16 { return MsgIDTerminalPropResp }

func (m *TerminalPropRespMessage) Marshal() ([]byte, error) {
	buf := []byte{m.PropType}
	buf = append(buf, m.Manufacturer...)
	buf = append(buf, 0x00)
	buf = append(buf, m.Model...)
	buf = append(buf, 0x00)
	buf = append(buf, m.ID...)
	buf = append(buf, 0x00)
	buf = append(buf, m.ICCID...)
	buf = append(buf, 0x00)
	buf = append(buf, m.HardwareVer...)
	buf = append(buf, 0x00)
	buf = append(buf, m.FirmwareVer...)
	buf = append(buf, 0x00)
	buf = append(buf, m.GNSSProp)
	buf = append(buf, m.CommProp)
	return buf, nil
}

func (m *TerminalPropRespMessage) Unmarshal(data []byte) error {
	if len(data) < 2 {
		return ErrDataTooShort
	}
	m.PropType = data[0]
	parts := splitNullFields(data[1:])
	// FIXED-2026-07-23 [P2]: 验证字段数量不超过 8
	if len(parts) > 8 {
		return fmt.Errorf("TerminalPropResp: too many fields %d (max 8)", len(parts))
	}
	if len(parts) > 0 {
		m.Manufacturer = parts[0]
	}
	if len(parts) > 1 {
		m.Model = parts[1]
	}
	if len(parts) > 2 {
		m.ID = parts[2]
	}
	if len(parts) > 3 {
		m.ICCID = parts[3]
	}
	if len(parts) > 4 {
		m.HardwareVer = parts[4]
	}
	if len(parts) > 5 {
		m.FirmwareVer = parts[5]
	}
	if len(parts) > 6 && len(parts[6]) > 0 {
		m.GNSSProp = parts[6][0]
	}
	if len(parts) > 6 && len(parts[6]) > 1 {
		m.CommProp = parts[6][1]
	}
	return nil
}

type OverspeedSetMessage struct {
	ID       uint16
	SpeedLimit uint16
	Duration uint16
	NightSpeed uint16
	AreaCount byte
	Areas    []OverspeedArea
}

type OverspeedArea struct {
	AreaType byte
	ID       uint32
	Lat      float64
	Lon      float64
	Radius   uint32
	MaxSpeed uint16
	SpeedDur uint16
	NightMax uint16
	NightDur uint16
}

func (m *OverspeedSetMessage) MsgID() uint16 { return MsgIDOverspeedSet }

func (m *OverspeedSetMessage) Marshal() ([]byte, error) {
	// AUTO-FIX-2026-06-26: 修正buffer重叠覆盖（原buf[5]被Duration与NightSpeed双写，buf[6]被NightSpeed与AreaCount双写）
	buf := make([]byte, 9) // ID(2)+SpeedLimit(2)+Duration(2)+NightSpeed(2)+AreaCount(1)=9
	binary.BigEndian.PutUint16(buf[0:2], m.ID)
	binary.BigEndian.PutUint16(buf[2:4], m.SpeedLimit)
	binary.BigEndian.PutUint16(buf[4:6], m.Duration)
	binary.BigEndian.PutUint16(buf[6:8], m.NightSpeed)
	buf[8] = m.AreaCount
	for _, a := range m.Areas {
		ab := make([]byte, 27)
		ab[0] = a.AreaType
		binary.BigEndian.PutUint32(ab[1:5], a.ID)
		// [P0-修复] 负坐标处理
		absLat := a.Lat
		if absLat < 0 {
			absLat = -absLat
		}
		absLon := a.Lon
		if absLon < 0 {
			absLon = -absLon
		}
		if absLat > 90.0 {
		return nil, fmt.Errorf("latitude %.6f exceeds ±90 range", absLat)
	}
	lat := uint32(absLat * JT808CoordScaleFactor)
		if absLon > 180.0 {
		return nil, fmt.Errorf("longitude %.6f exceeds ±180 range", absLon)
	}
	lon := uint32(absLon * JT808CoordScaleFactor)
		binary.BigEndian.PutUint32(ab[5:9], lat)
		binary.BigEndian.PutUint32(ab[9:13], lon)
		binary.BigEndian.PutUint32(ab[13:17], a.Radius)
		binary.BigEndian.PutUint16(ab[17:19], a.MaxSpeed)
		binary.BigEndian.PutUint16(ab[19:21], a.SpeedDur)
		binary.BigEndian.PutUint16(ab[21:23], a.NightMax)
		binary.BigEndian.PutUint16(ab[23:25], a.NightDur)
		buf = append(buf, ab...)
	}
	return buf, nil
}

func (m *OverspeedSetMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-06-26: 修正buffer重叠覆盖，最小长度9字节
	if len(data) < 9 {
		return ErrDataTooShort
	}
	m.ID = binary.BigEndian.Uint16(data[0:2])
	m.SpeedLimit = binary.BigEndian.Uint16(data[2:4])
	m.Duration = binary.BigEndian.Uint16(data[4:6])
	m.NightSpeed = binary.BigEndian.Uint16(data[6:8])
	m.AreaCount = data[8]
	offset := 9
	for i := 0; i < int(m.AreaCount) && offset+27 <= len(data); i++ {
		var a OverspeedArea
		a.AreaType = data[offset]
		a.ID = binary.BigEndian.Uint32(data[offset+1 : offset+5])
		a.Lat = float64(binary.BigEndian.Uint32(data[offset+5:offset+9])) / JT808CoordScaleFactor
		a.Lon = float64(binary.BigEndian.Uint32(data[offset+9:offset+13])) / JT808CoordScaleFactor
		a.Radius = binary.BigEndian.Uint32(data[offset+13 : offset+17])
		a.MaxSpeed = binary.BigEndian.Uint16(data[offset+17 : offset+19])
		a.SpeedDur = binary.BigEndian.Uint16(data[offset+19 : offset+21])
		a.NightMax = binary.BigEndian.Uint16(data[offset+21 : offset+23])
		a.NightDur = binary.BigEndian.Uint16(data[offset+23 : offset+25])
		m.Areas = append(m.Areas, a)
		offset += 27
	}
	return nil
}

type FatigueDriveSetMessage struct {
	ID       uint16
	Threshold uint16
	Duration uint16
	AreaCount byte
	Areas    []FatigueArea
}

type FatigueArea struct {
	AreaType byte
	ID       uint32
	Lat      float64
	Lon      float64
	Radius   uint32
	MaxDrive uint16
	MinRest  uint16
}

func (m *FatigueDriveSetMessage) MsgID() uint16 { return MsgIDFatigueDriveSet }

func (m *FatigueDriveSetMessage) Marshal() ([]byte, error) {
	// AUTO-FIX-2026-06-26: 修正buffer重叠覆盖（原buf[5]被Duration与AreaCount双写）
	buf := make([]byte, 7) // ID(2)+Threshold(2)+Duration(2)+AreaCount(1)=7
	binary.BigEndian.PutUint16(buf[0:2], m.ID)
	binary.BigEndian.PutUint16(buf[2:4], m.Threshold)
	binary.BigEndian.PutUint16(buf[4:6], m.Duration)
	buf[6] = m.AreaCount
	for _, a := range m.Areas {
		ab := make([]byte, 21)
		ab[0] = a.AreaType
		binary.BigEndian.PutUint32(ab[1:5], a.ID)
		// [P0-修复] 负坐标处理
		absLat := a.Lat
		if absLat < 0 {
			absLat = -absLat
		}
		absLon := a.Lon
		if absLon < 0 {
			absLon = -absLon
		}
		if absLat > 90.0 {
		return nil, fmt.Errorf("latitude %.6f exceeds ±90 range", absLat)
	}
	lat := uint32(absLat * JT808CoordScaleFactor)
		if absLon > 180.0 {
		return nil, fmt.Errorf("longitude %.6f exceeds ±180 range", absLon)
	}
	lon := uint32(absLon * JT808CoordScaleFactor)
		binary.BigEndian.PutUint32(ab[5:9], lat)
		binary.BigEndian.PutUint32(ab[9:13], lon)
		binary.BigEndian.PutUint32(ab[13:17], a.Radius)
		binary.BigEndian.PutUint16(ab[17:19], a.MaxDrive)
		binary.BigEndian.PutUint16(ab[19:21], a.MinRest)
		buf = append(buf, ab...)
	}
	return buf, nil
}

func (m *FatigueDriveSetMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-06-26: 修正buffer重叠覆盖，最小长度7字节
	if len(data) < 7 {
		return ErrDataTooShort
	}
	m.ID = binary.BigEndian.Uint16(data[0:2])
	m.Threshold = binary.BigEndian.Uint16(data[2:4])
	m.Duration = binary.BigEndian.Uint16(data[4:6])
	m.AreaCount = data[6]
	offset := 7
	for i := 0; i < int(m.AreaCount) && offset+21 <= len(data); i++ {
		var a FatigueArea
		a.AreaType = data[offset]
		a.ID = binary.BigEndian.Uint32(data[offset+1 : offset+5])
		a.Lat = float64(binary.BigEndian.Uint32(data[offset+5:offset+9])) / JT808CoordScaleFactor
		a.Lon = float64(binary.BigEndian.Uint32(data[offset+9:offset+13])) / JT808CoordScaleFactor
		a.Radius = binary.BigEndian.Uint32(data[offset+13 : offset+17])
		a.MaxDrive = binary.BigEndian.Uint16(data[offset+17 : offset+19])
		a.MinRest = binary.BigEndian.Uint16(data[offset+19 : offset+21])
		m.Areas = append(m.Areas, a)
		offset += 21
	}
	return nil
}

type SMSForwardRespMessage struct {
	SMSContent []byte
}

func (m *SMSForwardRespMessage) MsgID() uint16 { return MsgIDSMSForwardResp }

func (m *SMSForwardRespMessage) Marshal() ([]byte, error) {
	// AUTO-FIX-2026-06-26: 标准格式为 2B长度前缀 + 内容（0x0703定时短消息上传）
	buf := make([]byte, 0, 2+len(m.SMSContent))
	l := uint16(len(m.SMSContent))
	buf = append(buf, byte(l>>8), byte(l))
	buf = append(buf, m.SMSContent...)
	return buf, nil
}

func (m *SMSForwardRespMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-06-26: 先读2B长度再按长度读取内容（0x0703定时短消息上传）
	if len(data) < 2 {
		return ErrDataTooShort
	}
	l := int(uint16(data[0])<<8 | uint16(data[1]))
	if len(data) < 2+l {
		return ErrDataTooShort
	}
	m.SMSContent = make([]byte, l)
	copy(m.SMSContent, data[2:2+l])
	return nil
}

type EventRespMessage struct {
	// AUTO-FIX-2026-06-27: 0x0301 标准格式 EventID 为 uint16(2B)，原 uint32(4B) 错位
	EventID uint16
}

func (m *EventRespMessage) MsgID() uint16 { return MsgIDEventResp }

// AUTO-FIX-2026-06-27: EventID 改为 2B，Marshal 输出 2 字节
func (m *EventRespMessage) Marshal() ([]byte, error) {
	return []byte{byte(m.EventID >> 8), byte(m.EventID)}, nil
}

func (m *EventRespMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-06-27: EventID 改为 2B，最小长度由 4 收紧为 2
	if len(data) < 2 {
		return ErrDataTooShort
	}
	m.EventID = uint16(data[0])<<8 | uint16(data[1])
	return nil
}

type QuestionRespMessage struct {
	// AUTO-FIX-2026-06-27: 0x0302 新增 RespSeqNum(2B) 字段，体为 RespSeqNum(2B)+AnswerID(2B)
	RespSeqNum uint16
	AnswerID   uint16
}

func (m *QuestionRespMessage) MsgID() uint16 { return MsgIDQuestionResp }

// AUTO-FIX-2026-06-27: 体改为 RespSeqNum(2B)+AnswerID(2B)，共4B
func (m *QuestionRespMessage) Marshal() ([]byte, error) {
	return []byte{
		byte(m.RespSeqNum >> 8), byte(m.RespSeqNum),
		byte(m.AnswerID >> 8), byte(m.AnswerID),
	}, nil
}

func (m *QuestionRespMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-06-27: 最小长度由 2 改为 4
	if len(data) < 4 {
		return ErrDataTooShort
	}
	m.RespSeqNum = uint16(data[0])<<8 | uint16(data[1])
	m.AnswerID = uint16(data[2])<<8 | uint16(data[3])
	return nil
}

type CommandRespMessage struct {
	RespSeqNum uint16
	RespMsgID  uint16
	RespCount  byte
	Params     map[uint32][]byte
}

func (m *CommandRespMessage) MsgID() uint16 { return MsgIDCommandResp }

// AUTO-FIX-2026-06-27: 0x0103 标准体为 RespSeqNum(2B)+参数总数(1B)+参数项列表，删除原 RespMsgID(2B)
// FIXED-2026-07-23 [P1]: 确定性编码——按 paramID 排序后遍历，确保同一消息多次编码产生相同字节序列
func (m *CommandRespMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 3+100)
	buf = append(buf, byte(m.RespSeqNum>>8), byte(m.RespSeqNum))
	buf = append(buf, m.RespCount)
	// [P1-修复] 确定性编码：按 paramID 排序后遍历
	paramIDs := make([]uint32, 0, len(m.Params))
	for id := range m.Params {
		paramIDs = append(paramIDs, id)
	}
	sort.Slice(paramIDs, func(i, j int) bool { return paramIDs[i] < paramIDs[j] })
	for _, id := range paramIDs {
		val := m.Params[id]
		buf = append(buf, byte(id>>24), byte(id>>16), byte(id>>8), byte(id))
		buf = append(buf, byte(len(val)))
		buf = append(buf, val...)
	}
	return buf, nil
}

func (m *CommandRespMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-06-27: 标准0x0103格式: RespSeqNum(2B)+参数总数(1B)+参数项列表
	if len(data) < 3 {
		return ErrDataTooShort
	}
	m.RespSeqNum = uint16(data[0])<<8 | uint16(data[1])
	m.RespCount = data[2]
	m.Params = make(map[uint32][]byte)
	offset := 3
	for i := 0; i < int(m.RespCount); i++ {
		if offset+5 > len(data) {
			return fmt.Errorf("CommandResp: expected %d params, got %d: %w", m.RespCount, i, ErrDataTooShort)
		}
		paramID := uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
		paramLen := int(data[offset+4])
		offset += 5
		if offset+paramLen > len(data) {
			return fmt.Errorf("CommandResp param %d: data too short: %w", i, ErrDataTooShort)
		}
		val := make([]byte, paramLen)
		copy(val, data[offset:offset+paramLen])
		m.Params[paramID] = val
		offset += paramLen
	}
	return nil
}

// TempLocationTrackMessage 对应 0x8202 临时位置跟踪控制请求。
// 消息体: 时间间隔(uint16, 单位秒) + 位置跟踪有效期(uint16, 单位秒, 0表示一直跟踪)
type TempLocationTrackMessage struct {
	Interval uint16
	Validity uint16
}

func (m *TempLocationTrackMessage) MsgID() uint16 { return MsgIDTempLocationTrack }

func (m *TempLocationTrackMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 4)
	buf[0] = byte(m.Interval >> 8)
	buf[1] = byte(m.Interval)
	buf[2] = byte(m.Validity >> 8)
	buf[3] = byte(m.Validity)
	return buf, nil
}

func (m *TempLocationTrackMessage) Unmarshal(data []byte) error {
	if len(data) < 4 {
		return ErrDataTooShort
	}
	m.Interval = uint16(data[0])<<8 | uint16(data[1])
	m.Validity = uint16(data[2])<<8 | uint16(data[3])
	return nil
}

// ManualAlarmConfirmMessage 对应 0x8203 人工确认报警消息。
// 消息体: 报警标志(uint16, bit0=紧急报警 bit1=碰撞侧翻报警)
type ManualAlarmConfirmMessage struct {
	AlarmFlag uint16
}

// AUTO-FIX-2026-06-27: 0x8203 常量重命名 MsgIDSchedulePos → MsgIDManualAlarmConfirm
func (m *ManualAlarmConfirmMessage) MsgID() uint16 { return MsgIDManualAlarmConfirm }

func (m *ManualAlarmConfirmMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 2)
	buf[0] = byte(m.AlarmFlag >> 8)
	buf[1] = byte(m.AlarmFlag)
	return buf, nil
}

func (m *ManualAlarmConfirmMessage) Unmarshal(data []byte) error {
	if len(data) < 2 {
		return ErrDataTooShort
	}
	m.AlarmFlag = uint16(data[0])<<8 | uint16(data[1])
	return nil
}

var _ protocol.MessageBody = (*RawMessage)(nil)
var _ protocol.MessageBody = (*RegisterMessage)(nil)
var _ protocol.MessageBody = (*AuthMessage)(nil)
var _ protocol.MessageBody = (*HeartbeatMessage)(nil)
var _ protocol.MessageBody = (*LocationMessage)(nil)
var _ protocol.MessageBody = (*GeneralResponse)(nil)
var _ protocol.MessageBody = (*RegisterResponse)(nil)
var _ protocol.MessageBody = (*CommandMessage)(nil)
var _ protocol.MessageBody = (*TerminalCancelMessage)(nil)
var _ protocol.MessageBody = (*LocationBatchMessage)(nil)
var _ protocol.MessageBody = (*LocationQueryResponse)(nil)
var _ protocol.MessageBody = (*MultimediaMessage)(nil)
var _ protocol.MessageBody = (*MultimediaUploadMessage)(nil)
var _ protocol.MessageBody = (*CircularAreaSetMessage)(nil)
var _ protocol.MessageBody = (*CircularAreaDelMessage)(nil)
var _ protocol.MessageBody = (*RectAreaSetMessage)(nil)
var _ protocol.MessageBody = (*RectAreaDelMessage)(nil)
var _ protocol.MessageBody = (*PolygonAreaSetMessage)(nil)
var _ protocol.MessageBody = (*PolygonAreaDelMessage)(nil)
var _ protocol.MessageBody = (*RouteSetMessage)(nil)
var _ protocol.MessageBody = (*RouteDelMessage)(nil)
var _ protocol.MessageBody = (*TerminalCtrlMessage)(nil)
var _ protocol.MessageBody = (*TerminalPropRespMessage)(nil)
var _ protocol.MessageBody = (*OverspeedSetMessage)(nil)
var _ protocol.MessageBody = (*FatigueDriveSetMessage)(nil)
var _ protocol.MessageBody = (*DriverIDMessage)(nil)
var _ protocol.MessageBody = (*CanDataMessage)(nil)
var _ protocol.MessageBody = (*ElectronicWaybillMessage)(nil)
var _ protocol.MessageBody = (*InfoMenuRespMessage)(nil)
var _ protocol.MessageBody = (*TerminalCtrlMessage)(nil)
var _ protocol.MessageBody = (*VehicleControlMessage)(nil)
var _ protocol.MessageBody = (*TerminalPropRespMessage)(nil)
var _ protocol.MessageBody = (*OverspeedSetMessage)(nil)
var _ protocol.MessageBody = (*FatigueDriveSetMessage)(nil)
var _ protocol.MessageBody = (*SMSForwardRespMessage)(nil)
var _ protocol.MessageBody = (*EventRespMessage)(nil)
var _ protocol.MessageBody = (*QuestionRespMessage)(nil)
var _ protocol.MessageBody = (*CommandRespMessage)(nil)
var _ protocol.MessageBody = (*TempLocationTrackMessage)(nil)
var _ protocol.MessageBody = (*ManualAlarmConfirmMessage)(nil)
// AUTO-FIX-2026-06-28: 补全缺失的接口断言（原已实现三方法但未显式断言）
var _ protocol.MessageBody = (*TerminalCancelResponse)(nil)
var _ protocol.MessageBody = (*LocationQueryMessage)(nil)
var _ protocol.MessageBody = (*AlarmAttachmentMessage)(nil)

type TextSendMessage struct {
	Sign    byte
	Text    string
}

func (m *TextSendMessage) MsgID() uint16 { return 0x8300 }

func (m *TextSendMessage) Marshal() ([]byte, error) {
	// AUTO-FIX-2026-06-26: 移除多余长度前缀，标准0x8300为 Sign(1) + 文本内容(变长，长度由消息头BodyLen指定)
	buf := make([]byte, 0, len(m.Text)+1)
	buf = append(buf, m.Sign)
	buf = append(buf, []byte(m.Text)...)
	return buf, nil
}

func (m *TextSendMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-06-26: 移除多余长度前缀，标准0x8300为 Sign(1) + 文本内容(变长，长度由消息头BodyLen指定)
	if len(data) < 1 {
		return fmt.Errorf("text send message too short")
	}
	m.Sign = data[0]
	m.Text = string(data[1:])
	return nil
}

type AudioRecordCmdMessage struct {
	// AUTO-FIX-2026-06-27: 新增 RecordCmd(1B) 字段，体为 录音时间(2B)+录音命令(1B)+保存标志(1B)+音频采样率(1B) 共5B
	RecordTime  uint16
	RecordCmd   byte
	SaveFlag    byte
	AudioSample byte
}

func (m *AudioRecordCmdMessage) MsgID() uint16 { return 0x8804 }

// AUTO-FIX-2026-06-27: 体改为 录音时间(2B)+录音命令(1B)+保存标志(1B)+音频采样率(1B) 共5B
func (m *AudioRecordCmdMessage) Marshal() ([]byte, error) {
	return []byte{
		byte(m.RecordTime >> 8), byte(m.RecordTime),
		m.RecordCmd, m.SaveFlag, m.AudioSample,
	}, nil
}

func (m *AudioRecordCmdMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-06-27: 最小长度由 4 改为 5
	if len(data) < 5 {
		return fmt.Errorf("audio record cmd message too short")
	}
	m.RecordTime = uint16(data[0])<<8 | uint16(data[1])
	m.RecordCmd = data[2]
	m.SaveFlag = data[3]
	m.AudioSample = data[4]
	return nil
}

type PhotoCommandMessage struct {
	ChannelID   byte
	Cmd         byte
	Time        uint16
	SaveFlag    byte
	Resolution  byte
	Quality     byte
	Brightness  byte
	Contrast    byte
	Saturation  byte
	Chroma      byte
}

func (m *PhotoCommandMessage) MsgID() uint16 { return 0x8801 }

func (m *PhotoCommandMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 12)
	buf = append(buf, m.ChannelID)
	buf = append(buf, m.Cmd)
	buf = append(buf, byte(m.Time>>8), byte(m.Time))
	buf = append(buf, m.SaveFlag)
	buf = append(buf, m.Resolution, m.Quality, m.Brightness, m.Contrast, m.Saturation, m.Chroma)
	return buf, nil
}

func (m *PhotoCommandMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-07-04: 修正 off-by-one 错误，Marshal 产出 11 字节（1+1+2+1+1+1+1+1+1+1），
	// Unmarshal 需 ≥11 字节而非 ≥12。
	if len(data) < 11 {
		return fmt.Errorf("photo command message too short")
	}
	m.ChannelID = data[0]
	m.Cmd = data[1]
	m.Time = uint16(data[2])<<8 | uint16(data[3])
	m.SaveFlag = data[4]
	m.Resolution = data[5]
	m.Quality = data[6]
	m.Brightness = data[7]
	m.Contrast = data[8]
	m.Saturation = data[9]
	m.Chroma = data[10]
	return nil
}

type FireAreaAlarmMessage struct {
	AreaType byte
	AreaID   uint32
	Dir      byte
	// FIXED-2026-07-23 [P2]: Lat/Lng 改为 float64，与其他区域消息一致
	Lat      float64
	Lng      float64
}

func (m *FireAreaAlarmMessage) MsgID() uint16 { return 0x0500 }

func (m *FireAreaAlarmMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 14)
	buf = append(buf, m.AreaType)
	buf = append(buf, byte(m.AreaID>>24), byte(m.AreaID>>16), byte(m.AreaID>>8), byte(m.AreaID))
	buf = append(buf, m.Dir)
	// FIXED-2026-07-23 [P2]: 坐标转换为 uint32 编码，与 CircularAreaSetMessage 一致
	absLat := m.Lat
	if absLat < 0 {
		absLat = -absLat
	}
	absLon := m.Lng
	if absLon < 0 {
		absLon = -absLon
	}
	if absLat > 90.0 {
		return nil, fmt.Errorf("latitude %.6f exceeds ±90 range", absLat)
	}
	if absLon > 180.0 {
		return nil, fmt.Errorf("longitude %.6f exceeds ±180 range", absLon)
	}
	lat := uint32(absLat * JT808CoordScaleFactor)
	lon := uint32(absLon * JT808CoordScaleFactor)
	buf = append(buf, byte(lat>>24), byte(lat>>16), byte(lat>>8), byte(lat))
	buf = append(buf, byte(lon>>24), byte(lon>>16), byte(lon>>8), byte(lon))
	return buf, nil
}

func (m *FireAreaAlarmMessage) Unmarshal(data []byte) error {
	if len(data) < 14 {
		return fmt.Errorf("fire area alarm message too short")
	}
	m.AreaType = data[0]
	m.AreaID = uint32(data[1])<<24 | uint32(data[2])<<16 | uint32(data[3])<<8 | uint32(data[4])
	m.Dir = data[5]
	// FIXED-2026-07-23 [P2]: 坐标除以缩放因子转为 float64
	latRaw := uint32(data[6])<<24 | uint32(data[7])<<16 | uint32(data[8])<<8 | uint32(data[9])
	lngRaw := uint32(data[10])<<24 | uint32(data[11])<<16 | uint32(data[12])<<8 | uint32(data[13])
	m.Lat = float64(latRaw) / JT808CoordScaleFactor
	m.Lng = float64(lngRaw) / JT808CoordScaleFactor
	return nil
}

var _ protocol.MessageBody = (*TextSendMessage)(nil)
var _ protocol.MessageBody = (*AudioRecordCmdMessage)(nil)
var _ protocol.MessageBody = (*PhotoCommandMessage)(nil)
var _ protocol.MessageBody = (*FireAreaAlarmMessage)(nil)
var _ protocol.MessageBody = (*TerminalGeneralRespMessage)(nil)
var _ protocol.MessageBody = (*TerminalUpgradeRespMessage)(nil)
var _ protocol.MessageBody = (*AlarmMessage)(nil)
var _ protocol.MessageBody = (*AlarmAckMessage)(nil)
var _ protocol.MessageBody = (*FireAreaSetMessage)(nil)
var _ protocol.MessageBody = (*FireAreaDelMessage)(nil)
var _ protocol.MessageBody = (*StorageMediaSearchMessage)(nil)
var _ protocol.MessageBody = (*StorageMediaUploadMessage)(nil)
var _ protocol.MessageBody = (*PassengerCountMessage)(nil)
var _ protocol.MessageBody = (*BillOperateMessage)(nil)
var _ protocol.MessageBody = (*InfoMenuSetMessage)(nil)
var _ protocol.MessageBody = (*QuestionDownMessage)(nil)

type TerminalGeneralRespMessage struct {
	RespSeqNum uint16
	RespMsgID  uint16
	RespResult byte
}

func (m *TerminalGeneralRespMessage) MsgID() uint16 { return MsgIDTerminalGeneralResp }

func (m *TerminalGeneralRespMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 5)
	buf[0] = byte(m.RespSeqNum >> 8)
	buf[1] = byte(m.RespSeqNum)
	buf[2] = byte(m.RespMsgID >> 8)
	buf[3] = byte(m.RespMsgID)
	buf[4] = m.RespResult
	return buf, nil
}

func (m *TerminalGeneralRespMessage) Unmarshal(data []byte) error {
	if len(data) < 5 {
		return ErrDataTooShort
	}
	m.RespSeqNum = uint16(data[0])<<8 | uint16(data[1])
	m.RespMsgID = uint16(data[2])<<8 | uint16(data[3])
	m.RespResult = data[4]
	return nil
}

type TerminalUpgradeRespMessage struct {
	UpgradeType byte
	CompileLen  uint32
	ProvinceID  uint16
	CityID      uint16
	Manufacturer string
	Version     string
}

func (m *TerminalUpgradeRespMessage) MsgID() uint16 { return MsgIDTerminalUpgradeResp }

func (m *TerminalUpgradeRespMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 20)
	buf = append(buf, m.UpgradeType)
	buf = append(buf, byte(m.CompileLen>>24), byte(m.CompileLen>>16), byte(m.CompileLen>>8), byte(m.CompileLen))
	buf = append(buf, byte(m.ProvinceID>>8), byte(m.ProvinceID))
	buf = append(buf, byte(m.CityID>>8), byte(m.CityID))
	manu := []byte(m.Manufacturer)
	buf = append(buf, byte(len(manu)))
	buf = append(buf, manu...)
	buf = append(buf, []byte(m.Version)...)
	return buf, nil
}

func (m *TerminalUpgradeRespMessage) Unmarshal(data []byte) error {
	if len(data) < 10 {
		return ErrDataTooShort
	}
	m.UpgradeType = data[0]
	m.CompileLen = uint32(data[1])<<24 | uint32(data[2])<<16 | uint32(data[3])<<8 | uint32(data[4])
	m.ProvinceID = uint16(data[5])<<8 | uint16(data[6])
	m.CityID = uint16(data[7])<<8 | uint16(data[8])
	manuLen := int(data[9])
	offset := 10
	if offset+manuLen > len(data) {
		return ErrDataTooShort
	}
	m.Manufacturer = string(data[offset : offset+manuLen])
	offset += manuLen
	if offset < len(data) {
		m.Version = string(data[offset:])
	}
	return nil
}

type AlarmMessage struct {
	AlarmFlag  uint32
	AlarmCount uint16
	AlarmItems []AlarmItem
}

type AlarmItem struct {
	SeqNum    uint16
	AlarmType uint32
}

func (m *AlarmMessage) MsgID() uint16 { return MsgIDAlarm }

func (m *AlarmMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 6+len(m.AlarmItems)*6)
	buf = append(buf, byte(m.AlarmFlag>>24), byte(m.AlarmFlag>>16), byte(m.AlarmFlag>>8), byte(m.AlarmFlag))
	buf = append(buf, byte(m.AlarmCount>>8), byte(m.AlarmCount))
	for _, item := range m.AlarmItems {
		buf = append(buf, byte(item.SeqNum>>8), byte(item.SeqNum))
		buf = append(buf, byte(item.AlarmType>>24), byte(item.AlarmType>>16), byte(item.AlarmType>>8), byte(item.AlarmType))
	}
	return buf, nil
}

func (m *AlarmMessage) Unmarshal(data []byte) error {
	if len(data) < 6 {
		return ErrDataTooShort
	}
	m.AlarmFlag = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	m.AlarmCount = uint16(data[4])<<8 | uint16(data[5])
	m.AlarmItems = make([]AlarmItem, 0, m.AlarmCount)
	offset := 6
	for i := 0; i < int(m.AlarmCount) && offset+6 <= len(data); i++ {
		var item AlarmItem
		item.SeqNum = uint16(data[offset])<<8 | uint16(data[offset+1])
		item.AlarmType = uint32(data[offset+2])<<24 | uint32(data[offset+3])<<16 | uint32(data[offset+4])<<8 | uint32(data[offset+5])
		m.AlarmItems = append(m.AlarmItems, item)
		offset += 6
	}
	return nil
}

type AlarmAckMessage struct {
	RespSeqNum uint16
	AlarmType  uint32
	AlarmID    uint16
}

func (m *AlarmAckMessage) MsgID() uint16 { return MsgIDAlarmAck }

func (m *AlarmAckMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 8)
	buf[0] = byte(m.RespSeqNum >> 8)
	buf[1] = byte(m.RespSeqNum)
	buf[2] = byte(m.AlarmType >> 24)
	buf[3] = byte(m.AlarmType >> 16)
	buf[4] = byte(m.AlarmType >> 8)
	buf[5] = byte(m.AlarmType)
	buf[6] = byte(m.AlarmID >> 8)
	buf[7] = byte(m.AlarmID)
	return buf, nil
}

func (m *AlarmAckMessage) Unmarshal(data []byte) error {
	if len(data) < 8 {
		return ErrDataTooShort
	}
	m.RespSeqNum = uint16(data[0])<<8 | uint16(data[1])
	m.AlarmType = uint32(data[2])<<24 | uint32(data[3])<<16 | uint32(data[4])<<8 | uint32(data[5])
	m.AlarmID = uint16(data[6])<<8 | uint16(data[7])
	return nil
}

type FireAreaSetMessage struct {
	SetType byte
	Areas   []FireArea
}

type FireArea struct {
	AreaID       uint32
	CenterLat    float64
	CenterLon    float64
	Radius       uint32
	SpeedLimit   uint16
	Duration     uint16
	MaxSpeed     uint16
	NightMaxSpeed uint16
}

func (m *FireAreaSetMessage) MsgID() uint16 { return MsgIDFireAreaSet }

func (m *FireAreaSetMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 1+len(m.Areas)*27)
	buf = append(buf, m.SetType)
	buf = append(buf, byte(len(m.Areas)>>8), byte(len(m.Areas)))
	for _, area := range m.Areas {
		buf = append(buf, byte(area.AreaID>>24), byte(area.AreaID>>16), byte(area.AreaID>>8), byte(area.AreaID))
		// [P0-修复] 负坐标处理：南纬/西经为负值，uint32 转换前取绝对值
		absLat := area.CenterLat
		if absLat < 0 {
			absLat = -absLat
		}
		absLon := area.CenterLon
		if absLon < 0 {
			absLon = -absLon
		}
		if absLat > 90.0 {
		return nil, fmt.Errorf("latitude %.6f exceeds ±90 range", absLat)
	}
	lat := uint32(absLat * JT808CoordScaleFactor)
		if absLon > 180.0 {
		return nil, fmt.Errorf("longitude %.6f exceeds ±180 range", absLon)
	}
	lon := uint32(absLon * JT808CoordScaleFactor)
		buf = append(buf, byte(lat>>24), byte(lat>>16), byte(lat>>8), byte(lat))
		buf = append(buf, byte(lon>>24), byte(lon>>16), byte(lon>>8), byte(lon))
		buf = append(buf, byte(area.Radius>>24), byte(area.Radius>>16), byte(area.Radius>>8), byte(area.Radius))
		buf = append(buf, byte(area.SpeedLimit>>8), byte(area.SpeedLimit))
		buf = append(buf, byte(area.Duration>>8), byte(area.Duration))
		buf = append(buf, byte(area.MaxSpeed>>8), byte(area.MaxSpeed))
		buf = append(buf, byte(area.NightMaxSpeed>>8), byte(area.NightMaxSpeed))
	}
	return buf, nil
}

func (m *FireAreaSetMessage) Unmarshal(data []byte) error {
	if len(data) < 3 {
		return ErrDataTooShort
	}
	m.SetType = data[0]
	areaCount := int(uint16(data[1])<<8 | uint16(data[2]))
	m.Areas = make([]FireArea, 0, areaCount)
	offset := 3
	// AUTO-FIX-2026-06-26: 修正偏移错位，每区24B（与Marshal一致），原27导致跳区
	for i := 0; i < areaCount && offset+24 <= len(data); i++ {
		var area FireArea
		area.AreaID = uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
		latRaw := uint32(data[offset+4])<<24 | uint32(data[offset+5])<<16 | uint32(data[offset+6])<<8 | uint32(data[offset+7])
		area.CenterLat = float64(latRaw) / JT808CoordScaleFactor
		lonRaw := uint32(data[offset+8])<<24 | uint32(data[offset+9])<<16 | uint32(data[offset+10])<<8 | uint32(data[offset+11])
		area.CenterLon = float64(lonRaw) / JT808CoordScaleFactor
		area.Radius = uint32(data[offset+12])<<24 | uint32(data[offset+13])<<16 | uint32(data[offset+14])<<8 | uint32(data[offset+15])
		area.SpeedLimit = uint16(data[offset+16])<<8 | uint16(data[offset+17])
		area.Duration = uint16(data[offset+18])<<8 | uint16(data[offset+19])
		area.MaxSpeed = uint16(data[offset+20])<<8 | uint16(data[offset+21])
		area.NightMaxSpeed = uint16(data[offset+22])<<8 | uint16(data[offset+23])
		m.Areas = append(m.Areas, area)
		offset += 24
	}
	return nil
}

type FireAreaDelMessage struct {
	AreaIDs []uint32
}

func (m *FireAreaDelMessage) MsgID() uint16 { return MsgIDFireAreaDel }

func (m *FireAreaDelMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 2+len(m.AreaIDs)*4)
	buf = append(buf, byte(len(m.AreaIDs)>>8), byte(len(m.AreaIDs)))
	for _, id := range m.AreaIDs {
		buf = append(buf, byte(id>>24), byte(id>>16), byte(id>>8), byte(id))
	}
	return buf, nil
}

func (m *FireAreaDelMessage) Unmarshal(data []byte) error {
	if len(data) < 2 {
		return ErrDataTooShort
	}
	count := int(uint16(data[0])<<8 | uint16(data[1]))
	m.AreaIDs = make([]uint32, 0, count)
	offset := 2
	for i := 0; i < count && offset+4 <= len(data); i++ {
		id := uint32(data[offset])<<24 | uint32(data[offset+1])<<16 | uint32(data[offset+2])<<8 | uint32(data[offset+3])
		m.AreaIDs = append(m.AreaIDs, id)
		offset += 4
	}
	return nil
}

type StorageMediaSearchMessage struct {
	MultimediaID   uint32
	MultimediaType byte
	ChannelID      byte
	StartTime      string
	EndTime        string
}

func (m *StorageMediaSearchMessage) MsgID() uint16 { return MsgIDStorageMediaSearch }

func (m *StorageMediaSearchMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 18)
	buf = append(buf, byte(m.MultimediaID>>24), byte(m.MultimediaID>>16), byte(m.MultimediaID>>8), byte(m.MultimediaID))
	buf = append(buf, m.MultimediaType)
	buf = append(buf, m.ChannelID)
	startBCD, err := StringToBCD6(m.StartTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, startBCD...)
	endBCD, err := StringToBCD6(m.EndTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, endBCD...)
	return buf, nil
}

func (m *StorageMediaSearchMessage) Unmarshal(data []byte) error {
	if len(data) < 18 {
		return ErrDataTooShort
	}
	m.MultimediaID = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	m.MultimediaType = data[4]
	m.ChannelID = data[5]
	m.StartTime = BCDToStringSafe(data[6:12])  // AUTO-FIX-2026-06-26: 时间字段保留前导零
	m.EndTime = BCDToStringSafe(data[12:18])   // AUTO-FIX-2026-06-26: 时间字段保留前导零
	return nil
}

type StorageMediaUploadMessage struct {
	MultimediaID   uint32
	MultimediaType byte
	ChannelID      byte
	StartTime      string
	EndTime        string
	DeleteFlag     byte
}

func (m *StorageMediaUploadMessage) MsgID() uint16 { return MsgIDStorageMediaUpload }

func (m *StorageMediaUploadMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 19)
	buf = append(buf, byte(m.MultimediaID>>24), byte(m.MultimediaID>>16), byte(m.MultimediaID>>8), byte(m.MultimediaID))
	buf = append(buf, m.MultimediaType)
	buf = append(buf, m.ChannelID)
	startBCD, err := StringToBCD6(m.StartTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, startBCD...)
	endBCD, err := StringToBCD6(m.EndTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, endBCD...)
	buf = append(buf, m.DeleteFlag)
	return buf, nil
}

func (m *StorageMediaUploadMessage) Unmarshal(data []byte) error {
	if len(data) < 19 {
		return ErrDataTooShort
	}
	m.MultimediaID = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	m.MultimediaType = data[4]
	m.ChannelID = data[5]
	m.StartTime = BCDToStringSafe(data[6:12])  // AUTO-FIX-2026-06-26: 时间字段保留前导零
	m.EndTime = BCDToStringSafe(data[12:18])   // AUTO-FIX-2026-06-26: 时间字段保留前导零
	m.DeleteFlag = data[18]
	return nil
}

// PassengerCountMessage 0x0A00 客流统计（非标准占用，保留供旧调用方显式构造/解码）。
// AUTO-FIX-2026-07-02 [P3]: 0x0A00 按 JT/T 808-2019 标准为"终端 RSA 公钥交换"，
// jt808.Codec.ParseBody 将 0x0A00 分发至 RSAPublicKeyMessage，本结构体不再经 ParseBody
// 自动分发。需要解析客流统计的旧调用方可显式调用 Unmarshal；新代码应使用 RSAPublicKeyMessage。
// 经核查 module-protocol-1045 拥有独立 codec（ADAS=0x0901），不复用 0x0A00，无运行时冲突。
type PassengerCountMessage struct {
	CountType byte
	CountData []PassengerCountItem
}

type PassengerCountItem struct {
	DoorID   byte
	UpCount  uint16
	DownCount uint16
}

func (m *PassengerCountMessage) MsgID() uint16 { return MsgIDPassengerCount }

func (m *PassengerCountMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 3+len(m.CountData)*5)
	buf = append(buf, m.CountType)
	buf = append(buf, byte(len(m.CountData)>>8), byte(len(m.CountData)))
	for _, item := range m.CountData {
		buf = append(buf, item.DoorID)
		buf = append(buf, byte(item.UpCount>>8), byte(item.UpCount))
		buf = append(buf, byte(item.DownCount>>8), byte(item.DownCount))
	}
	return buf, nil
}

func (m *PassengerCountMessage) Unmarshal(data []byte) error {
	if len(data) < 3 {
		return ErrDataTooShort
	}
	m.CountType = data[0]
	count := int(uint16(data[1])<<8 | uint16(data[2]))
	m.CountData = make([]PassengerCountItem, 0, count)
	offset := 3
	for i := 0; i < count && offset+5 <= len(data); i++ {
		var item PassengerCountItem
		item.DoorID = data[offset]
		item.UpCount = uint16(data[offset+1])<<8 | uint16(data[offset+2])
		item.DownCount = uint16(data[offset+3])<<8 | uint16(data[offset+4])
		m.CountData = append(m.CountData, item)
		offset += 5
	}
	return nil
}

type BillOperateMessage struct {
	OperateType byte
	OperateData []byte
}

func (m *BillOperateMessage) MsgID() uint16 { return MsgIDBillOperate }

func (m *BillOperateMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 1+len(m.OperateData))
	buf = append(buf, m.OperateType)
	buf = append(buf, m.OperateData...)
	return buf, nil
}

func (m *BillOperateMessage) Unmarshal(data []byte) error {
	if len(data) < 1 {
		return ErrDataTooShort
	}
	m.OperateType = data[0]
	if len(data) > 1 {
		m.OperateData = make([]byte, len(data)-1)
		copy(m.OperateData, data[1:])
	}
	return nil
}

// splitNullFields 按空字节分割字段。
// FIXED-2026-07-23 [P2]: 添加 maxFields=20 上限，防止恶意数据生成巨大切片。
func splitNullFields(data []byte) []string {
	const maxFields = 20
	var fields []string
	start := 0
	for i, b := range data {
		if b == 0x00 {
			if len(fields) >= maxFields {
				break
			}
			fields = append(fields, string(data[start:i]))
			start = i + 1
		}
	}
	if start < len(data) && len(fields) < maxFields {
		fields = append(fields, string(data[start:]))
	}
	return fields
}

type TempLocationTrackRespMessage struct {
	Result byte
}

func (m *TempLocationTrackRespMessage) MsgID() uint16 { return MsgIDTempLocationTrackResp }

func (m *TempLocationTrackRespMessage) Marshal() ([]byte, error) {
	return []byte{m.Result}, nil
}

func (m *TempLocationTrackRespMessage) Unmarshal(data []byte) error {
	if len(data) < 1 {
		return ErrDataTooShort
	}
	m.Result = data[0]
	return nil
}

type InfoMenuSetMessage struct {
	SetType byte
	Items   []InfoMenuItem
}

type InfoMenuItem struct {
	// AUTO-FIX-2026-06-27: 0x8700 InfoID 由 4B 改为 2B
	InfoID   uint16
	InfoName string
}

func (m *InfoMenuSetMessage) MsgID() uint16 { return MsgIDInfoMenuSet }

// AUTO-FIX-2026-06-27: 0x8700 菜单总数由 2B 改为 1B，InfoID 由 4B 改为 2B
func (m *InfoMenuSetMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 2+len(m.Items)*8)
	buf = append(buf, m.SetType)
	buf = append(buf, byte(len(m.Items)))
	for _, item := range m.Items {
		buf = append(buf, byte(item.InfoID>>8), byte(item.InfoID))
		nameBytes := []byte(item.InfoName)
		buf = append(buf, byte(len(nameBytes)))
		buf = append(buf, nameBytes...)
	}
	return buf, nil
}

func (m *InfoMenuSetMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-06-27: 菜单总数 1B + 每项 InfoID(2B)+名称长度(1B)+名称
	if len(data) < 2 {
		return ErrDataTooShort
	}
	m.SetType = data[0]
	count := int(data[1])
	m.Items = make([]InfoMenuItem, 0, count)
	offset := 2
	for i := 0; i < count && offset+3 <= len(data); i++ {
		var item InfoMenuItem
		item.InfoID = uint16(data[offset])<<8 | uint16(data[offset+1])
		nameLen := int(data[offset+2])
		offset += 3
		if offset+nameLen > len(data) {
			break
		}
		item.InfoName = string(data[offset : offset+nameLen])
		offset += nameLen
		m.Items = append(m.Items, item)
	}
	return nil
}

type QuestionDownMessage struct {
	Sign      byte
	Question  string
	Options   []QuestionOption
}

type QuestionOption struct {
	OptionID byte
	Content  string
}

func (m *QuestionDownMessage) MsgID() uint16 { return MsgIDQuestionDown }

func (m *QuestionDownMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 10+len(m.Question)+len(m.Options)*10)
	buf = append(buf, m.Sign)
	questionBytes := []byte(m.Question)
	buf = append(buf, byte(len(questionBytes)>>8), byte(len(questionBytes)))
	buf = append(buf, questionBytes...)
	buf = append(buf, byte(len(m.Options)))
	for _, opt := range m.Options {
		buf = append(buf, opt.OptionID)
		contentBytes := []byte(opt.Content)
		buf = append(buf, byte(len(contentBytes)))
		buf = append(buf, contentBytes...)
	}
	return buf, nil
}

func (m *QuestionDownMessage) Unmarshal(data []byte) error {
	if len(data) < 4 {
		return ErrDataTooShort
	}
	m.Sign = data[0]
	qLen := int(uint16(data[1])<<8 | uint16(data[2]))
	offset := 3
	if offset+qLen > len(data) {
		return ErrDataTooShort
	}
	m.Question = string(data[offset : offset+qLen])
	offset += qLen
	if offset >= len(data) {
		return nil
	}
	optCount := int(data[offset])
	offset++
	m.Options = make([]QuestionOption, 0, optCount)
	for i := 0; i < optCount && offset+2 <= len(data); i++ {
		var opt QuestionOption
		opt.OptionID = data[offset]
		cLen := int(data[offset+1])
		offset += 2
		if offset+cLen > len(data) {
			break
		}
		opt.Content = string(data[offset : offset+cLen])
		offset += cLen
		m.Options = append(m.Options, opt)
	}
	return nil
}

// AlarmAttachmentMessage 0x0901 报警附件上传（终端→平台）
// JT/T 808-2019 报警附件上传消息，支持多附件。
type AlarmAttachmentMessage struct {
	AlarmID     uint32              // 报警标识
	Attachments []AlarmAttachmentItem // 附件列表
}

// AlarmAttachmentItem 单个报警附件
type AlarmAttachmentItem struct {
	Type byte   // 附件类型: 0=图片 1=音频 2=视频 3=文本 4=其他
	Size uint32 // 附件大小（字节）
	Data []byte // 附件数据
}

func (m *AlarmAttachmentMessage) MsgID() uint16 { return MsgIDAlarmAttachment }

func (m *AlarmAttachmentMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 10)
	// 报警标识 4 字节
	buf = append(buf, byte(m.AlarmID>>24), byte(m.AlarmID>>16), byte(m.AlarmID>>8), byte(m.AlarmID))
	// 附件数量 1 字节
	buf = append(buf, byte(len(m.Attachments)))
	for _, att := range m.Attachments {
		// 附件类型 1 字节
		buf = append(buf, att.Type)
		// 附件大小 4 字节
		buf = append(buf, byte(att.Size>>24), byte(att.Size>>16), byte(att.Size>>8), byte(att.Size))
		// 附件数据
		buf = append(buf, att.Data...)
	}
	return buf, nil
}

func (m *AlarmAttachmentMessage) Unmarshal(data []byte) error {
	if len(data) < 5 {
		return ErrDataTooShort
	}
	// FIXED-2026-07-23 [P1]: 附件 Size 上限，防止恶意数据触发大内存分配
	const maxAttachmentSize = 10 * 1024 * 1024 // 10MB
	m.AlarmID = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	attCount := int(data[4])
	offset := 5
	m.Attachments = make([]AlarmAttachmentItem, 0, attCount)
	for i := 0; i < attCount; i++ {
		if offset+5 > len(data) {
			return fmt.Errorf("AlarmAttachment: expected %d attachments, got %d: %w", attCount, i, ErrDataTooShort)
		}
		var att AlarmAttachmentItem
		att.Type = data[offset]
		att.Size = uint32(data[offset+1])<<24 | uint32(data[offset+2])<<16 | uint32(data[offset+3])<<8 | uint32(data[offset+4])
		offset += 5
		// FIXED-2026-07-23 [P1]: 检查附件 Size 上限
		if int(att.Size) > maxAttachmentSize {
			return fmt.Errorf("attachment size %d exceeds max %d", att.Size, maxAttachmentSize)
		}
		if offset+int(att.Size) > len(data) {
			// 数据不完整（可能是分包），取剩余数据
			att.Data = make([]byte, len(data)-offset)
			copy(att.Data, data[offset:])
			offset = len(data)
		} else {
			att.Data = make([]byte, att.Size)
			copy(att.Data, data[offset:offset+int(att.Size)])
			offset += int(att.Size)
		}
		m.Attachments = append(m.Attachments, att)
	}
	return nil
}

// AlarmAttachmentRespMessage 0x9001 报警附件上传应答（平台→终端）
type AlarmAttachmentRespMessage struct {
	RespSeqNum uint16 // 应答流水号（对应 0x0901 的流水号）
	Result     byte   // 0=成功 1=失败
}

func (m *AlarmAttachmentRespMessage) MsgID() uint16 { return MsgIDAlarmAttachmentResp }

func (m *AlarmAttachmentRespMessage) Marshal() ([]byte, error) {
	return []byte{byte(m.RespSeqNum >> 8), byte(m.RespSeqNum), m.Result}, nil
}

func (m *AlarmAttachmentRespMessage) Unmarshal(data []byte) error {
	if len(data) < 3 {
		return ErrDataTooShort
	}
	m.RespSeqNum = uint16(data[0])<<8 | uint16(data[1])
	m.Result = data[2]
	return nil
}

// FIX-7-1: 0x9101 视频请求消息体缺失，补充结构体实现协议完整性 [2026-06-26]
// 平台→终端的音视频请求指令（JT/T 808-2019 0x9101）
type VideoRequestMessage struct {
	ServerIP    string // 服务器IP地址（点分十进制字符串，编码为 6 字节 BCD）
	ServerPort  uint16 // 服务器TCP端口
	PlayType    byte   // 0=实时 1=回放
	Channel     byte   // 逻辑通道号
	DataType    byte   // 0=音视频 1=音频 2=视频 3=音视频（反向）
	StreamType  byte   // 0=主码流 1=子码流
}

func (m *VideoRequestMessage) MsgID() uint16 { return MsgIDVideoRequest }

func (m *VideoRequestMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 11)
	ipParts := strings.Split(m.ServerIP, ".")
	if len(ipParts) != 4 {
		return nil, fmt.Errorf("invalid server ip: %s", m.ServerIP)
	}
	for _, p := range ipParts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return nil, fmt.Errorf("invalid ip part: %s", p)
		}
		buf = append(buf, byte(n))
	}
	buf = append(buf, byte(m.ServerPort>>8), byte(m.ServerPort))
	buf = append(buf, m.PlayType)
	buf = append(buf, m.Channel)
	buf = append(buf, m.DataType)
	buf = append(buf, m.StreamType)
	return buf, nil
}

func (m *VideoRequestMessage) Unmarshal(data []byte) error {
	if len(data) < 10 {
		return ErrDataTooShort
	}
	m.ServerIP = fmt.Sprintf("%d.%d.%d.%d", data[0], data[1], data[2], data[3])
	m.ServerPort = uint16(data[4])<<8 | uint16(data[5])
	m.PlayType = data[6]
	m.Channel = data[7]
	m.DataType = data[8]
	m.StreamType = data[9]
	return nil
}

var _ protocol.MessageBody = (*VideoRequestMessage)(nil)

// AUTO-FIX-2026-06-26: 补充808协议缺失消息体结构体（10项），按文档第一轮.txt要求实现 [2026-06-26]

// TerminalUpgradeMessage 0x8108 终端升级（平台→终端）
// 升级类型(1B) + 制造商ID(5B) + 版本号长度(1B) + 版本号(NB) + 升级URL(剩余)
type TerminalUpgradeMessage struct {
	UpgradeType  byte
	Manufacturer string
	Version      string
	URL          string
}

func (m *TerminalUpgradeMessage) MsgID() uint16 { return MsgIDTerminalUpgrade }

func (m *TerminalUpgradeMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 12+len(m.Version)+len(m.URL))
	buf = append(buf, m.UpgradeType)
	manu := make([]byte, 5)
	copy(manu, m.Manufacturer)
	buf = append(buf, manu...)
	v := []byte(m.Version)
	buf = append(buf, byte(len(v)))
	buf = append(buf, v...)
	buf = append(buf, []byte(m.URL)...)
	return buf, nil
}

func (m *TerminalUpgradeMessage) Unmarshal(data []byte) error {
	if len(data) < 7 {
		return ErrDataTooShort
	}
	m.UpgradeType = data[0]
	m.Manufacturer = string(bytes.TrimRight(data[1:6], "\x00"))
	vLen := int(data[6])
	offset := 7
	if offset+vLen > len(data) {
		return ErrDataTooShort
	}
	m.Version = string(data[offset : offset+vLen])
	offset += vLen
	if offset < len(data) {
		m.URL = string(data[offset:])
	}
	return nil
}

// TerminalPropQueryMessage 0x8107 终端属性查询（平台→终端）
// 消息体为空，平台下发查询，终端通过0x0107应答
type TerminalPropQueryMessage struct{}

func (m *TerminalPropQueryMessage) MsgID() uint16 { return MsgIDTerminalPropQuery }

func (m *TerminalPropQueryMessage) Marshal() ([]byte, error) { return nil, nil }

func (m *TerminalPropQueryMessage) Unmarshal(data []byte) error { return nil }

// MultimediaUploadCmdMessage 0x8802 多媒体上传控制（平台→终端）
// AUTO-FIX-2026-06-27: 重构为 多媒体ID(4B)+通道号(1B)+媒体类型(1B)+起止时间(BCD6B×2)，删除原有重传包逻辑
type MultimediaUploadCmdMessage struct {
	MultimediaID uint32
	ChannelID    byte
	MediaType     byte
	StartTime    string // BCD6B YYMMDDHHmmss
	EndTime      string // BCD6B YYMMDDHHmmss
}

func (m *MultimediaUploadCmdMessage) MsgID() uint16 { return MsgIDMultimediaUploadCmd }

// AUTO-FIX-2026-06-27: 体为 多媒体ID(4B)+通道号(1B)+媒体类型(1B)+起止时间(BCD6B×2)，共18B
func (m *MultimediaUploadCmdMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 18)
	buf = append(buf, byte(m.MultimediaID>>24), byte(m.MultimediaID>>16), byte(m.MultimediaID>>8), byte(m.MultimediaID))
	buf = append(buf, m.ChannelID)
	buf = append(buf, m.MediaType)
	startBCD, err := StringToBCD6(m.StartTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, startBCD...)
	endBCD, err := StringToBCD6(m.EndTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, endBCD...)
	return buf, nil
}

func (m *MultimediaUploadCmdMessage) Unmarshal(data []byte) error {
	// AUTO-FIX-2026-06-27: 最小长度 18B (4+1+1+6+6)
	if len(data) < 18 {
		return ErrDataTooShort
	}
	m.MultimediaID = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	m.ChannelID = data[4]
	m.MediaType = data[5]
	m.StartTime = BCDToStringSafe(data[6:12])
	m.EndTime = BCDToStringSafe(data[12:18])
	return nil
}

// FileUploadCmdMessage 0x8803 多媒体传输/文件上传指令（平台→终端）
// 多媒体ID(4B) + 拍摄命令(1B) + 时间(2B) + 保存标志(1B) + 音频采样率(1B)
type FileUploadCmdMessage struct {
	MultimediaID uint32
	Cmd          byte
	Time         uint16
	SaveFlag     byte
	AudioSample  byte
}

func (m *FileUploadCmdMessage) MsgID() uint16 { return MsgIDFileUploadCmd }

func (m *FileUploadCmdMessage) Marshal() ([]byte, error) {
	return []byte{
		byte(m.MultimediaID >> 24), byte(m.MultimediaID >> 16), byte(m.MultimediaID >> 8), byte(m.MultimediaID),
		m.Cmd, byte(m.Time >> 8), byte(m.Time), m.SaveFlag, m.AudioSample,
	}, nil
}

func (m *FileUploadCmdMessage) Unmarshal(data []byte) error {
	if len(data) < 9 {
		return ErrDataTooShort
	}
	m.MultimediaID = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	m.Cmd = data[4]
	m.Time = uint16(data[5])<<8 | uint16(data[6])
	m.SaveFlag = data[7]
	m.AudioSample = data[8]
	return nil
}

// MultimediaSearchMessage 0x0805 多媒体检索（终端→平台，资源列表上传）
// 多媒体ID(4B) + 检索结果数量(2B) + 资源项列表(N × 项)
// 每项: 通道号(1B) + 类型(1B) + 开始时间(6B BCD) + 结束时间(6B BCD) + 大小(4B)
type MultimediaSearchMessage struct {
	MultimediaID uint32
	Items        []MultimediaSearchItem
}

type MultimediaSearchItem struct {
	ChannelID  byte
	MediaType  byte
	StartTime  string
	EndTime    string
	Size       uint32
}

// AUTO-FIX-2026-06-27: 0x0805 常量重命名 MsgIDAudioRecord → MsgIDMultimediaSearchResp
func (m *MultimediaSearchMessage) MsgID() uint16 { return MsgIDMultimediaSearchResp }

func (m *MultimediaSearchMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 6+len(m.Items)*18)
	buf = append(buf, byte(m.MultimediaID>>24), byte(m.MultimediaID>>16), byte(m.MultimediaID>>8), byte(m.MultimediaID))
	buf = append(buf, byte(len(m.Items)>>8), byte(len(m.Items)))
	for _, it := range m.Items {
		buf = append(buf, it.ChannelID, it.MediaType)
itStartBCD, err := StringToBCD6(it.StartTime)
		if err != nil {
			return nil, err
		}
		buf = append(buf, itStartBCD...)
		itEndBCD, err := StringToBCD6(it.EndTime)
		if err != nil {
			return nil, err
		}
		buf = append(buf, itEndBCD...)
		buf = append(buf, byte(it.Size>>24), byte(it.Size>>16), byte(it.Size>>8), byte(it.Size))
	}
	return buf, nil
}

func (m *MultimediaSearchMessage) Unmarshal(data []byte) error {
	if len(data) < 6 {
		return ErrDataTooShort
	}
	m.MultimediaID = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	count := int(uint16(data[4])<<8 | uint16(data[5]))
	m.Items = make([]MultimediaSearchItem, 0, count)
	offset := 6
	for i := 0; i < count && offset+18 <= len(data); i++ {
		var it MultimediaSearchItem
		it.ChannelID = data[offset]
		it.MediaType = data[offset+1]
		it.StartTime = BCDToStringSafe(data[offset+2 : offset+8])  // AUTO-FIX-2026-06-26: 时间字段保留前导零
		it.EndTime = BCDToStringSafe(data[offset+8 : offset+14])   // AUTO-FIX-2026-06-26: 时间字段保留前导零
		it.Size = uint32(data[offset+14])<<24 | uint32(data[offset+15])<<16 | uint32(data[offset+16])<<8 | uint32(data[offset+17])
		m.Items = append(m.Items, it)
		offset += 18
	}
	return nil
}

// PhoneCallbackMessage 0x8702 电话回拨（平台→终端，信息类）
// 电话类型(1B: 0=呼入 1=呼出 2=监听) + 电话号码(变长)
type PhoneCallbackMessage struct {
	CallbackType byte
	PhoneNumber string
}

func (m *PhoneCallbackMessage) MsgID() uint16 { return MsgIDPhoneCallback }

func (m *PhoneCallbackMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 1+len(m.PhoneNumber))
	buf = append(buf, m.CallbackType)
	buf = append(buf, []byte(m.PhoneNumber)...)
	return buf, nil
}

func (m *PhoneCallbackMessage) Unmarshal(data []byte) error {
	if len(data) < 1 {
		return ErrDataTooShort
	}
	m.CallbackType = data[0]
	if len(data) > 1 {
		m.PhoneNumber = string(data[1:])
	}
	return nil
}

// SMSForwardMessage 0x8703 信息服务/短信转发（平台→终端，信息类）
// 信息类型(1B) + 信息内容(变长)
type SMSForwardMessage struct {
	InfoType byte
	Content  string
}

func (m *SMSForwardMessage) MsgID() uint16 { return MsgIDSMSForward }

func (m *SMSForwardMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 1+len(m.Content))
	buf = append(buf, m.InfoType)
	buf = append(buf, []byte(m.Content)...)
	return buf, nil
}

func (m *SMSForwardMessage) Unmarshal(data []byte) error {
	if len(data) < 1 {
		return ErrDataTooShort
	}
	m.InfoType = data[0]
	if len(data) > 1 {
		m.Content = string(data[1:])
	}
	return nil
}

// EventSetMessage 0x8301 事件设置（平台→终端）
// 设置类型(1B: 0=删除全部 1=更新 2=追加 3=修改) + 事件数(2B) + 事件项(N × 项)
// 每项: 事件ID(2B) + 事件内容长度(1B) + 事件内容(变长)
type EventSetMessage struct {
	SetType byte
	Events  []EventItem
}

type EventItem struct {
	EventID uint16
	Content string
}

func (m *EventSetMessage) MsgID() uint16 { return MsgIDEventSet }

func (m *EventSetMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 3+len(m.Events)*5)
	buf = append(buf, m.SetType)
	if m.SetType == 0 {
		return buf, nil
	}
	buf = append(buf, byte(len(m.Events)>>8), byte(len(m.Events)))
	for _, ev := range m.Events {
		buf = append(buf, byte(ev.EventID>>8), byte(ev.EventID))
		c := []byte(ev.Content)
		buf = append(buf, byte(len(c)))
		buf = append(buf, c...)
	}
	return buf, nil
}

func (m *EventSetMessage) Unmarshal(data []byte) error {
	if len(data) < 1 {
		return ErrDataTooShort
	}
	m.SetType = data[0]
	if m.SetType == 0 {
		return nil
	}
	if len(data) < 3 {
		return ErrDataTooShort
	}
	count := int(uint16(data[1])<<8 | uint16(data[2]))
	m.Events = make([]EventItem, 0, count)
	offset := 3
	for i := 0; i < count && offset+3 <= len(data); i++ {
		var ev EventItem
		ev.EventID = uint16(data[offset])<<8 | uint16(data[offset+1])
		cLen := int(data[offset+2])
		offset += 3
		if offset+cLen > len(data) {
			break
		}
		ev.Content = string(data[offset : offset+cLen])
		offset += cLen
		m.Events = append(m.Events, ev)
	}
	return nil
}

// InfoDistributeMessage 0x8303 信息点设置/下发（平台→终端）
// 设置类型(1B) + 信息点ID(4B) + 信息点名称长度(1B) + 信息点名称(变长)
type InfoDistributeMessage struct {
	SetType  byte
	InfoID   uint32
	InfoName string
}

func (m *InfoDistributeMessage) MsgID() uint16 { return MsgIDInfoDistribute }

func (m *InfoDistributeMessage) Marshal() ([]byte, error) {
	name := []byte(m.InfoName)
	buf := make([]byte, 0, 6+len(name))
	buf = append(buf, m.SetType)
	buf = append(buf, byte(m.InfoID>>24), byte(m.InfoID>>16), byte(m.InfoID>>8), byte(m.InfoID))
	buf = append(buf, byte(len(name)))
	buf = append(buf, name...)
	return buf, nil
}

func (m *InfoDistributeMessage) Unmarshal(data []byte) error {
	if len(data) < 6 {
		return ErrDataTooShort
	}
	m.SetType = data[0]
	m.InfoID = uint32(data[1])<<24 | uint32(data[2])<<16 | uint32(data[3])<<8 | uint32(data[4])
	nameLen := int(data[5])
	offset := 6
	if offset+nameLen > len(data) {
		return ErrDataTooShort
	}
	m.InfoName = string(data[offset : offset+nameLen])
	return nil
}

// AUTO-FIX-2026-06-26: 新增消息体接口实现断言
var _ protocol.MessageBody = (*TerminalUpgradeMessage)(nil)
var _ protocol.MessageBody = (*TerminalPropQueryMessage)(nil)
var _ protocol.MessageBody = (*MultimediaUploadCmdMessage)(nil)
var _ protocol.MessageBody = (*FileUploadCmdMessage)(nil)
var _ protocol.MessageBody = (*MultimediaSearchMessage)(nil)
var _ protocol.MessageBody = (*PhoneCallbackMessage)(nil)
var _ protocol.MessageBody = (*SMSForwardMessage)(nil)
var _ protocol.MessageBody = (*EventSetMessage)(nil)
var _ protocol.MessageBody = (*InfoDistributeMessage)(nil)

// ===================================================================
// AUTO-FIX-2026-06-28: 0x8404-0x8407 电子运单类消息具体结构体
// 替换原 RawMessage 占位实现，遵循 JT/T 808-2019 标准
// ===================================================================

// EWaybillSetMessage 0x8404 电子运单数据设置（平台→终端）
// 报文体: 电子运单长度(4B) + 电子运单内容(变长, GBK编码)
// 内容格式由具体业务定义，通常为 JSON/XML 文本
// AUTO-FIX-2026-07-04 [P2]: 增加 MaxEWaybillDataSize 上限校验，防止恶意超大运单数据
type EWaybillSetMessage struct {
	WaybillData []byte
}

// MaxEWaybillDataSize 电子运单数据最大字节数（1MB），防止内存耗尽
const MaxEWaybillDataSize = 1024 * 1024

func (m *EWaybillSetMessage) MsgID() uint16 { return MsgIDEWaybillSet }

func (m *EWaybillSetMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 4+len(m.WaybillData))
	l := uint32(len(m.WaybillData))
	buf = append(buf, byte(l>>24), byte(l>>16), byte(l>>8), byte(l))
	buf = append(buf, m.WaybillData...)
	return buf, nil
}

func (m *EWaybillSetMessage) Unmarshal(data []byte) error {
if len(data) < 4 {
return ErrDataTooShort
}
l := uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
// AUTO-FIX-2026-07-04 [P2]: 校验运单数据大小上限
if l > MaxEWaybillDataSize {
return fmt.Errorf("ewaybill data too large: %d bytes (max %d)", l, MaxEWaybillDataSize)
}
if len(data) < 4+int(l) {
return ErrDataTooShort
}
m.WaybillData = make([]byte, l)
copy(m.WaybillData, data[4:4+l])
return nil
}

// EWaybillDelMessage 0x8405 电子运单数据删除（平台→终端）
// 报文体: 删除类型(1B)
//   - 0=删除所有电子运单（无后续字段）
//   - 1=删除指定电子运单: 运单ID数量(2B) + (运单ID长度(1B) + 运单ID(变长)) × N
type EWaybillDelMessage struct {
	DelType byte
	IDs     []string
}

func (m *EWaybillDelMessage) MsgID() uint16 { return MsgIDEWaybillDel }

func (m *EWaybillDelMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 16)
	buf = append(buf, m.DelType)
	if m.DelType == 1 {
		buf = append(buf, byte(len(m.IDs)>>8), byte(len(m.IDs)))
		for _, id := range m.IDs {
			idBytes := []byte(id)
			buf = append(buf, byte(len(idBytes)))
			buf = append(buf, idBytes...)
		}
	}
	return buf, nil
}

func (m *EWaybillDelMessage) Unmarshal(data []byte) error {
	if len(data) < 1 {
		return ErrDataTooShort
	}
	m.DelType = data[0]
	if m.DelType == 0 {
		return nil
	}
	if m.DelType != 1 {
		return fmt.Errorf("invalid ewaybill del type: %d", m.DelType)
	}
	if len(data) < 3 {
		return ErrDataTooShort
	}
	count := int(uint16(data[1])<<8 | uint16(data[2]))
	m.IDs = make([]string, 0, count)
	offset := 3
	for i := 0; i < count && offset+1 < len(data); i++ {
		idLen := int(data[offset])
		offset++
		if offset+idLen > len(data) {
			break
		}
		m.IDs = append(m.IDs, string(data[offset:offset+idLen]))
		offset += idLen
	}
	return nil
}

// EWaybillUploadMessage 0x8406 电子运单数据上传（平台→终端）
// 平台主动请求终端上传电子运单，无消息体
type EWaybillUploadMessage struct{}

func (m *EWaybillUploadMessage) MsgID() uint16 { return MsgIDEWaybillUpload }

func (m *EWaybillUploadMessage) Marshal() ([]byte, error) {
	return nil, nil
}

func (m *EWaybillUploadMessage) Unmarshal(data []byte) error {
	return nil
}

// EWaybillRespMessage 0x8407 电子运单上报应答（终端→平台）
// 报文体: 流水号(2B) + 结果(1B)
// 应答 0x0701 电子运单上报消息
type EWaybillRespMessage struct {
	SeqNum uint16
	Result byte
}

func (m *EWaybillRespMessage) MsgID() uint16 { return MsgIDEWaybillResp }

func (m *EWaybillRespMessage) Marshal() ([]byte, error) {
	return []byte{
		byte(m.SeqNum >> 8), byte(m.SeqNum),
		m.Result,
	}, nil
}

func (m *EWaybillRespMessage) Unmarshal(data []byte) error {
	if len(data) < 3 {
		return ErrDataTooShort
	}
	m.SeqNum = uint16(data[0])<<8 | uint16(data[1])
	m.Result = data[2]
	return nil
}

var _ protocol.MessageBody = (*EWaybillSetMessage)(nil)
var _ protocol.MessageBody = (*EWaybillDelMessage)(nil)
var _ protocol.MessageBody = (*EWaybillUploadMessage)(nil)
var _ protocol.MessageBody = (*EWaybillRespMessage)(nil)

// AUTO-FIX-2026-06-28: 补全历史遗漏的接口断言
var _ protocol.MessageBody = (*ParamQueryMessage)(nil)
var _ protocol.MessageBody = (*ParamRespMessage)(nil)
var _ protocol.MessageBody = (*ParamSetMessage)(nil)
var _ protocol.MessageBody = (*AlarmAttachmentRespMessage)(nil)
var _ protocol.MessageBody = (*TempLocationTrackRespMessage)(nil)

// AUTO-FIX-2026-06-27: 上轮未注册4项消息结构体 [2026-06-27]

// OverspeedAlarmMessage 0x0400 超速报警（终端→平台）
// 字段: 报警标志(4B) + 状态(4B) + 纬度(4B) + 经度(4B) + 高程(2B) + 速度(2B) + 方向(2B) + 时间(BCD6B) + 报警附件(变长)
type OverspeedAlarmMessage struct {
	AlarmFlag      uint32 // 报警标志
	StatusFlag     uint32 // 状态
	Latitude       float64
	Longitude      float64
	Altitude       uint16
	Speed          uint16
	Direction      uint16
	Time           string // BCD6B YYMMDDHHmmss
	AlarmAttach    []byte // 报警附件（变长）
}

func (m *OverspeedAlarmMessage) MsgID() uint16 { return MsgIDOverspeedAlarm }

func (m *OverspeedAlarmMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 28+len(m.AlarmAttach))
	buf = append(buf, byte(m.AlarmFlag>>24), byte(m.AlarmFlag>>16), byte(m.AlarmFlag>>8), byte(m.AlarmFlag))
	buf = append(buf, byte(m.StatusFlag>>24), byte(m.StatusFlag>>16), byte(m.StatusFlag>>8), byte(m.StatusFlag))
	// FIXED: [P0] 编码时取绝对值，N/S 由 StatusFlag bit2 指示 [2026-07-17]
	absLat := m.Latitude
	if absLat < 0 {
		absLat = -absLat
	}
	if absLat > 90.0 {
		return nil, fmt.Errorf("latitude %.6f exceeds ±90 range", absLat)
	}
	lat := uint32(absLat * JT808CoordScaleFactor)
	// FIXED: [P0] 编码时取绝对值，E/W 由 StatusFlag bit3 指示 [2026-07-17]
	absLon := m.Longitude
	if absLon < 0 {
		absLon = -absLon
	}
	if absLon > 180.0 {
		return nil, fmt.Errorf("longitude %.6f exceeds ±180 range", absLon)
	}
	lon := uint32(absLon * JT808CoordScaleFactor)
	buf = append(buf, byte(lat>>24), byte(lat>>16), byte(lat>>8), byte(lat))
	buf = append(buf, byte(lon>>24), byte(lon>>16), byte(lon>>8), byte(lon))
	buf = append(buf, byte(m.Altitude>>8), byte(m.Altitude))
	buf = append(buf, byte(m.Speed>>8), byte(m.Speed))
	buf = append(buf, byte(m.Direction>>8), byte(m.Direction))
	timeBCD, err := StringToBCD6(m.Time)
	if err != nil {
		return nil, err
	}
	buf = append(buf, timeBCD...)
	buf = append(buf, m.AlarmAttach...)
	return buf, nil
}

func (m *OverspeedAlarmMessage) Unmarshal(data []byte) error {
	if len(data) < 28 {
		return ErrDataTooShort
	}
	m.AlarmFlag = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	m.StatusFlag = uint32(data[4])<<24 | uint32(data[5])<<16 | uint32(data[6])<<8 | uint32(data[7])
	latRaw := uint32(data[8])<<24 | uint32(data[9])<<16 | uint32(data[10])<<8 | uint32(data[11])
	m.Latitude = float64(latRaw) / JT808CoordScaleFactor
	lonRaw := uint32(data[12])<<24 | uint32(data[13])<<16 | uint32(data[14])<<8 | uint32(data[15])
	m.Longitude = float64(lonRaw) / JT808CoordScaleFactor
	m.Altitude = uint16(data[16])<<8 | uint16(data[17])
	m.Speed = uint16(data[18])<<8 | uint16(data[19])
	m.Direction = uint16(data[20])<<8 | uint16(data[21])
	m.Time = BCDToStringSafe(data[22:28])
	if len(data) > 28 {
		m.AlarmAttach = make([]byte, len(data)-28)
		copy(m.AlarmAttach, data[28:])
	}
	return nil
}

// FatigueDriveAlarmMessage 0x0401 疲劳驾驶报警（终端→平台）
// 字段同 0x0400: 报警标志(4B) + 状态(4B) + 纬度(4B) + 经度(4B) + 高程(2B) + 速度(2B) + 方向(2B) + 时间(BCD6B) + 报警附件(变长)
type FatigueDriveAlarmMessage struct {
	AlarmFlag      uint32
	StatusFlag     uint32
	Latitude       float64
	Longitude      float64
	Altitude       uint16
	Speed          uint16
	Direction      uint16
	Time           string
	AlarmAttach    []byte
}

func (m *FatigueDriveAlarmMessage) MsgID() uint16 { return MsgIDFatigueDriveAlarm }

func (m *FatigueDriveAlarmMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 28+len(m.AlarmAttach))
	buf = append(buf, byte(m.AlarmFlag>>24), byte(m.AlarmFlag>>16), byte(m.AlarmFlag>>8), byte(m.AlarmFlag))
	buf = append(buf, byte(m.StatusFlag>>24), byte(m.StatusFlag>>16), byte(m.StatusFlag>>8), byte(m.StatusFlag))
	// FIXED: [P0] 编码时取绝对值，N/S 由 StatusFlag bit2 指示 [2026-07-17]
	absLat := m.Latitude
	if absLat < 0 {
		absLat = -absLat
	}
	if absLat > 90.0 {
		return nil, fmt.Errorf("latitude %.6f exceeds ±90 range", absLat)
	}
	lat := uint32(absLat * JT808CoordScaleFactor)
	// FIXED: [P0] 编码时取绝对值，E/W 由 StatusFlag bit3 指示 [2026-07-17]
	absLon := m.Longitude
	if absLon < 0 {
		absLon = -absLon
	}
	if absLon > 180.0 {
		return nil, fmt.Errorf("longitude %.6f exceeds ±180 range", absLon)
	}
	lon := uint32(absLon * JT808CoordScaleFactor)
	buf = append(buf, byte(lat>>24), byte(lat>>16), byte(lat>>8), byte(lat))
	buf = append(buf, byte(lon>>24), byte(lon>>16), byte(lon>>8), byte(lon))
	buf = append(buf, byte(m.Altitude>>8), byte(m.Altitude))
	buf = append(buf, byte(m.Speed>>8), byte(m.Speed))
	buf = append(buf, byte(m.Direction>>8), byte(m.Direction))
	timeBCD, err := StringToBCD6(m.Time)
	if err != nil {
		return nil, err
	}
	buf = append(buf, timeBCD...)
	buf = append(buf, m.AlarmAttach...)
	return buf, nil
}

func (m *FatigueDriveAlarmMessage) Unmarshal(data []byte) error {
	if len(data) < 28 {
		return ErrDataTooShort
	}
	m.AlarmFlag = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	m.StatusFlag = uint32(data[4])<<24 | uint32(data[5])<<16 | uint32(data[6])<<8 | uint32(data[7])
	latRaw := uint32(data[8])<<24 | uint32(data[9])<<16 | uint32(data[10])<<8 | uint32(data[11])
	m.Latitude = float64(latRaw) / JT808CoordScaleFactor
	lonRaw := uint32(data[12])<<24 | uint32(data[13])<<16 | uint32(data[14])<<8 | uint32(data[15])
	m.Longitude = float64(lonRaw) / JT808CoordScaleFactor
	m.Altitude = uint16(data[16])<<8 | uint16(data[17])
	m.Speed = uint16(data[18])<<8 | uint16(data[19])
	m.Direction = uint16(data[20])<<8 | uint16(data[21])
	m.Time = BCDToStringSafe(data[22:28])
	if len(data) > 28 {
		m.AlarmAttach = make([]byte, len(data)-28)
		copy(m.AlarmAttach, data[28:])
	}
	return nil
}

// InfoPushMessage 0x8701 信息点推送（平台→终端）
// 字段: InfoID(4B) + 信息点名称长度(1B) + 名称 + 信息点类型(1B) + 信息点经度(4B) + 信息点纬度(4B)
type InfoPushMessage struct {
	InfoID     uint32
	InfoName   string // GBK 编码（保存原始字节后由调用方解码）
	InfoType   byte
	Longitude  float64
	Latitude   float64
}

func (m *InfoPushMessage) MsgID() uint16 { return MsgIDInfoPush }

func (m *InfoPushMessage) Marshal() ([]byte, error) {
	name := []byte(m.InfoName)
	if len(name) > 255 {
		return nil, fmt.Errorf("info name too long: %d", len(name))
	}
	buf := make([]byte, 0, 14+len(name))
	buf = append(buf, byte(m.InfoID>>24), byte(m.InfoID>>16), byte(m.InfoID>>8), byte(m.InfoID))
	buf = append(buf, byte(len(name)))
	buf = append(buf, name...)
	buf = append(buf, m.InfoType)
	// FIXED: [P0] 编码时取绝对值，E/W 由 StatusFlag bit3 指示 [2026-07-17]
	absLon := m.Longitude
	if absLon < 0 {
		absLon = -absLon
	}
	if absLon > 180.0 {
		return nil, fmt.Errorf("longitude %.6f exceeds ±180 range", absLon)
	}
	lon := uint32(absLon * JT808CoordScaleFactor)
	// FIXED: [P0] 编码时取绝对值，N/S 由 StatusFlag bit2 指示 [2026-07-17]
	absLat := m.Latitude
	if absLat < 0 {
		absLat = -absLat
	}
	if absLat > 90.0 {
		return nil, fmt.Errorf("latitude %.6f exceeds ±90 range", absLat)
	}
	lat := uint32(absLat * JT808CoordScaleFactor)
	buf = append(buf, byte(lon>>24), byte(lon>>16), byte(lon>>8), byte(lon))
	buf = append(buf, byte(lat>>24), byte(lat>>16), byte(lat>>8), byte(lat))
	return buf, nil
}

func (m *InfoPushMessage) Unmarshal(data []byte) error {
	if len(data) < 5 {
		return ErrDataTooShort
	}
	m.InfoID = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	nameLen := int(data[4])
	offset := 5
	if offset+nameLen > len(data) {
		return ErrDataTooShort
	}
	m.InfoName = string(data[offset : offset+nameLen])
	offset += nameLen
	if offset+1+4+4 > len(data) {
		return ErrDataTooShort
	}
	m.InfoType = data[offset]
	lonRaw := uint32(data[offset+1])<<24 | uint32(data[offset+2])<<16 | uint32(data[offset+3])<<8 | uint32(data[offset+4])
	m.Longitude = float64(lonRaw) / JT808CoordScaleFactor
	latRaw := uint32(data[offset+5])<<24 | uint32(data[offset+6])<<16 | uint32(data[offset+7])<<8 | uint32(data[offset+8])
	m.Latitude = float64(latRaw) / JT808CoordScaleFactor
	return nil
}

// VideoControlMessage 0x9102 实时音视频控制（jt1078实现，jt808保留转发结构体）
// 字段: 通道号(1B) + 控制命令(1B) + 切换音视频(1B) + 重置(1B) + 关闭流(1B) + 切换流(1B)
type VideoControlMessage struct {
	Channel      byte
	ControlCmd  byte
	SwitchAV     byte
	Reset        byte
	CloseStream  byte
	SwitchStream byte
}

func (m *VideoControlMessage) MsgID() uint16 { return MsgIDVideoControl }

func (m *VideoControlMessage) Marshal() ([]byte, error) {
	return []byte{
		m.Channel, m.ControlCmd, m.SwitchAV, m.Reset, m.CloseStream, m.SwitchStream,
	}, nil
}

func (m *VideoControlMessage) Unmarshal(data []byte) error {
	if len(data) < 6 {
		return ErrDataTooShort
	}
	m.Channel = data[0]
	m.ControlCmd = data[1]
	m.SwitchAV = data[2]
	m.Reset = data[3]
	m.CloseStream = data[4]
	m.SwitchStream = data[5]
	return nil
}

// AUTO-FIX-2026-06-27: 新增消息体接口实现断言
var _ protocol.MessageBody = (*OverspeedAlarmMessage)(nil)
var _ protocol.MessageBody = (*FatigueDriveAlarmMessage)(nil)
var _ protocol.MessageBody = (*InfoPushMessage)(nil)
var _ protocol.MessageBody = (*VideoControlMessage)(nil)

// AUTO-FIX-2026-06-27: 缺失10项消息结构体（0x8204/0x8304/0x8402/0x8403 + 0x8404-0x8407） [2026-06-27]
// AUTO-FIX-2026-06-28 [P3]: 0x8404-0x8407 已替换 RawMessage 占位为具体结构体（EWaybillSet/Del/Info/Resp）

// PhoneBookSetMessage 0x8204 设置电话本（平台→终端）
// 字段: 联系人总数(1B) + 联系人项列表(每项: 联系人姓名GBK变长 + 电话号码变长 + 来电类型1B)
// 注：原标准每项含姓名长度+姓名+号码长度+号码+来电类型；这里采用标准做法每项前缀长度
type PhoneBookSetMessage struct {
	Contacts []PhoneBookContact
}

// PhoneBookContact 电话本联系人项
// AUTO-FIX-2026-06-27: 字段 联系人姓名GBK(变长,前缀1B) + 电话号码(变长,前缀1B) + 来电类型(1B)
type PhoneBookContact struct {
	Name        string
	PhoneNumber string
	CallType    byte // 0=呼入 1=呼出 2=全部
}

func (m *PhoneBookSetMessage) MsgID() uint16 { return MsgIDPhoneBookSet }

func (m *PhoneBookSetMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 1+len(m.Contacts)*20)
	buf = append(buf, byte(len(m.Contacts)))
	for _, c := range m.Contacts {
		name := []byte(c.Name)
		phone := []byte(c.PhoneNumber)
		buf = append(buf, byte(len(name)))
		buf = append(buf, name...)
		buf = append(buf, byte(len(phone)))
		buf = append(buf, phone...)
		buf = append(buf, c.CallType)
	}
	return buf, nil
}

func (m *PhoneBookSetMessage) Unmarshal(data []byte) error {
	if len(data) < 1 {
		return ErrDataTooShort
	}
	count := int(data[0])
	m.Contacts = make([]PhoneBookContact, 0, count)
	offset := 1
	for i := 0; i < count && offset+2 <= len(data); i++ {
		var c PhoneBookContact
		nameLen := int(data[offset])
		offset++
		if offset+nameLen > len(data) {
			break
		}
		c.Name = string(data[offset : offset+nameLen])
		offset += nameLen
		if offset+1 > len(data) {
			break
		}
		phoneLen := int(data[offset])
		offset++
		if offset+phoneLen > len(data) {
			break
		}
		c.PhoneNumber = string(data[offset : offset+phoneLen])
		offset += phoneLen
		if offset+1 > len(data) {
			break
		}
		c.CallType = data[offset]
		offset++
		m.Contacts = append(m.Contacts, c)
	}
	return nil
}

// InfoServiceMessage 0x8304 信息服务（平台→终端）
// 字段: 信息类型(1B) + 信息长度(2B) + 信息内容GBK
type InfoServiceMessage struct {
	InfoType byte
	Content  string // GBK 编码（保存原始字节）
}

func (m *InfoServiceMessage) MsgID() uint16 { return MsgIDInfoService }

func (m *InfoServiceMessage) Marshal() ([]byte, error) {
	content := []byte(m.Content)
	if len(content) > 65535 {
		return nil, fmt.Errorf("info content too long: %d", len(content))
	}
	buf := make([]byte, 0, 3+len(content))
	buf = append(buf, m.InfoType)
	l := uint16(len(content))
	buf = append(buf, byte(l>>8), byte(l))
	buf = append(buf, content...)
	return buf, nil
}

func (m *InfoServiceMessage) Unmarshal(data []byte) error {
	if len(data) < 3 {
		return ErrDataTooShort
	}
	m.InfoType = data[0]
	l := int(uint16(data[1])<<8 | uint16(data[2]))
	if len(data) < 3+l {
		return ErrDataTooShort
	}
	m.Content = string(data[3 : 3+l])
	return nil
}

// AreaRouteAlarmSetMessage 0x8402 区域路线报警设置（平台→终端）
// 字段: 区域ID(4B) + 区域属性(2B) + 起始时间(BCD6B) + 结束时间(BCD6B) + 报警标志(2B) + 中心点纬度(4B) + 中心点经度(4B) + 半径(4B)
// 注：JT/T 808-2019 标准对此消息描述较简略，本实现按区域报警基本结构实现。
type AreaRouteAlarmSetMessage struct {
	AreaID       uint32
	AreaAttr     uint16
	StartTime    string // BCD6B
	EndTime      string // BCD6B
	AlarmFlag    uint16
	CenterLat    float64
	CenterLon    float64
	Radius       uint32
}

func (m *AreaRouteAlarmSetMessage) MsgID() uint16 { return MsgIDAreaRouteAlarmSet }

func (m *AreaRouteAlarmSetMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 32)
	buf = append(buf, byte(m.AreaID>>24), byte(m.AreaID>>16), byte(m.AreaID>>8), byte(m.AreaID))
	buf = append(buf, byte(m.AreaAttr>>8), byte(m.AreaAttr))
	startBCD, err := StringToBCD6(m.StartTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, startBCD...)
	endBCD, err := StringToBCD6(m.EndTime)
	if err != nil {
		return nil, err
	}
	buf = append(buf, endBCD...)
	buf = append(buf, byte(m.AlarmFlag>>8), byte(m.AlarmFlag))
	// [P0-修复] 负坐标处理
	absLat := m.CenterLat
	if absLat < 0 {
		absLat = -absLat
	}
	absLon := m.CenterLon
	if absLon < 0 {
		absLon = -absLon
	}
	if absLat > 90.0 {
		return nil, fmt.Errorf("latitude %.6f exceeds ±90 range", absLat)
	}
	lat := uint32(absLat * JT808CoordScaleFactor)
	if absLon > 180.0 {
		return nil, fmt.Errorf("longitude %.6f exceeds ±180 range", absLon)
	}
	lon := uint32(absLon * JT808CoordScaleFactor)
	buf = append(buf, byte(lat>>24), byte(lat>>16), byte(lat>>8), byte(lat))
	buf = append(buf, byte(lon>>24), byte(lon>>16), byte(lon>>8), byte(lon))
	buf = append(buf, byte(m.Radius>>24), byte(m.Radius>>16), byte(m.Radius>>8), byte(m.Radius))
	return buf, nil
}

func (m *AreaRouteAlarmSetMessage) Unmarshal(data []byte) error {
	if len(data) < 32 {
		return ErrDataTooShort
	}
	m.AreaID = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	m.AreaAttr = uint16(data[4])<<8 | uint16(data[5])
	m.StartTime = BCDToStringSafe(data[6:12])
	m.EndTime = BCDToStringSafe(data[12:18])
	m.AlarmFlag = uint16(data[18])<<8 | uint16(data[19])
	latRaw := uint32(data[20])<<24 | uint32(data[21])<<16 | uint32(data[22])<<8 | uint32(data[23])
	m.CenterLat = float64(latRaw) / JT808CoordScaleFactor
	lonRaw := uint32(data[24])<<24 | uint32(data[25])<<16 | uint32(data[26])<<8 | uint32(data[27])
	m.CenterLon = float64(lonRaw) / JT808CoordScaleFactor
	m.Radius = uint32(data[28])<<24 | uint32(data[29])<<16 | uint32(data[30])<<8 | uint32(data[31])
	return nil
}

// AreaRouteAlarmDelMessage 0x8403 区域路线报警删除（平台→终端）
// 字段: 区域ID(4B)
type AreaRouteAlarmDelMessage struct {
	AreaID uint32
}

func (m *AreaRouteAlarmDelMessage) MsgID() uint16 { return MsgIDAreaRouteAlarmDel }

func (m *AreaRouteAlarmDelMessage) Marshal() ([]byte, error) {
	return []byte{
		byte(m.AreaID >> 24), byte(m.AreaID >> 16), byte(m.AreaID >> 8), byte(m.AreaID),
	}, nil
}

func (m *AreaRouteAlarmDelMessage) Unmarshal(data []byte) error {
	if len(data) < 4 {
		return ErrDataTooShort
	}
	m.AreaID = uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	return nil
}

// AUTO-FIX-2026-06-28 [P3]: 0x8404-0x8407 电子运单类消息已使用具体结构体（EWaybillSet/Del/Info/Resp），
// 在 codec.go 中注册分发，不再使用 RawMessage 占位

// AUTO-FIX-2026-06-27: 新增消息体接口实现断言
var _ protocol.MessageBody = (*PhoneBookSetMessage)(nil)
var _ protocol.MessageBody = (*InfoServiceMessage)(nil)
var _ protocol.MessageBody = (*AreaRouteAlarmSetMessage)(nil)
var _ protocol.MessageBody = (*AreaRouteAlarmDelMessage)(nil)

// ===================================================================
// AUTO-FIX-2026-06-28: 808 RSA 公钥交换消息（0x0A00 / 0x8A00）
// 对照 jte-plan-final-v3.0.md 第3章、第5章要求：
//   - 0x0A00 终端→平台 RSA 公钥交换消息（原被 PassengerCount 占用，现回归标准语义）
//   - 0x8A00 平台→终端 RSA 公钥下发消息（原仅声明 ADAS 常量，未在 ParseBody 注册）
// 注：PassengerCount/ADAS 常量保留以兼容旧引用，实际报警已在 1045 模块实现。
// AUTO-FIX-2026-07-02 [P3]: 别名冲突核查结论——
//   - module-protocol-1045 拥有独立 codec，ADAS 报警使用 0x0901（非 0x8A00），不复用 0x0A00；
//   - 0x0A00/0x8A00 在 808 链路无运行时冲突，ParseBody 按 808-2019 标准分发至 RSA 结构体；
//   - MsgIDRSAPublicKey/MsgIDRSADistribute 已解除对 PassengerCount/ADASAlarm 的别名依赖（值不变）。
// ===================================================================

// RSAPublicKeyMessage 0x0A00 终端 RSA 公钥交换（终端→平台）
// 消息体: RSA 模数 n (128 字节) + 公钥指数 e (4 字节，大端)
type RSAPublicKeyMessage struct {
	Euler              []byte // RSA 模数 n（128 字节）
	PublicExponent     uint32 // RSA 公钥指数 e
}

func (m *RSAPublicKeyMessage) MsgID() uint16 { return MsgIDRSAPublicKey }

func (m *RSAPublicKeyMessage) Marshal() ([]byte, error) {
	// 模数固定 128 字节，不足补 0
	mod := make([]byte, 128)
	if len(m.Euler) > 128 {
		copy(mod, m.Euler[len(m.Euler)-128:])
	} else {
		copy(mod[128-len(m.Euler):], m.Euler)
	}
	buf := make([]byte, 0, 132)
	buf = append(buf, mod...)
	buf = append(buf,
		byte(m.PublicExponent>>24),
		byte(m.PublicExponent>>16),
		byte(m.PublicExponent>>8),
		byte(m.PublicExponent),
	)
	return buf, nil
}

func (m *RSAPublicKeyMessage) Unmarshal(data []byte) error {
	if len(data) < 132 {
		return ErrDataTooShort
	}
	m.Euler = make([]byte, 128)
	copy(m.Euler, data[0:128])
	m.PublicExponent = uint32(data[128])<<24 | uint32(data[129])<<16 | uint32(data[130])<<8 | uint32(data[131])
	return nil
}

// RSADistributeMessage 0x8A00 平台 RSA 公钥下发（平台→终端）
// 消息体: RSA 公钥（128 字节模数 + 4 字节指数，共 132 字节；兼容仅下发模数的实现）
type RSADistributeMessage struct {
	RSAKey []byte // RSA 公钥（128 字节模数或 132 字节 模数+指数）
}

func (m *RSADistributeMessage) MsgID() uint16 { return MsgIDRSADistribute }

func (m *RSADistributeMessage) Marshal() ([]byte, error) {
	if len(m.RSAKey) == 0 {
		return nil, fmt.Errorf("rsa key is empty")
	}
	buf := make([]byte, len(m.RSAKey))
	copy(buf, m.RSAKey)
	return buf, nil
}

func (m *RSADistributeMessage) Unmarshal(data []byte) error {
	if len(data) < 128 {
		return ErrDataTooShort
	}
	m.RSAKey = make([]byte, len(data))
	copy(m.RSAKey, data)
	return nil
}

// ===================================================================
// AUTO-FIX-2026-07-04: 0x0805 摄像头立即拍摄命令应答（终端→平台）
// JT/T 808-2019 标准：终端收到 0x8801 摄像头立即拍摄命令后，以此消息应答。
// 消息体格式: 应答流水号(2B) + 结果(1B) + 多媒体ID(4B) + 重传包ID数量(2B) + [重传包ID列表(2B×N)]
//   - 应答流水号: 对应 0x8801 命令的消息流水号
//   - 结果: 0=成功 1=失败
//   - 多媒体ID: 终端为本次拍摄分配的多媒体ID（成功时有效）
//   - 重传包ID数量: 需要平台重传的包数量（成功时可能存在丢包重传）
//   - 重传包ID列表: 需要重传的包序号列表
// ===================================================================

// PhotoCommandRespMessage 0x0805 摄像头立即拍摄命令应答（终端→平台）
type PhotoCommandRespMessage struct {
	RespSeqNum     uint16   // 应答流水号（对应 0x8801 的 SeqNum）
	Result         byte     // 0=成功 1=失败
	MultimediaID   uint32   // 终端分配的多媒体ID（成功时有效）
	RetransmitIDs  []uint16 // 需要重传的包ID列表（成功时可能存在丢包重传）
}

func (m *PhotoCommandRespMessage) MsgID() uint16 { return MsgIDPhotoCommandResp }

func (m *PhotoCommandRespMessage) Marshal() ([]byte, error) {
	buf := make([]byte, 0, 9+len(m.RetransmitIDs)*2)
	buf = append(buf, byte(m.RespSeqNum>>8), byte(m.RespSeqNum))
	buf = append(buf, m.Result)
	buf = append(buf, byte(m.MultimediaID>>24), byte(m.MultimediaID>>16), byte(m.MultimediaID>>8), byte(m.MultimediaID))
	buf = append(buf, byte(len(m.RetransmitIDs)>>8), byte(len(m.RetransmitIDs)))
	for _, id := range m.RetransmitIDs {
		buf = append(buf, byte(id>>8), byte(id))
	}
	return buf, nil
}

func (m *PhotoCommandRespMessage) Unmarshal(data []byte) error {
	if len(data) < 9 {
		return ErrDataTooShort
	}
	m.RespSeqNum = uint16(data[0])<<8 | uint16(data[1])
	m.Result = data[2]
	m.MultimediaID = uint32(data[3])<<24 | uint32(data[4])<<16 | uint32(data[5])<<8 | uint32(data[6])
	count := int(uint16(data[7])<<8 | uint16(data[8]))
	m.RetransmitIDs = make([]uint16, 0, count)
	offset := 9
	for i := 0; i < count && offset+2 <= len(data); i++ {
		id := uint16(data[offset])<<8 | uint16(data[offset+1])
		m.RetransmitIDs = append(m.RetransmitIDs, id)
		offset += 2
	}
	return nil
}

var _ protocol.MessageBody = (*PhotoCommandRespMessage)(nil)

var _ protocol.MessageBody = (*RSAPublicKeyMessage)(nil)
var _ protocol.MessageBody = (*RSADistributeMessage)(nil)