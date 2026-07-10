package handler

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/suoten/jt-engine/internal/gateway"
	"github.com/suoten/jt-engine/pkg/protocol"
	"github.com/suoten/jt-engine/pkg/protocol/jt808"
	"go.uber.org/zap"
)

type PendingCommand struct {
	MsgID    uint16
	SeqNum   uint16
	SentAt   int64
	Result   byte
	Done     chan struct{}
}

type CommandSender struct {
	sessions       *gateway.SessionManager
	codec          *jt808.JT808Codec
	logger         *zap.Logger
	seqNum         uint32
	pending        map[uint16]*PendingCommand
	pendingMu      sync.RWMutex
}

func NewCommandSender(sessions *gateway.SessionManager, logger *zap.Logger) *CommandSender {
	return &CommandSender{
		sessions: sessions,
		codec:    jt808.NewCodec(),
		logger:   logger,
		pending:  make(map[uint16]*PendingCommand),
	}
}

func (cs *CommandSender) nextSeq() uint16 {
	return uint16(atomic.AddUint32(&cs.seqNum, 1))
}

func (cs *CommandSender) SendToDevice(phone string, msgID uint16, body protocol.MessageBody) error {
	session, ok := cs.sessions.GetByPhone(phone)
	if !ok {
		return fmt.Errorf("device %s not online", phone)
	}

	if session.Conn == nil {
		return fmt.Errorf("device %s has no connection", phone)
	}

	// AUTO-FIX-2026-06-30 [P0-1]: 对接 session.Send，由 session 统一负责
	// 序号分配、组帧（header+body+校验码）、转义、首尾分隔符与发送队列串行化写入。
	// 原实现手动编码后调用 session.Write，存在两条并行的下行组帧路径，
	// 且 CommandSender 自维护的 seqNum 与 session.seqNum 双计数器易产生序号冲突。
	// 现统一收敛到 session.Send，确保 0x8103/0x8300/0x9101/0x9203 等指令
	// 经 per-session sendLoop 串行化下发，避免并发写冲突。
	bodyData, err := cs.codec.EncodeBody(body)
	if err != nil {
		return fmt.Errorf("encode body: %w", err)
	}

	if err := session.Send(msgID, bodyData); err != nil {
		return fmt.Errorf("write to connection: %w", err)
	}

	cs.logger.Info("command sent to device",
		zap.String("phone", phone),
		zap.Uint16("msg_id", msgID),
		zap.Int("bytes", len(bodyData)))

	return nil
}

func (cs *CommandSender) SendGeneralResponse(phone string, resp *jt808.TerminalGeneralRespMessage) error {
	return cs.SendToDevice(phone, jt808.MsgIDGeneralResp, resp)
}

func (cs *CommandSender) SendTextMessage(phone string, text string, sign byte) error {
	msg := &jt808.TextSendMessage{
		Sign: sign,
		Text: text,
	}
	return cs.SendToDevice(phone, jt808.MsgIDTextSend, msg)
}

func (cs *CommandSender) SendLocationQuery(phone string) error {
	msg := &jt808.RawMessage{Data: nil}
	return cs.SendToDevice(phone, jt808.MsgIDLocationQuery, msg)
}

func (cs *CommandSender) SendPhotoCommand(phone string, channelId byte, cmd byte, time uint16, saveFlag byte, resolution byte) error {
	msg := &jt808.PhotoCommandMessage{
		ChannelID:  channelId,
		Cmd:        cmd,
		Time:       time,
		SaveFlag:   saveFlag,
		Resolution: resolution,
	}
	return cs.SendToDevice(phone, jt808.MsgIDPhotoCommand, msg)
}

func (cs *CommandSender) BuildCommandMessage(seqNum uint16, params map[uint32][]byte) *jt808.CommandMessage {
	return &jt808.CommandMessage{
		SeqNum: seqNum,
		Params: params,
	}
}

func (cs *CommandSender) IsDeviceOnline(phone string) bool {
	_, ok := cs.sessions.GetByPhone(phone)
	return ok
}

func (cs *CommandSender) GetDeviceConn(phone string) (net.Conn, bool) {
	session, ok := cs.sessions.GetByPhone(phone)
	if !ok || session.Conn == nil {
		return nil, false
	}
	return session.Conn, true
}

func (cs *CommandSender) SendSetTerminalParams(phone string, params map[uint32][]byte) error {
	seq := cs.nextSeq()
	msg := cs.BuildCommandMessage(seq, params)
	return cs.SendToDevice(phone, jt808.MsgIDCommand, msg)
}

func (cs *CommandSender) SendTerminalControl(phone string, ctrlType byte, param []byte) error {
	msg := &jt808.TerminalCtrlMessage{
		CtrlType: ctrlType,
		Param:    param,
	}
	return cs.SendToDevice(phone, jt808.MsgIDTerminalCtrl, msg)
}

// SendTempLocationTrack 发送 0x8202 临时位置跟踪控制请求。
// interval 为位置上报时间间隔（秒），validity 为跟踪有效期（秒，0表示一直跟踪）。
func (cs *CommandSender) SendTempLocationTrack(phone string, interval uint16, validity uint16) error {
	msg := &jt808.TempLocationTrackMessage{
		Interval: interval,
		Validity: validity,
	}
	return cs.SendToDevice(phone, jt808.MsgIDTempLocationTrack, msg)
}

// SendManualAlarmConfirm 发送 0x8203 人工确认报警消息。
// alarmFlag 报警标志：bit0=紧急报警 bit1=碰撞侧翻报警。
// AUTO-FIX-2026-06-27: 0x8203 常量重命名为 MsgIDManualAlarmConfirm
func (cs *CommandSender) SendManualAlarmConfirm(phone string, alarmFlag uint16) error {
	msg := &jt808.ManualAlarmConfirmMessage{
		AlarmFlag: alarmFlag,
	}
	return cs.SendToDevice(phone, jt808.MsgIDManualAlarmConfirm, msg)
}

func (cs *CommandSender) SendOverspeedSet(phone string, msg *jt808.OverspeedSetMessage) error {
	return cs.SendToDevice(phone, jt808.MsgIDOverspeedSet, msg)
}

func (cs *CommandSender) SendFatigueDriveSet(phone string, msg *jt808.FatigueDriveSetMessage) error {
	return cs.SendToDevice(phone, jt808.MsgIDFatigueDriveSet, msg)
}

func (cs *CommandSender) SendCircularAreaSet(phone string, msg *jt808.CircularAreaSetMessage) error {
	return cs.SendToDevice(phone, jt808.MsgIDCircularAreaSet, msg)
}

func (cs *CommandSender) SendCircularAreaDel(phone string, areaIDs []uint32) error {
	msg := &jt808.CircularAreaDelMessage{AreaIDs: areaIDs}
	return cs.SendToDevice(phone, jt808.MsgIDCircularAreaDel, msg)
}

func (cs *CommandSender) SendRectAreaSet(phone string, msg *jt808.RectAreaSetMessage) error {
	return cs.SendToDevice(phone, jt808.MsgIDRectAreaSet, msg)
}

func (cs *CommandSender) SendRectAreaDel(phone string, areaIDs []uint32) error {
	msg := &jt808.RectAreaDelMessage{AreaIDs: areaIDs}
	return cs.SendToDevice(phone, jt808.MsgIDRectAreaDel, msg)
}

func (cs *CommandSender) SendPolygonAreaSet(phone string, msg *jt808.PolygonAreaSetMessage) error {
	return cs.SendToDevice(phone, jt808.MsgIDPolygonAreaSet, msg)
}

func (cs *CommandSender) SendPolygonAreaDel(phone string, areaIDs []uint32) error {
	msg := &jt808.PolygonAreaDelMessage{AreaIDs: areaIDs}
	return cs.SendToDevice(phone, jt808.MsgIDPolygonAreaDel, msg)
}

func (cs *CommandSender) SendRouteSet(phone string, msg *jt808.RouteSetMessage) error {
	return cs.SendToDevice(phone, jt808.MsgIDRouteSet, msg)
}

func (cs *CommandSender) SendRouteDel(phone string, routeIDs []uint32) error {
	msg := &jt808.RouteDelMessage{RouteIDs: routeIDs}
	return cs.SendToDevice(phone, jt808.MsgIDRouteDel, msg)
}

func (cs *CommandSender) SendFireAreaSet(phone string, msg *jt808.FireAreaSetMessage) error {
	return cs.SendToDevice(phone, jt808.MsgIDFireAreaSet, msg)
}

func (cs *CommandSender) SendFireAreaDel(phone string, areaIDs []uint32) error {
	msg := &jt808.FireAreaDelMessage{AreaIDs: areaIDs}
	return cs.SendToDevice(phone, jt808.MsgIDFireAreaDel, msg)
}

func (cs *CommandSender) SendInfoMenuSet(phone string, msg *jt808.InfoMenuSetMessage) error {
	return cs.SendToDevice(phone, jt808.MsgIDInfoMenuSet, msg)
}

func (cs *CommandSender) SendQuestionDown(phone string, msg *jt808.QuestionDownMessage) error {
	return cs.SendToDevice(phone, jt808.MsgIDQuestionDown, msg)
}

func (cs *CommandSender) SendParamQuery(phone string, paramIDs []uint32) error {
	seq := cs.nextSeq()
	msg := &jt808.ParamQueryMessage{
		SeqNum:   seq,
		ParamIDs: paramIDs,
	}
	return cs.SendToDevice(phone, jt808.MsgIDParamQuery, msg)
}

// AUTO-FIX-2026-06-27: ParamSetMessage 移除 SeqNum 字段，参数项改为仅ID列表（0x8106标准）
func (cs *CommandSender) SendParamSet(phone string, paramIDs []uint32) error {
	msg := &jt808.ParamSetMessage{
		ParamIDs: paramIDs,
	}
	return cs.SendToDevice(phone, jt808.MsgIDParamSet, msg)
}

func (cs *CommandSender) SendTerminalPropQuery(phone string) error {
	msg := &jt808.RawMessage{Data: nil}
	return cs.SendToDevice(phone, jt808.MsgIDTerminalPropQuery, msg)
}

func (cs *CommandSender) SendAlarmAck(phone string, respSeqNum uint16, alarmType uint32, alarmID uint16) error {
	msg := &jt808.AlarmAckMessage{
		RespSeqNum: respSeqNum,
		AlarmType:  alarmType,
		AlarmID:    alarmID,
	}
	return cs.SendToDevice(phone, jt808.MsgIDAlarmAck, msg)
}

// SendTerminalUpgrade 发送 0x8108 终端升级指令。
// body 为完整的升级指令消息体（升级类型+制造商ID+版本号等），由调用方按 JT/T 808 标准组装。
func (cs *CommandSender) SendTerminalUpgrade(phone string, body []byte) error {
	msg := &jt808.RawMessage{Data: body}
	return cs.SendToDevice(phone, jt808.MsgIDTerminalUpgrade, msg)
}

// SendMultimediaUploadCmd 发送 0x8802 多媒体上传指令。
// multimediaID 为多媒体ID，retransmitPackets 为需要重传的包序号列表（空表示上传全部）。
func (cs *CommandSender) SendMultimediaUploadCmd(phone string, multimediaID uint32, retransmitPackets []uint16) error {
	buf := make([]byte, 4)
	buf[0] = byte(multimediaID >> 24)
	buf[1] = byte(multimediaID >> 16)
	buf[2] = byte(multimediaID >> 8)
	buf[3] = byte(multimediaID)
	for _, p := range retransmitPackets {
		buf = append(buf, byte(p>>8), byte(p))
	}
	msg := &jt808.RawMessage{Data: buf}
	return cs.SendToDevice(phone, jt808.MsgIDMultimediaUploadCmd, msg)
}

// SendFileUploadCmd 发送 0x8803 文件上传指令。
// body 为完整的文件上传指令消息体，由调用方按 JT/T 808 标准组装。
func (cs *CommandSender) SendFileUploadCmd(phone string, body []byte) error {
	msg := &jt808.RawMessage{Data: body}
	return cs.SendToDevice(phone, jt808.MsgIDFileUploadCmd, msg)
}

// SendAudioRecordCmd 发送 0x8804 录音指令。
// recordTime 为录音时间（秒），recordCmd 为录音命令(0x00停止 0x01开始)，saveFlag 为保存标志，audioSample 为音频采样率。
// AUTO-FIX-2026-06-27: AudioRecordCmdMessage 新增 RecordCmd(1B) 字段，体改为5B
func (cs *CommandSender) SendAudioRecordCmd(phone string, recordTime uint16, recordCmd byte, saveFlag byte, audioSample byte) error {
	msg := &jt808.AudioRecordCmdMessage{
		RecordTime:  recordTime,
		RecordCmd:   recordCmd,
		SaveFlag:    saveFlag,
		AudioSample: audioSample,
	}
	return cs.SendToDevice(phone, jt808.MsgIDAudioRecordCmd, msg)
}

func (cs *CommandSender) HandleGeneralResp(resp *jt808.TerminalGeneralRespMessage) {
	cs.pendingMu.RLock()
	defer cs.pendingMu.RUnlock()

	if pending, ok := cs.pending[resp.RespSeqNum]; ok {
		pending.Result = resp.RespResult
		close(pending.Done)
	}
}

func (cs *CommandSender) SendAndWait(phone string, msgID uint16, body protocol.MessageBody) (byte, error) {
	session, ok := cs.sessions.GetByPhone(phone)
	if !ok {
		return 0, fmt.Errorf("device %s not online", phone)
	}
	if session.Conn == nil {
		return 0, fmt.Errorf("device %s has no connection", phone)
	}

	// AUTO-FIX-2026-06-30 [P0-1]: 对接 session.SendWithSeq，由 session 分配下行序号。
	// 原实现用 cs.nextSeq() 自维护序号，与 session.seqNum 双计数器并存易冲突；
	// 现统一由 session 分配序号并返回，供 pending 应答匹配使用。
	// 注意：session.SendWithSeq 仅在帧入队（enqueueSend）成功后返回 seq，
	// 实际网络写入由 sendLoop 异步执行；pending 在 seq 返回后立即注册，
	// 写入与设备应答之间的网络 RTT 远大于注册耗时，无应答丢失风险。
	bodyData, err := cs.codec.EncodeBody(body)
	if err != nil {
		return 0, fmt.Errorf("encode body: %w", err)
	}

	seq, err := session.SendWithSeq(msgID, bodyData)
	if err != nil {
		return 0, fmt.Errorf("write to connection: %w", err)
	}

	pending := &PendingCommand{
		MsgID:  msgID,
		SeqNum: seq,
		SentAt: time.Now().Unix(),
		Done:   make(chan struct{}),
	}

	cs.pendingMu.Lock()
	cs.pending[seq] = pending
	cs.pendingMu.Unlock()

	defer func() {
		cs.pendingMu.Lock()
		delete(cs.pending, seq)
		cs.pendingMu.Unlock()
	}()

	select {
	case <-pending.Done:
		return pending.Result, nil
	case <-time.After(30 * time.Second):
		return 0, fmt.Errorf("command timeout: no response from device %s", phone)
	}
}