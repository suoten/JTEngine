package jt808

import (
	"encoding/binary"
	"fmt"

	"github.com/jte-engine/jte/pkg/protocol"
)

const (
	MsgIDTerminalCancel        uint16 = 0x0003
	MsgIDTerminalCancelResp    uint16 = 0x8003
	MsgIDHeartbeat             uint16 = 0x0002
	MsgIDRegister              uint16 = 0x0100
	MsgIDRegisterResp          uint16 = 0x8100
	MsgIDAuth                  uint16 = 0x0102
	MsgIDLocation              uint16 = 0x0200
	MsgIDLocationBatch         uint16 = 0x0704
	MsgIDLocationQuery         uint16 = 0x8201
	MsgIDLocationQueryResp     uint16 = 0x0201
	MsgIDTempLocationTrack     uint16 = 0x8202
	MsgIDTempLocationTrackResp uint16 = 0x0202
	MsgIDTerminalGeneralResp   uint16 = 0x0001
	MsgIDAlarm                 uint16 = 0x0900
	MsgIDAlarmAck              uint16 = 0x8900
	MsgIDAlarmAttachment       uint16 = 0x0901
	MsgIDAlarmAttachmentResp   uint16 = 0x9001
	MsgIDCommand               uint16 = 0x8103
	MsgIDCommandResp           uint16 = 0x0103
	MsgIDParamQuery            uint16 = 0x8104
	MsgIDParamResp             uint16 = 0x0104
	MsgIDParamSet              uint16 = 0x8106
	MsgIDGeneralResp           uint16 = 0x8001
	MsgIDTerminalCtrl          uint16 = 0x8105
	// AUTO-FIX-2026-06-26: 补充车辆控制消息 0x8500（原缺失导致车辆控制指令无法编解码）
	MsgIDVehicleControl      uint16 = 0x8500
	MsgIDTerminalPropQuery   uint16 = 0x8107
	MsgIDTerminalPropResp    uint16 = 0x0107
	MsgIDTerminalUpgrade     uint16 = 0x8108
	MsgIDTerminalUpgradeResp uint16 = 0x0108
	MsgIDMultimedia          uint16 = 0x0801
	MsgIDMultimediaUpload    uint16 = 0x0802
	MsgIDPhotoCommand        uint16 = 0x8801
	MsgIDMultimediaUploadCmd uint16 = 0x8802
	// AUTO-FIX-2026-07-04: 0x0805 按 JT/T 808-2019 标准为"摄像头立即拍摄命令应答"（终端→平台），
	// 非"多媒体检索应答"。原 MultimediaSearchMessage 结构体格式不符标准，已替换为 PhotoCommandRespMessage。
	// MultimediaSearchMessage 结构体保留供旧调用方显式构造/解码，不再经 ParseBody 自动分发。
	MsgIDPhotoCommandResp uint16 = 0x0805
	// Deprecated: MsgIDMultimediaSearchResp 已被 MsgIDPhotoCommandResp 替代，保留仅为向后兼容。
	MsgIDMultimediaSearchResp uint16 = 0x0805
	MsgIDAudioRecordCmd       uint16 = 0x8804
	MsgIDVideoRequest         uint16 = 0x9101
	MsgIDVideoControl         uint16 = 0x9102
	MsgIDTextSend             uint16 = 0x8300
	MsgIDCircularAreaSet      uint16 = 0x8600
	MsgIDCircularAreaDel      uint16 = 0x8601
	MsgIDRectAreaSet          uint16 = 0x8602
	MsgIDRectAreaDel          uint16 = 0x8603
	MsgIDPolygonAreaSet       uint16 = 0x8604
	MsgIDPolygonAreaDel       uint16 = 0x8605
	MsgIDRouteSet             uint16 = 0x8606
	MsgIDRouteDel             uint16 = 0x8607
	MsgIDInfoMenuSet          uint16 = 0x8700
	MsgIDInfoMenuResp         uint16 = 0x0700
	MsgIDInfoPush             uint16 = 0x8701
	MsgIDPhoneCallback        uint16 = 0x8702

	MsgIDDriverID       uint16 = 0x0702
	MsgIDSMSForward     uint16 = 0x8703
	MsgIDSMSForwardResp uint16 = 0x0703
	// AUTO-FIX-2026-06-27: 0x8203 常量重命名为 MsgIDManualAlarmConfirm（人工确认报警）
	MsgIDManualAlarmConfirm uint16 = 0x8203
	MsgIDEventSet           uint16 = 0x8301
	MsgIDEventResp          uint16 = 0x0301
	MsgIDQuestionDown       uint16 = 0x8302
	MsgIDQuestionResp       uint16 = 0x0302
	MsgIDInfoDistribute     uint16 = 0x8303
	// AUTO-FIX-2026-06-27: 新增缺失消息常量(0x8204/0x8304/0x8402/0x8403)
	MsgIDPhoneBookSet      uint16 = 0x8204
	MsgIDInfoService       uint16 = 0x8304
	MsgIDAreaRouteAlarmSet uint16 = 0x8402
	MsgIDAreaRouteAlarmDel uint16 = 0x8403
	// AUTO-FIX-2026-06-27: 0x8404-0x8407 电子运单类消息
	MsgIDEWaybillSet       uint16 = 0x8404
	MsgIDEWaybillDel       uint16 = 0x8405
	MsgIDEWaybillUpload    uint16 = 0x8406
	MsgIDEWaybillResp      uint16 = 0x8407
	MsgIDOverspeedSet      uint16 = 0x8400
	MsgIDOverspeedAlarm    uint16 = 0x0400
	MsgIDFatigueDriveSet   uint16 = 0x8401
	MsgIDFatigueDriveAlarm uint16 = 0x0401
	MsgIDFireAreaSet       uint16 = 0x8608
	MsgIDFireAreaDel       uint16 = 0x8609
	MsgIDFireAreaAlarm     uint16 = 0x0500
	MsgIDCanData           uint16 = 0x0705
	MsgIDElectronicWaybill uint16 = 0x0701

	MsgIDStorageMediaSearch uint16 = 0x0803
	MsgIDStorageMediaUpload uint16 = 0x0804
	MsgIDFileUploadCmd      uint16 = 0x8803
	// AUTO-FIX-2026-07-02 [P3]: 0x0A00 常量别名冲突修复。
	// 经核查 module-protocol-1045 使用独立的 MsgIDADASAlarm=0x0901（见该模块 codec.go），
	// 拥有独立 ParseBody，不复用 jt808.Codec.ParseBody，故 0x0A00 在 808 链路不存在运行时冲突。
	// 0x0A00 按 JT/T 808-2019 标准语义为"终端 RSA 公钥交换"，ParseBody 据此分发至
	// RSAPublicKeyMessage。PassengerCountMessage 结构体保留供旧调用方显式构造/解码，不再经
	// ParseBody 自动分发。
	// Deprecated: MsgIDPassengerCount 为非标准占用，保留仅为兼容旧引用；新代码请使用 MsgIDRSAPublicKey。
	MsgIDPassengerCount uint16 = 0x0A00
	MsgIDBillOperate    uint16 = 0x0B00
	// AUTO-FIX-2026-06-28: 808 RSA 公钥交换消息常量（对照 v3.0 第3/5章）
	// AUTO-FIX-2026-07-02 [P3]: 解除与 MsgIDPassengerCount/MsgIDADASAlarm 的别名依赖，
	// 独立声明并显式赋值，避免"同名不同义"误导。值不变（0x0A00/0x8A00），向后兼容。
	MsgIDRSAPublicKey  uint16 = 0x0A00 // 终端→平台 RSA 公钥交换（808-2019 标准语义）
	MsgIDRSADistribute uint16 = 0x8A00 // 平台→终端 RSA 公钥下发（808-2019 标准语义）

	// AUTO-FIX-2026-06-27: 0x9xxx 系列音视频消息常量（在 jt1078 中实现，这里仅保留常量）
	// Implemented in jt1078 module
	MsgIDRealtimeAVReq1078   uint16 = 0x9101
	MsgIDRealtimeAVCtrl1078  uint16 = 0x9102
	MsgIDTermAVReq1078       uint16 = 0x9103
	MsgIDTermAVResp1078      uint16 = 0x9104
	MsgIDCtrlReq1078         uint16 = 0x9105
	MsgIDCtrlResp1078        uint16 = 0x9106
	MsgIDPlaybackReq1078     uint16 = 0x9201
	MsgIDPlaybackResp1078    uint16 = 0x9202
	MsgIDPlaybackCtrl1078    uint16 = 0x9203
	MsgIDPlaybackCtrlAck1078 uint16 = 0x9204
	MsgIDDownloadReq1078     uint16 = 0x9205
	MsgIDDownloadResp1078    uint16 = 0x9206
	MsgIDRTPData1078         uint16 = 0x1200
	MsgIDAlarmVideoReq1078   uint16 = 0x9401
	MsgIDAlarmVideoResp1078  uint16 = 0x9402
	MsgIDPTZControl1078      uint16 = 0x9301
	MsgIDAVParamSet1078      uint16 = 0x9501
	MsgIDAVParamQuery1078    uint16 = 0x9502
	MsgIDAVParamResp1078     uint16 = 0x9503
	MsgIDTermLogReq1078      uint16 = 0x9601
	MsgIDTermLogResp1078     uint16 = 0x9602
	MsgIDTermLogUpload1078   uint16 = 0x9603

	// AUTO-FIX-2026-07-02 [P3]: 修正误导性注释——0x8A00-0x8A08 并非 jt1045 实际使用的 ADAS 消息 ID。
	// 经核查 module-protocol-1045/codec.go 中 ADAS 报警使用 MsgIDADASAlarm=0x0901（独立 codec），
	// 而非本块的 0x8A00。0x8A00 按 JT/T 808-2019 标准为"平台→终端 RSA 公钥下发"（见 MsgIDRSADistribute）。
	// Deprecated: 以下 MsgIDADAS* 常量保留以兼容旧引用，请勿在新代码中使用；
	// ADAS 相关消息请使用 module-protocol-1045 包中的常量（0x0901 系列）。
	MsgIDADASAlarm      uint16 = 0x8A00
	MsgIDADASAlarmResp  uint16 = 0x8A01
	MsgIDADASData       uint16 = 0x8A02
	MsgIDADASDataResp   uint16 = 0x8A03
	MsgIDADASParamSet   uint16 = 0x8A04
	MsgIDADASParamQuery uint16 = 0x8A05
	MsgIDADASParamResp  uint16 = 0x8A06
	MsgIDADASActive     uint16 = 0x8A07
	MsgIDADASActiveResp uint16 = 0x8A08

	// AUTO-FIX-2026-06-27: 0x1205-0x1304 DSM/ADAS 消息常量（在 jt1045 中实现，这里仅保留常量）
	// Implemented in jt1045 module
	MsgIDDSMAlarm      uint16 = 0x1205
	MsgIDDSMAlarmResp  uint16 = 0x1206
	MsgIDDSMData       uint16 = 0x1207
	MsgIDDSMDataResp   uint16 = 0x1208
	MsgIDDSMParamSet   uint16 = 0x1209
	MsgIDDSMParamQuery uint16 = 0x120A
	MsgIDDSMParamResp  uint16 = 0x120B
	MsgIDDSMActive     uint16 = 0x120C
	MsgIDDSMActiveResp uint16 = 0x120D
	MsgIDADASExtended  uint16 = 0x1304
)

const (
	ResultSuccess        byte = 0
	ResultVehicleReg     byte = 1
	ResultVehicleExists  byte = 2
	ResultNoVehicle      byte = 3
	ResultTerminalReg    byte = 0
	ResultTerminalExists byte = 1
	ResultNoVehicleReg   byte = 2
	ResultTerminalAuth   byte = 3
)

type JT808Codec struct{}

func NewCodec() *JT808Codec {
	return &JT808Codec{}
}

func (c *JT808Codec) ProtocolType() protocol.ProtocolType {
	return protocol.ProtocolJT808
}

func (c *JT808Codec) ParseHeader(data []byte) (*protocol.MessageHeader, int, error) {
	if len(data) < 12 {
		return nil, 0, fmt.Errorf("header too short: %d bytes", len(data))
	}

	header := &protocol.MessageHeader{}
	header.MsgID = binary.BigEndian.Uint16(data[0:2])
	header.BodyAttr = binary.BigEndian.Uint16(data[2:4])

	header.BodyLen = int(header.BodyAttr & 0x03FF)
	header.HasPack = (header.BodyAttr & 0x2000) != 0

	phoneBCD := data[4:10]
	header.Phone = BCDToString(phoneBCD)
	header.SeqNum = binary.BigEndian.Uint16(data[10:12])

	offset := 12
	if header.HasPack {
		if len(data) < offset+4 {
			return nil, 0, fmt.Errorf("header with pack info too short")
		}
		header.PackTotal = binary.BigEndian.Uint16(data[offset : offset+2])
		header.PackIndex = binary.BigEndian.Uint16(data[offset+2 : offset+4])
		offset += 4
	}

	return header, offset, nil
}

func (c *JT808Codec) EncodeHeader(header *protocol.MessageHeader) ([]byte, error) {
	buf := make([]byte, 0, 20)

	msgIDBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(msgIDBytes, header.MsgID)
	buf = append(buf, msgIDBytes...)

	bodyAttr := uint16(header.BodyLen) & 0x03FF
	if header.HasPack {
		bodyAttr |= 0x2000
	}
	bodyAttrBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(bodyAttrBytes, bodyAttr)
	buf = append(buf, bodyAttrBytes...)

	phoneBCD := StringToBCD(header.Phone)
	buf = append(buf, phoneBCD...)

	seqBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(seqBytes, header.SeqNum)
	buf = append(buf, seqBytes...)

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

func (c *JT808Codec) ParseBody(msgID uint16, data []byte) (protocol.MessageBody, error) {
	var body protocol.MessageBody

	switch msgID {
	case MsgIDTerminalGeneralResp:
		body = &TerminalGeneralRespMessage{}
	case MsgIDRegister:
		body = &RegisterMessage{}
	case MsgIDAuth:
		body = &AuthMessage{}
	case MsgIDHeartbeat:
		body = &HeartbeatMessage{}
	case MsgIDTerminalCancel:
		body = &TerminalCancelMessage{}
	case MsgIDLocation:
		body = &LocationMessage{}
	case MsgIDLocationBatch:
		body = &LocationBatchMessage{}
	case MsgIDLocationQueryResp:
		body = &LocationQueryResponse{}
	// AUTO-FIX-2026-06-26: 注册0x8201位置查询请求（平台→终端，消息体为空）
	case MsgIDLocationQuery:
		body = &LocationQueryMessage{}
	// AUTO-FIX-2026-06-26: 注册0x8202临时位置跟踪控制（结构体已存在，原未注册）
	case MsgIDTempLocationTrack:
		body = &TempLocationTrackMessage{}
	case MsgIDTempLocationTrackResp:
		body = &TempLocationTrackRespMessage{}
	case MsgIDGeneralResp:
		body = &GeneralResponse{}
	case MsgIDRegisterResp:
		body = &RegisterResponse{}
	case MsgIDTerminalCancelResp:
		body = &TerminalCancelResponse{}
	case MsgIDCommand:
		body = &CommandMessage{}
	case MsgIDCommandResp:
		body = &CommandRespMessage{}
	case MsgIDParamQuery:
		body = &ParamQueryMessage{}
	case MsgIDParamResp:
		body = &ParamRespMessage{}
	case MsgIDParamSet:
		body = &ParamSetMessage{}
	case MsgIDTerminalCtrl:
		body = &TerminalCtrlMessage{}
	// AUTO-FIX-2026-06-26: 补充车辆控制消息 0x8500 case
	case MsgIDVehicleControl:
		body = &VehicleControlMessage{}
	// FIX-7-1: 0x9101 视频请求消息体注册 [2026-06-26]
	case MsgIDVideoRequest:
		body = &VideoRequestMessage{}
	case MsgIDTerminalPropResp:
		body = &TerminalPropRespMessage{}
	case MsgIDTerminalUpgradeResp:
		body = &TerminalUpgradeRespMessage{}
	case MsgIDMultimedia:
		body = &MultimediaMessage{}
	case MsgIDMultimediaUpload:
		body = &MultimediaUploadMessage{}
	case MsgIDTextSend:
		body = &TextSendMessage{}
	case MsgIDPhotoCommand:
		body = &PhotoCommandMessage{}
	case MsgIDAlarmAck:
		body = &AlarmAckMessage{}
	case MsgIDCircularAreaSet:
		body = &CircularAreaSetMessage{}
	case MsgIDCircularAreaDel:
		body = &CircularAreaDelMessage{}
	case MsgIDRectAreaSet:
		body = &RectAreaSetMessage{}
	case MsgIDRectAreaDel:
		body = &RectAreaDelMessage{}
	case MsgIDPolygonAreaSet:
		body = &PolygonAreaSetMessage{}
	case MsgIDPolygonAreaDel:
		body = &PolygonAreaDelMessage{}
	case MsgIDRouteSet:
		body = &RouteSetMessage{}
	case MsgIDRouteDel:
		body = &RouteDelMessage{}
	case MsgIDFireAreaSet:
		body = &FireAreaSetMessage{}
	case MsgIDFireAreaDel:
		body = &FireAreaDelMessage{}
	case MsgIDFireAreaAlarm:
		body = &FireAreaAlarmMessage{}
	case MsgIDOverspeedSet:
		body = &OverspeedSetMessage{}
	case MsgIDFatigueDriveSet:
		body = &FatigueDriveSetMessage{}
	case MsgIDDriverID:
		body = &DriverIDMessage{}
	case MsgIDCanData:
		body = &CanDataMessage{}
	case MsgIDElectronicWaybill:
		body = &ElectronicWaybillMessage{}
	case MsgIDInfoMenuResp:
		body = &InfoMenuRespMessage{}
	case MsgIDSMSForwardResp:
		body = &SMSForwardRespMessage{}
	case MsgIDEventResp:
		body = &EventRespMessage{}
	case MsgIDQuestionResp:
		body = &QuestionRespMessage{}
	// AUTO-FIX-2026-06-27: 0x8203 常量重命名为 MsgIDManualAlarmConfirm（人工确认报警）
	case MsgIDManualAlarmConfirm:
		body = &ManualAlarmConfirmMessage{}
	case MsgIDAlarm:
		body = &AlarmMessage{}
	case MsgIDStorageMediaSearch:
		body = &StorageMediaSearchMessage{}
	case MsgIDStorageMediaUpload:
		body = &StorageMediaUploadMessage{}
	// AUTO-FIX-2026-07-02 [P3]: 0x0A00 / 0x8A00 分发歧义已核查并明确——
	// 经核查 module-protocol-1045 拥有独立 codec（ADAS 报警=0x0901），不复用本 ParseBody，
	// 故 0x0A00 在 808 链路无运行时冲突。0x0A00 按 808-2019 标准解析为 RSAPublicKeyMessage；
	// PassengerCountMessage 结构体保留供旧调用方显式构造/解码，不再经 ParseBody 自动分发。
	// MsgIDPassengerCount 与 MsgIDRSAPublicKey 同值（0x0A00），仅列一个避免 duplicate case。
	case MsgIDRSAPublicKey:
		body = &RSAPublicKeyMessage{}
	// AUTO-FIX-2026-06-28: 0x8A00 平台→终端 RSA 公钥下发（原仅 ADAS 常量占位，未注册 ParseBody）
	// AUTO-FIX-2026-07-02 [P3]: 0x8A00 按 808-2019 标准为 RSA 公钥下发，非 ADAS 报警
	// （ADAS 报警在 module-protocol-1045 使用 0x0901）。MsgIDADASAlarm 与 MsgIDRSADistribute
	// 同值（0x8A00），仅列一个避免 duplicate case。
	case MsgIDRSADistribute:
		body = &RSADistributeMessage{}
	case MsgIDBillOperate:
		body = &BillOperateMessage{}
	case MsgIDInfoMenuSet:
		body = &InfoMenuSetMessage{}
	case MsgIDQuestionDown:
		body = &QuestionDownMessage{}
	case MsgIDAlarmAttachment:
		body = &AlarmAttachmentMessage{}
	case MsgIDAlarmAttachmentResp:
		body = &AlarmAttachmentRespMessage{}
	// AUTO-FIX-2026-06-26: 注册808协议缺失消息体case（按第一轮.txt要求）
	case MsgIDTerminalUpgrade:
		body = &TerminalUpgradeMessage{}
	case MsgIDTerminalPropQuery:
		body = &TerminalPropQueryMessage{}
	case MsgIDMultimediaUploadCmd:
		body = &MultimediaUploadCmdMessage{}
	case MsgIDFileUploadCmd:
		body = &FileUploadCmdMessage{}
	case MsgIDAudioRecordCmd:
		body = &AudioRecordCmdMessage{}
	// AUTO-FIX-2026-07-04: 0x0805 按 JT/T 808-2019 标准为"摄像头立即拍摄命令应答"（终端→平台），
	// 原 MultimediaSearchMessage 结构体格式不符标准，已替换为 PhotoCommandRespMessage。
	// MsgIDPhotoCommandResp 与 MsgIDMultimediaSearchResp 同值（0x0805），仅列一个避免 duplicate case。
	case MsgIDPhotoCommandResp:
		body = &PhotoCommandRespMessage{}
	case MsgIDPhoneCallback:
		body = &PhoneCallbackMessage{}
	case MsgIDSMSForward:
		body = &SMSForwardMessage{}
	case MsgIDEventSet:
		body = &EventSetMessage{}
	case MsgIDInfoDistribute:
		body = &InfoDistributeMessage{}
	// AUTO-FIX-2026-06-27: 注册 0x0400 超速报警消息（终端→平台）
	case MsgIDOverspeedAlarm:
		body = &OverspeedAlarmMessage{}
	// AUTO-FIX-2026-06-27: 注册 0x0401 疲劳驾驶报警消息（终端→平台）
	case MsgIDFatigueDriveAlarm:
		body = &FatigueDriveAlarmMessage{}
	// AUTO-FIX-2026-06-27: 注册 0x8701 信息点推送消息（平台→终端）
	case MsgIDInfoPush:
		body = &InfoPushMessage{}
	// AUTO-FIX-2026-06-27: 注册 0x9102 实时音视频控制（jt1078实现，这里保留转发结构体）
	case MsgIDVideoControl:
		body = &VideoControlMessage{}
	// AUTO-FIX-2026-06-27: 注册 0x8204 电话本设置消息（平台→终端）
	case MsgIDPhoneBookSet:
		body = &PhoneBookSetMessage{}
	// AUTO-FIX-2026-06-27: 注册 0x8304 信息服务消息（平台→终端）
	case MsgIDInfoService:
		body = &InfoServiceMessage{}
	// AUTO-FIX-2026-06-27: 注册 0x8402 区域路线报警设置（平台→终端）
	case MsgIDAreaRouteAlarmSet:
		body = &AreaRouteAlarmSetMessage{}
	// AUTO-FIX-2026-06-27: 注册 0x8403 区域路线报警删除（平台→终端）
	case MsgIDAreaRouteAlarmDel:
		body = &AreaRouteAlarmDelMessage{}
	// AUTO-FIX-2026-06-28: 0x8404-0x8407 电子运单类消息具体结构体（替换原 RawMessage 占位）
	case MsgIDEWaybillSet:
		body = &EWaybillSetMessage{}
	case MsgIDEWaybillDel:
		body = &EWaybillDelMessage{}
	case MsgIDEWaybillUpload:
		body = &EWaybillUploadMessage{}
	case MsgIDEWaybillResp:
		body = &EWaybillRespMessage{}
	default:
		body = &RawMessage{ID: msgID, Data: data}
	}

	if err := body.Unmarshal(data); err != nil {
		return nil, fmt.Errorf("unmarshal msg 0x%04X: %w", msgID, err)
	}

	return body, nil
}

func (c *JT808Codec) EncodeBody(body protocol.MessageBody) ([]byte, error) {
	return body.Marshal()
}

func (c *JT808Codec) VerifyChecksum(data []byte) bool {
	if len(data) < 2 {
		return false
	}
	var xor byte
	for i := 0; i < len(data)-1; i++ {
		xor ^= data[i]
	}
	return xor == data[len(data)-1]
}

func CalcChecksum(data []byte) byte {
	var xor byte
	for _, b := range data {
		xor ^= b
	}
	return xor
}

// AUTO-FIX-2026-06-29 [P0]: 原 BCDToString 剥除前导零，导致 SIM/Phone（6字节 BCD = 12 位
// 定长数字，常以 0 开头如 013800000000）解码后丢失前导零（→ 13800000000），引发：
//  1. 终端 0x0100 注册时 byPhone 索引建立错误，后续指令下发失败
//  2. 与 1078 视频侧 streamID 不一致，视频不通
//  3. DB 存储的 SIM 与协议解码不一致，跨系统对账错误
//
// 修复：SIM/Phone 是定长 BCD 字段，不应剥除前导零。已移除剥零循环。
func BCDToString(bcd []byte) string {
	result := make([]byte, 0, len(bcd)*2)
	for _, b := range bcd {
		result = append(result, (b>>4)+'0', (b&0x0F)+'0')
	}
	return string(result)
}

// BCDToStringFixed 将 BCD 字节转换为字符串，不剥除前导零。
// AUTO-FIX-2026-06-26: 用于时间字段（BCD[6]=YYMMDDHHmmss）等定长BCD字段，
// 原 BCDToString 会剥除前导零导致 "000101000000" 变为 "101000000"（长度错乱）。
func BCDToStringFixed(bcd []byte) string {
	result := make([]byte, 0, len(bcd)*2)
	for _, b := range bcd {
		high := (b >> 4) + '0'
		low := (b & 0x0F) + '0'
		result = append(result, high, low)
	}
	return string(result)
}

func StringToBCD(s string) []byte {
	for len(s) < 12 {
		s = "0" + s
	}
	if len(s) > 12 {
		s = s[len(s)-12:]
	}
	bcd := make([]byte, 6)
	for i := 0; i < 6; i++ {
		high := s[i*2] - '0'
		low := s[i*2+1] - '0'
		bcd[i] = (high << 4) | low
	}
	return bcd
}
