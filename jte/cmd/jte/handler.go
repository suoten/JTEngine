package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/suoten/jt-engine/internal/gateway"
	jteMetrics "github.com/suoten/jt-engine/internal/metrics"
	"github.com/suoten/jt-engine/internal/trace"
	"github.com/suoten/jt-engine/pkg/handler"
	"github.com/suoten/jt-engine/pkg/merge"
	"github.com/suoten/jt-engine/pkg/protocol"
	jt808 "github.com/suoten/jt-engine/pkg/protocol/jt808"
	jt1078 "github.com/suoten/jt-engine/pkg/protocol/jt1078"
	"github.com/suoten/jt-engine/pkg/storage"
	"go.uber.org/zap"
)

type gatewaySessionAdapter struct {
	*gateway.Session
}

func (a gatewaySessionAdapter) GetID() string                          { return a.Session.ID }
func (a gatewaySessionAdapter) GetPhone() string                       { return a.Session.GetPhone() }
func (a gatewaySessionAdapter) GetProtocol() protocol.ProtocolType     { return a.Session.GetProtocol() }
func (a gatewaySessionAdapter) UpdateActivity()                        { a.Session.UpdateActivity() }
func (a gatewaySessionAdapter) SetProtocol(pt protocol.ProtocolType)   { a.Session.SetProtocol(pt) }
func (a gatewaySessionAdapter) Write(data []byte) (int, error)         { return a.Session.Write(data) }

type MessageHandler struct {
	store           storage.Interface
	sessions        *gateway.SessionManager
	merge           *merge.Engine
	eventBus        *merge.EventBus
	limiter         *gateway.Limiter
	logger          *zap.Logger
	protocolHub     *protocol.Hub
	handlerRegistry *handler.HandlerRegistry
	videoEngine     *jt1078.VideoEngine
	respSeqNum      atomic.Uint32
	// onCommandResp ڽնͨӦ͸ CommandSender SendAndWait  pending 
	onCommandResp func(resp *jt808.TerminalGeneralRespMessage)
	// reassembler 808 ְնϱķְϢλáýݣ
	reassembler *jt808.PacketReassembler
	// authCodeMgr ȨP0-1ǿȨ + α/¡
	// nil ʱ˵߼ֻ AuthCodeδעʱݣע롣
	authCodeMgr *gateway.AuthCodeManager
	// cache ѡ Redis 㣨P1-2 Ȩ 24h / P1-5 ״̬
	// nil ʱлӰ̡
	cache storage.CacheStorage
	// licenseValidator ȨУAUTO-FIX-2026-06-30 [-6]
	// nil ʱȨУ飬 limiter ޡ
	licenseValidator LicenseValidator
}

// LicenseValidator ȨУӿڣ-6 洢ּۣ
//  module.LicenseManager ʵ֣handler ͨ˽ӿڽ
type LicenseValidator interface {
	ValidateVehicleCount(currentCount int) error
	ValidateArchive() error
}

// SetLicenseValidator עȨУ-6
func (h *MessageHandler) SetLicenseValidator(v LicenseValidator) {
	h.licenseValidator = v
}

func NewMessageHandler(
	store storage.Interface,
	sessions *gateway.SessionManager,
	mergeEngine *merge.Engine,
	limiter *gateway.Limiter,
	logger *zap.Logger,
	protocolHub *protocol.Hub,
	handlerRegistry *handler.HandlerRegistry,
	videoEngine *jt1078.VideoEngine,
) *MessageHandler {
	var eventBus *merge.EventBus
	if mergeEngine != nil {
		eventBus = mergeEngine.GetEventBus()
	}
	return &MessageHandler{
		store:           store,
		sessions:        sessions,
		merge:           mergeEngine,
		eventBus:        eventBus,
		limiter:         limiter,
		logger:          logger,
		protocolHub:     protocolHub,
		handlerRegistry: handlerRegistry,
		videoEngine:     videoEngine,
		reassembler:     jt808.NewPacketReassembler(5 * time.Minute),
	}
}

// SetVideoEngine allows late binding of the VideoEngine (it is created after
// the message handler in main.go).
func (h *MessageHandler) SetVideoEngine(engine *jt1078.VideoEngine) {
	h.videoEngine = engine
}

// SetAuthCodeManager עȨP0-1 ȫӹ̣
//  Start ǰע룻δעʱ˵߼ֻ AuthCodeȫݣ
func (h *MessageHandler) SetAuthCodeManager(mgr *gateway.AuthCodeManager) {
	h.authCodeMgr = mgr
}

// SetCacheStorage ע Redis 㣨P1-2 Ȩ / P1-5 ״̬
// ѡnil ʱл
func (h *MessageHandler) SetCacheStorage(c storage.CacheStorage) {
	h.cache = c
}

// HandleSessionTimeout ʱԴP1-5
//  HeartbeatChecker ͨ SetTimeoutHook ãӹرǰԴ
//   1. ³״̬ϵ㣩
//   2. Ȩ루ֹ븴ã
//   3. ֹͣ豸ƵVideoEngine ע˿ڣ
//   4. ¼־ƣ
//
// SessionManager Ƴ session ͷ handleConn  defer sessions.Remove
//  session.Close() ɣӹرպ󴥷
func (h *MessageHandler) HandleSessionTimeout(session *gateway.Session) {
	phone := session.GetPhone()
	// 1. ³״̬
	if err := h.store.UpdateVehicleOnline(context.Background(), session.ID, false); err != nil {
		h.logger.Error("ʱ³ʧ", zap.Error(err))
	}
	// 1b. AUTO-FIX-2026-06-30 [P1-5]:  Redis ״̬
	if h.cache != nil {
		if err := h.cache.DeleteOnlineState(context.Background(), session.ID); err != nil {
			h.logger.Debug(" Redis ״̬ʧ", zap.String("id", session.ID), zap.Error(err))
		}
	}
	// 2. Ȩ
	if h.authCodeMgr != nil {
		h.authCodeMgr.Revoke(phone)
	}
	// 3. ֹͣ豸Ƶע˿ڣ ZLMediaKit ֹͣ
	if h.videoEngine != nil && phone != "" {
		//  vehicleID ע豸ͨstreamID ʽ: {vehicleID}_ch{channel}
		// VideoEngine.UnregisterStreamPort  streamID ȷע˴ vehicleID ǰ׺
		// ͨ 1-161078 ߼ͨΧʵ VideoEngine ڲԲڵ streamID
		for ch := 1; ch <= 16; ch++ {
			streamID := fmt.Sprintf("%s_ch%d", session.ID, ch)
			h.videoEngine.UnregisterStreamPort(streamID)
		}
	}
	// 4. ¼־
	h.logger.Info("豸ʱ",
		zap.String("session_id", session.ID),
		zap.String("phone", phone),
		zap.String("remote_addr", session.RemoteAddr))
}

// SetCommandRespCallback ָӦصڽնͨӦ͸ CommandSender
//  SendAndWait ·ָյն 0x0001 ͨӦʱܱѣ 30 볬ʱ
func (h *MessageHandler) SetCommandRespCallback(cb func(resp *jt808.TerminalGeneralRespMessage)) {
	h.onCommandResp = cb
}

func (h *MessageHandler) nextRespSeq() uint16 {
	return uint16(h.respSeqNum.Add(1))
}

func (h *MessageHandler) send808Response(session *gateway.Session, msgID uint16, body protocol.MessageBody, reqSeq uint16) {
	codec, ok := h.protocolHub.GetCodec(protocol.ProtocolJT808)
	if !ok {
		h.logger.Error("808 codec not found for response")
		return
	}

	bodyData, err := codec.EncodeBody(body)
	if err != nil {
		h.logger.Error("encode 808 response body failed", zap.Error(err))
		return
	}

	header := &protocol.MessageHeader{
		MsgID:    msgID,
		BodyLen:  len(bodyData),
		Phone:    session.GetPhone(),
		SeqNum:   reqSeq,
		HasPack:  false,
	}

	headerData, err := codec.EncodeHeader(header)
	if err != nil {
		h.logger.Error("encode 808 response header failed", zap.Error(err))
		return
	}

	rawFrame := make([]byte, 0, len(headerData)+len(bodyData)+1)
	rawFrame = append(rawFrame, headerData...)
	rawFrame = append(rawFrame, bodyData...)

	checksum := jt808.CalcChecksum(rawFrame)
	rawFrame = append(rawFrame, checksum)

	escaped := jt808.Escape(rawFrame)
	frame := jt808.WrapWithDelimiter(escaped)

	if _, err := session.Write(frame); err != nil {
		h.logger.Error("send 808 response failed", zap.Error(err), zap.String("session", session.ID))
	} else {
		// AUTO-FIX-2026-07-02 [ɹ۲]: Ϣͼָ
		jteMetrics.MessagesSentTotal.IncWithLabels(map[string]string{
			"protocol": string(protocol.ProtocolJT808),
		})
		h.logger.Debug("sent 808 response",
			zap.Uint16("msg_id", msgID),
			zap.String("phone", session.GetPhone()))
	}
}

func (h *MessageHandler) send809Response(session *gateway.Session, msgID uint16, body protocol.MessageBody, reqSeq uint16) {
	codec, ok := h.protocolHub.GetCodec(protocol.ProtocolJT809)
	if !ok {
		h.logger.Error("809 codec not found for response")
		return
	}

	bodyData, err := codec.EncodeBody(body)
	if err != nil {
		h.logger.Error("encode 809 response body failed", zap.Error(err))
		return
	}

	header := &protocol.MessageHeader{
		MsgID:   msgID,
		BodyLen: len(bodyData),
		SeqNum:  reqSeq,
		HasPack: false,
	}

	headerData, err := codec.EncodeHeader(header)
	if err != nil {
		h.logger.Error("encode 809 response header failed", zap.Error(err))
		return
	}

	if ph, ok := h.handlerRegistry.Get(protocol.ProtocolJT809); ok {
		if err := ph.HandleMessage(gatewaySessionAdapter{session}, &protocol.Message{Header: *header, Body: body}, h.protocolHub); err != nil {
			h.logger.Error("809 handler send response failed", zap.Error(err))
		} else {
			// AUTO-FIX-2026-07-02 [ɹ۲]: Ϣͼָ
			jteMetrics.MessagesSentTotal.IncWithLabels(map[string]string{
				"protocol": string(protocol.ProtocolJT809),
			})
		}
		return
	}

	_ = headerData
	_ = bodyData
	h.logger.Warn("809 handler not registered, cannot send response")
}

// isJT808Family жЭǷ JT/T 808 㣨 808 Ϣͷ SeqNum
// JT808/1078/1045/905/1253  SeqNum һ£ͳһȥء
// 809  GBT32960 иԵĴӦƣڴȥء
func isJT808Family(pt protocol.ProtocolType) bool {
	switch pt {
	case protocol.ProtocolJT808, protocol.ProtocolJT1078, protocol.ProtocolJT1045,
		protocol.ProtocolJT905, protocol.ProtocolJT1253:
		return true
	}
	return false
}

func (h *MessageHandler) Handle(session *gateway.Session, msg *protocol.Message) {
	// AUTO-FIX-2026-07-02 [ɹ۲]: ؼ· span עϢաӦ
	// span ưЭϢID Jaeger аϢɸѡ·
	spanName := fmt.Sprintf("msg.recv.%s.0x%04X", session.GetProtocol(), msg.Header.MsgID)
	span, ctx := trace.StartSpan(context.Background(), spanName, h.logger)
	defer span.End()

	// ע device_id  context־淶Ҫ trace_id + device_id
	ctx = trace.WithDeviceID(ctx, session.GetPhone())
	span.Logger().Debug("message received",
		zap.String("phone", session.GetPhone()),
		zap.String("protocol", string(session.GetProtocol())),
		zap.Uint16("msg_id", msg.Header.MsgID),
		zap.Uint16("seq", msg.Header.SeqNum),
		zap.Int("body_len", len(msg.Raw)))

	// AUTO-FIX-2026-06-30 [-7]: Ϣָ꣨Эǩ
	jteMetrics.MessagesReceivedTotal.IncWithLabels(map[string]string{
		"protocol": string(session.GetProtocol()),
	})
	// ݣָ
	jteMetrics.MessagesTotal.IncWithLabels(map[string]string{
		"protocol": string(session.GetProtocol()),
	})

	h.logProtocolMessage(session, msg, "up")

	// AUTO-FIX-2026-06-29 [P1]: Ϣ SeqNum ȥء
	// նδյ 0x8001 Ӧʱشͬ SeqNum Ϣظλд/
	//  808 Э壨 808 ͷȥأų
	//   - ְϢHasPack=trueƬ handle808 ̴ SeqNum ȥػ󶪷Ƭ
	//   - 0x0001 նͨӦȥػᵼ CommandSender.SendAndWait 
	// ظʱ 0x8001 Ӧ𣨱ն˼شҵ
	if isJT808Family(session.GetProtocol()) && !msg.Header.HasPack && msg.Header.MsgID != jt808.MsgIDTerminalGeneralResp {
		if session.CheckDuplicate(msg.Header.SeqNum) {
			h.logger.Debug("duplicate uplink seq num, skipping",
				zap.String("phone", session.GetPhone()),
				zap.Uint16("seq", msg.Header.SeqNum),
				zap.Uint16("msg_id", msg.Header.MsgID))
			resp := &jt808.GeneralResponse{
				RespSeqNum: msg.Header.SeqNum,
				RespMsgID:  msg.Header.MsgID,
				Result:     0x00,
			}
			h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
			return
		}
	}

	// Ϣ spanЭַ
	procSpan, _ := trace.StartSpan(ctx, fmt.Sprintf("msg.process.%s", session.GetProtocol()), h.logger)
	defer procSpan.End()

	switch session.GetProtocol() {
	case protocol.ProtocolJT808:
		h.handle808(session, msg)
	case protocol.ProtocolJT809:
		h.handle809(session, msg)
	case protocol.ProtocolJT1078:
		h.handle1078(session, msg)
	case protocol.ProtocolJT1045:
		h.handle1045(session, msg)
	case protocol.ProtocolJT905:
		h.handle905(session, msg)
	case protocol.ProtocolJT1253:
		h.handle1253(session, msg)
	case protocol.ProtocolGBT32960:
		h.handle32960(session, msg)
	default:
		h.logger.Warn("unknown protocol", zap.String("protocol", string(session.GetProtocol())))
	}
}

func (h *MessageHandler) handle808(session *gateway.Session, msg *protocol.Message) {
	// ȷ 808 Ϣ phone Ϣͷͬ session
	// עϢ handleRegister еϢڴ˴ȷ phone һ
	if msg.Header.Phone != "" && session.GetPhone() == "" {
		session.SetPhone(msg.Header.Phone)
	}

	// ְ飺 HasPack=true ʱϢǷƬҪ沢зƬٴ
	if msg.Header.HasPack {
		frag, ok := msg.Body.(*protocol.RawFragment)
		if !ok {
			// ݣԴ Marshal ȡ body bytes
			bodyBytes, err := msg.Body.Marshal()
			if err != nil {
				h.logger.Error("fragment marshal failed", zap.Error(err))
				return
			}
			frag = protocol.NewRawFragment(msg.Header.MsgID, bodyBytes)
		}
		complete, ready, err := h.reassembler.Feed(&msg.Header, frag.Data)
		if err != nil {
			h.logger.Error("packet reassemble failed",
				zap.Error(err),
				zap.String("phone", session.GetPhone()),
				zap.Uint16("seq", msg.Header.SeqNum),
				zap.Uint16("pack_index", msg.Header.PackIndex),
				zap.Uint16("pack_total", msg.Header.PackTotal))
			return
		}
		if !ready {
			h.logger.Debug("packet fragment cached, waiting for more",
				zap.String("phone", session.GetPhone()),
				zap.Uint16("seq", msg.Header.SeqNum),
				zap.Uint16("pack_index", msg.Header.PackIndex),
				zap.Uint16("pack_total", msg.Header.PackTotal))
			return
		}

		// зƬ룬 body ½
		codec, ok := h.protocolHub.GetCodec(protocol.ProtocolJT808)
		if !ok {
			h.logger.Error("808 codec not found for reassembly")
			return
		}
		body, err := codec.ParseBody(msg.Header.MsgID, complete)
		if err != nil {
			h.logger.Error("reassembled body parse failed",
				zap.Error(err),
				zap.Uint16("msg_id", msg.Header.MsgID),
				zap.Int("body_len", len(complete)))
			return
		}
		// Ϣ滻ƬϢ
		msg = &protocol.Message{
			Header: msg.Header,
			Body:   body,
			Raw:    msg.Raw,
		}
		h.logger.Info("packet reassembled successfully",
			zap.String("phone", session.GetPhone()),
			zap.Uint16("msg_id", msg.Header.MsgID),
			zap.Uint16("seq", msg.Header.SeqNum),
			zap.Uint16("pack_total", msg.Header.PackTotal),
			zap.Int("body_len", len(complete)))
	}

	switch msg.Header.MsgID {
	case jt808.MsgIDRegister:
		h.handleRegister(session, msg)
	case jt808.MsgIDAuth:
		h.handleAuth(session, msg)
	case jt808.MsgIDHeartbeat:
		h.handleHeartbeat(session, msg)
	case jt808.MsgIDTerminalGeneralResp:
		h.handleTerminalGeneralResp(session, msg)
	case jt808.MsgIDTerminalCancel:
		h.handleTerminalCancel(session, msg)
	case jt808.MsgIDLocation:
		h.handleLocation(session, msg)
	case jt808.MsgIDLocationBatch:
		h.handleLocationBatch(session, msg)
	case jt808.MsgIDLocationQueryResp:
		h.handleLocationQueryResp(session, msg)
	case jt808.MsgIDTempLocationTrackResp:
		h.handleTempLocationTrackResp(session, msg)
	case jt808.MsgIDAlarm:
		h.handleAlarm(session, msg)
	case jt808.MsgIDAlarmAttachment:
		h.handleAlarmAttachment(session, msg)
	case jt808.MsgIDMultimedia:
		h.handleMultimedia(session, msg)
	case jt808.MsgIDMultimediaUpload:
		h.handleMultimediaUpload(session, msg)
	case jt808.MsgIDDriverID:
		h.handleDriverID(session, msg)
	case jt808.MsgIDCanData:
		h.handleCanData(session, msg)
	case jt808.MsgIDElectronicWaybill:
		h.handleElectronicWaybill(session, msg)
	case jt808.MsgIDInfoMenuResp:
		h.handleInfoMenuResp(session, msg)
	case jt808.MsgIDSMSForwardResp:
		h.handleSMSForwardResp(session, msg)
	case jt808.MsgIDEventResp:
		h.handleEventResp(session, msg)
	case jt808.MsgIDCommandResp:
		h.handleCommandResp(session, msg)
	case jt808.MsgIDQuestionResp:
		h.handleQuestionResp(session, msg)
	case jt808.MsgIDParamResp:
		h.handleParamResp(session, msg)
	case jt808.MsgIDTerminalUpgradeResp:
		h.handleTerminalUpgradeResp(session, msg)
	case jt808.MsgIDTerminalPropResp:
		h.handleTerminalPropResp(session, msg)
	// AUTO-FIX-2026-06-28: ȫϢԭȱʧ default 
	case jt808.MsgIDOverspeedAlarm:
		h.handleOverspeedAlarm(session, msg)
	case jt808.MsgIDFatigueDriveAlarm:
		h.handleFatigueDriveAlarm(session, msg)
	case jt808.MsgIDFireAreaAlarm:
		h.handleFireAreaAlarm(session, msg)
	// AUTO-FIX-2026-06-28: ȫ洢ýϢ
	case jt808.MsgIDStorageMediaSearch:
		h.handleStorageMediaSearch(session, msg)
	case jt808.MsgIDStorageMediaUpload:
		h.handleStorageMediaUpload(session, msg)
	// AUTO-FIX-2026-07-04: 0x0805  JT/T 808-2019 ׼Ϊ"ͷӦ"նˡƽ̨
	// ԭ handleMultimediaSearchResp  0x0805 ýӦΪ PhotoCommandRespMessage
	case jt808.MsgIDPhotoCommandResp:
		h.handlePhotoCommandResp(session, msg)
	// AUTO-FIX-2026-06-28: 0x0A00 ѻع 808-2019 ׼壨ն RSA Կ
	// ԭ PassengerCount ·ƳPassengerCountMessage ṹ屣 1045 ģ/üݣ
	case jt808.MsgIDRSAPublicKey:
		h.handleRSAPublicKey(session, msg)
	case jt808.MsgIDBillOperate:
		h.handleBillOperate(session, msg)
	default:
		// 1078 Ϣ 808 ֡ʽϢID 0x9000-0x9FFF Χ
		// նͨ 808 ӷ 1078 Ϣʱ808 codec Ὣ body װΪ RawMessage
		// ˴ 1078 ϢID 1078 codec ½ bodyȻίи handle1078 
		if msg.Header.MsgID >= 0x9000 && msg.Header.MsgID <= 0x9FFF {
			if codec, ok := h.protocolHub.GetCodec(protocol.ProtocolJT1078); ok {
				if rawMsg, ok := msg.Body.(*jt808.RawMessage); ok {
					if body, err := codec.ParseBody(msg.Header.MsgID, rawMsg.Data); err == nil {
						msg1078 := &protocol.Message{
							Header: msg.Header,
							Body:   body,
							Raw:    msg.Raw,
						}
						h.handle1078(session, msg1078)
						return
					}
				} else {
					// body Ѿǽ 1078 ֱͣί
					h.handle1078(session, msg)
					return
				}
			}
		}
		h.logger.Debug("unhandled 808 message",
			zap.Uint16("msg_id", msg.Header.MsgID),
			zap.String("session", session.ID))
	}
}

func (h *MessageHandler) handleRegister(session *gateway.Session, msg *protocol.Message) {
	reg, ok := msg.Body.(*jt808.RegisterMessage)
	if !ok {
		h.logger.Error("invalid register message body")
		return
	}

	//  808 Ϣͷնֻŵ session޸ؼbugԭʵ session.GetPhone() ؿգ
	// 808 ֡ͷе Phone ֶΣBCD ֻţڴ˴ͬ session
	// ȷעᡢȨλϱָ·ʹȷ phone
	if msg.Header.Phone != "" {
		session.SetPhone(msg.Header.Phone)
	}

	onlineCount := h.sessions.OnlineCount()
	if !h.limiter.AllowRegister(onlineCount) {
		h.logger.Warn("device limit reached, rejecting register",
			zap.String("phone", session.GetPhone()),
			zap.Int("online", onlineCount))
		resp := &jt808.RegisterResponse{
			RespSeqNum: msg.Header.SeqNum,
			Result:     0x03,
			AuthCode:   "",
		}
		h.send808Response(session, jt808.MsgIDRegisterResp, resp, msg.Header.SeqNum)
		return
	}

	// AUTO-FIX-2026-06-30 [-6]: ȨУ飨  max_vehicles
	if h.licenseValidator != nil {
		if err := h.licenseValidator.ValidateVehicleCount(onlineCount + 1); err != nil {
			h.logger.Warn("license vehicle limit reached, rejecting register",
				zap.String("phone", session.GetPhone()),
				zap.Int("online", onlineCount),
				zap.Error(err))
			resp := &jt808.RegisterResponse{
				RespSeqNum: msg.Header.SeqNum,
				Result:     0x03, // ѱעᣨľ
				AuthCode:   "",
			}
			h.send808Response(session, jt808.MsgIDRegisterResp, resp, msg.Header.SeqNum)
			return
		}
	}

	vehicle := &storage.Vehicle{
		ID:           session.ID,
		Phone:        session.GetPhone(),
		Protocol:     "jt808",
		ProvinceID:   reg.ProvinceID,
		CityID:       reg.CityID,
		Manufacturer: reg.Manufacturer,
		TerminalType: reg.TerminalModel,
		TerminalID:   reg.TerminalID,
		PlateColor:   int(reg.PlateColor),
		Online:       false,
		RegisteredAt: time.Now(),
		LastActive:   time.Now(),
	}

	if err := h.store.SaveVehicle(context.Background(), vehicle); err != nil {
		h.logger.Error("save vehicle failed", zap.Error(err))
		resp := &jt808.RegisterResponse{
			RespSeqNum: msg.Header.SeqNum,
			Result:     0x02,
			AuthCode:   "",
		}
		h.send808Response(session, jt808.MsgIDRegisterResp, resp, msg.Header.SeqNum)
		return
	}

	h.sessions.Register(session, session.GetPhone())

	// AUTO-FIX-2026-06-30 [P0-1]: ǿȨ루ֻţ
	// 豸ָ = Manufacturer + TerminalID׷ݣICCID ѡ0x0100 ׼
	//  0x0704 Ӧ 0x0102 IMEI չɲ䣩
	deviceFP := reg.Manufacturer + "/" + reg.TerminalID
	var authCode string
	if h.authCodeMgr != nil {
		authCode = h.authCodeMgr.Generate(session.GetPhone(), deviceFP, session.RemoteAddr, session.ID)
	} else {
		// δע AuthCodeManager Ļ·ȫݣ
		h.logger.Warn("AuthCodeManager δע룬˵ֻżȨ루ȫ")
		authCode = session.GetPhone()
	}
	session.Metadata["auth_code"] = authCode
	session.Metadata["device_fp"] = deviceFP

	resp := &jt808.RegisterResponse{
		RespSeqNum: msg.Header.SeqNum,
		Result:     0x00,
		AuthCode:   authCode,
	}
	h.send808Response(session, jt808.MsgIDRegisterResp, resp, msg.Header.SeqNum)

	h.logger.Info("terminal registered",
		zap.String("session_id", session.ID),
		zap.String("phone", session.GetPhone()),
		zap.String("manufacturer", reg.Manufacturer),
		zap.String("model", reg.TerminalModel),
		zap.String("device_fp", deviceFP))
}

func (h *MessageHandler) handleAuth(session *gateway.Session, msg *protocol.Message) {
	authMsg, ok := msg.Body.(*jt808.AuthMessage)
	if !ok {
		h.logger.Error("invalid auth message body")
		return
	}

	// AUTO-FIX-2026-06-30 [P0-1]: ϸУȨ루 expectedAuthCode=="" ʱ
	// ԭʵ session.Metadata["auth_code"] У飬 session ؽ expectedAuthCode
	// ΪգУ鱻 `!= ""` ƹκն˾ͨȨָΪͨ AuthCodeManager ϸУ飺
	//   1. ȨֻŰ󶨣α죩
	//   2. ͬ IP ʹ  澯쳣ʹü¼
	//   3. Ự SessionManager 豸Ựƾܾ¡
	phone := session.GetPhone()
	if h.authCodeMgr != nil {
		valid, reason := h.authCodeMgr.Validate(phone, authMsg.AuthCode, session.RemoteAddr)
		if !valid {
			h.logger.Warn("Ȩʧܣܾ֤",
				zap.String("session_id", session.ID),
				zap.String("phone", phone),
				zap.String("reason", reason),
				zap.String("remote_addr", session.RemoteAddr))
			resp := &jt808.GeneralResponse{
				RespSeqNum: msg.Header.SeqNum,
				RespMsgID:  jt808.MsgIDAuth,
				Result:     0x01, // Ȩʧ
			}
			h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
			return
		}
	} else {
		// δע AuthCodeManager Ļ·ɵ session.Metadata У飨
		expectedAuthCode, _ := session.Metadata["auth_code"].(string)
		if expectedAuthCode != "" && authMsg.AuthCode != expectedAuthCode {
			h.logger.Warn("auth code mismatch, rejecting authentication",
				zap.String("session_id", session.ID),
				zap.String("phone", phone))
			resp := &jt808.GeneralResponse{
				RespSeqNum: msg.Header.SeqNum,
				RespMsgID:  jt808.MsgIDAuth,
				Result:     0x01,
			}
			h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
			return
		}
	}

	// ¼ IMEI Ϊ豸ָǿ0x0102 ѡչֶΣ
	if authMsg.IMEI != "" {
		session.Metadata["imei"] = authMsg.IMEI
	}

	h.sessions.Authenticate(phone)

	if err := h.store.UpdateVehicleOnline(context.Background(), session.ID, true); err != nil {
		h.logger.Error("update vehicle online failed", zap.Error(err))
	}

	// AUTO-FIX-2026-06-30 [P1-2]: Ȩ浽 Redis24h TTLڵѯ/ơ
	// ʧܲӰ̣־Ϊ AuthCodeManager ڴУȨԴ
	if h.cache != nil {
		if err := h.cache.CacheSet(context.Background(), "auth:result:"+phone, authMsg.AuthCode, 24*time.Hour); err != nil {
			h.logger.Debug("Ȩ Redis ʧ", zap.String("phone", phone), zap.Error(err))
		}
	}

	resp := &jt808.GeneralResponse{
		RespSeqNum: msg.Header.SeqNum,
		RespMsgID:  jt808.MsgIDAuth,
		Result:     0x00,
	}
	h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)

	h.logger.Info("terminal authenticated",
		zap.String("session_id", session.ID),
		zap.String("phone", phone))
}

func (h *MessageHandler) handleHeartbeat(session *gateway.Session, msg *protocol.Message) {
	session.UpdateActivity()

	if err := h.store.UpdateVehicleOnline(context.Background(), session.ID, true); err != nil {
		h.logger.Debug("update vehicle activity failed", zap.Error(err))
	}

	resp := &jt808.GeneralResponse{
		RespSeqNum: msg.Header.SeqNum,
		RespMsgID:  jt808.MsgIDHeartbeat,
		Result:     0x00,
	}
	h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
}

func (h *MessageHandler) handleLocation(session *gateway.Session, msg *protocol.Message) {
	loc, ok := msg.Body.(*jt808.LocationMessage)
	if !ok {
		h.logger.Error("invalid location message body")
		return
	}

	// AUTO-FIX-2026-06-29 [P1]: ԭʵֶʧն˲ɼʱ loc.Timeʱ䣬
	// ²/ӳϱʱݿʱҡ޸ parseBCDTime ն BCD ʱ䡣
	terminalTime := parseBCDTime(loc.Time)

	locationData := &storage.LocationData{
		VehicleID:  session.ID,
		Phone:      session.GetPhone(),
		Latitude:   loc.Latitude,
		Longitude:  loc.Longitude,
		Altitude:   float64(loc.Altitude),
		Speed:      float64(loc.Speed) / 10.0,
		Direction:  int(loc.Direction),
		AlarmFlag:  loc.AlarmFlag,
		StatusFlag: loc.StatusFlag,
		Mileage:    float64(loc.Mileage) / 10.0,
		Fuel:       float64(loc.Fuel) / 10.0,
		Time:       terminalTime,
		ReceivedAt: time.Now(),
		Source:     "jt808",
	}

	if err := h.merge.Merge(context.Background(), locationData); err != nil {
		h.logger.Error("merge location failed", zap.Error(err))
		// ʹʧӦ𣬷ն˻ش
	} else {
		// AUTO-FIX-2026-06-30 [-7]: 洢дָ
		jteMetrics.StorageWriteTotal.IncWithLabels(map[string]string{
			"type": "location",
		})
		h.logger.Debug("location received",
			zap.String("phone", session.GetPhone()),
			zap.Float64("lat", loc.Latitude),
			zap.Float64("lon", loc.Longitude),
			zap.Float64("speed", float64(loc.Speed)/10.0))
	}

	// AUTO-FIX-2026-06-29 [P1]: 0x0200 ƵϢԭʵ 0x8001 ƽ̨ͨӦ
	// ն 3 δյӦط˷д޸ 0x8001 ӦResult=0 ɹ
	resp := &jt808.GeneralResponse{
		RespSeqNum: msg.Header.SeqNum,
		RespMsgID:  jt808.MsgIDLocation,
		Result:     0x00,
	}
	h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
}

func (h *MessageHandler) handleAlarm(session *gateway.Session, msg *protocol.Message) {
	alarmMsg, ok := msg.Body.(*jt808.AlarmMessage)
	if !ok {
		h.logger.Error("invalid alarm message body")
		return
	}

	alarm := &storage.AlarmData{
		ID:         fmt.Sprintf("alarm_%d", time.Now().UnixNano()),
		VehicleID:  session.ID,
		Phone:      session.GetPhone(),
		Type:       "jt808_alarm",
		AlarmFlag:  alarmMsg.AlarmFlag,
		ReceivedAt: time.Now(),
		Source:     "jt808",
	}

	if len(alarmMsg.AlarmItems) > 0 {
		var alarmTypes []uint32
		for _, item := range alarmMsg.AlarmItems {
			alarmTypes = append(alarmTypes, item.AlarmType)
		}
		if len(alarmTypes) > 0 {
			alarm.Additional = []byte(fmt.Sprintf("alarm_types:%v", alarmTypes))
		}
	}

	if err := h.merge.MergeAlarm(context.Background(), alarm); err != nil {
		h.logger.Error("merge alarm failed", zap.Error(err))
		// ʹʧӦ𣬷ն˻ش
	} else {
		h.logger.Info("alarm received",
			zap.String("phone", session.GetPhone()),
			zap.String("alarm_id", alarm.ID),
			zap.Uint32("alarm_flag", alarm.AlarmFlag))
	}

	// AUTO-FIX-2026-06-29 [P1]: 0x0900 ϱԭʵ 0x8001 ƽ̨ͨӦ
	// ն 3 δյӦط˷дظ¼
	// ޸ 0x8001 ӦResult=0 ɹ handleLocation Ӧģʽһ¡
	resp := &jt808.GeneralResponse{
		RespSeqNum: msg.Header.SeqNum,
		RespMsgID:  jt808.MsgIDAlarm,
		Result:     0x00,
	}
	h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
}

// AUTO-FIX-2026-06-28: ȫ 0x0400/0x0401/0x0500 Ϣ
// ԭ handle808 switch ȱʧЩ caseնϱĳ/ƣ/򱨾ᱻ
// ޸ merge.MergeAlarm ·type ֱֶ

// handleOverspeedAlarm  0x0400 ٱնˡƽ̨
func (h *MessageHandler) handleOverspeedAlarm(session *gateway.Session, msg *protocol.Message) {
	alarmMsg, ok := msg.Body.(*jt808.OverspeedAlarmMessage)
	if !ok {
		h.logger.Error("invalid overspeed alarm message body")
		return
	}
	alarm := &storage.AlarmData{
		ID:         fmt.Sprintf("overspeed_%d", time.Now().UnixNano()),
		VehicleID:  session.ID,
		Phone:      session.GetPhone(),
		Type:       "jt808_overspeed",
		Level:      2,
		AlarmFlag:  alarmMsg.AlarmFlag,
		Latitude:   alarmMsg.Latitude,
		Longitude:  alarmMsg.Longitude,
		Altitude:   float64(alarmMsg.Altitude),
		Speed:      float64(alarmMsg.Speed),
		Direction:  int(alarmMsg.Direction),
		Time:       parseBCDTime(alarmMsg.Time),
		ReceivedAt: time.Now(),
		Source:     "jt808",
	}
	if len(alarmMsg.AlarmAttach) > 0 {
		alarm.Additional = alarmMsg.AlarmAttach
	}
	if err := h.merge.MergeAlarm(context.Background(), alarm); err != nil {
		h.logger.Error("merge overspeed alarm failed", zap.Error(err))
		// ʹʧӦ𣬷ն˻ش
	} else {
		h.logger.Info("overspeed alarm received",
			zap.String("phone", session.GetPhone()),
			zap.Float64("speed", float64(alarmMsg.Speed)),
			zap.Float64("lat", alarmMsg.Latitude),
			zap.Float64("lon", alarmMsg.Longitude))
	}

	// AUTO-FIX-2026-06-29 [P1]:  0x8001 ƽ̨ͨӦ𣬱նشٱ
	resp := &jt808.GeneralResponse{
		RespSeqNum: msg.Header.SeqNum,
		RespMsgID:  jt808.MsgIDOverspeedAlarm,
		Result:     0x00,
	}
	h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
}

// handleFatigueDriveAlarm  0x0401 ƣͼʻնˡƽ̨
func (h *MessageHandler) handleFatigueDriveAlarm(session *gateway.Session, msg *protocol.Message) {
	alarmMsg, ok := msg.Body.(*jt808.FatigueDriveAlarmMessage)
	if !ok {
		h.logger.Error("invalid fatigue drive alarm message body")
		return
	}
	alarm := &storage.AlarmData{
		ID:         fmt.Sprintf("fatigue_%d", time.Now().UnixNano()),
		VehicleID:  session.ID,
		Phone:      session.GetPhone(),
		Type:       "jt808_fatigue",
		Level:      2,
		AlarmFlag:  alarmMsg.AlarmFlag,
		Latitude:   alarmMsg.Latitude,
		Longitude:  alarmMsg.Longitude,
		Altitude:   float64(alarmMsg.Altitude),
		Speed:      float64(alarmMsg.Speed),
		Direction:  int(alarmMsg.Direction),
		Time:       parseBCDTime(alarmMsg.Time),
		ReceivedAt: time.Now(),
		Source:     "jt808",
	}
	if len(alarmMsg.AlarmAttach) > 0 {
		alarm.Additional = alarmMsg.AlarmAttach
	}
	if err := h.merge.MergeAlarm(context.Background(), alarm); err != nil {
		h.logger.Error("merge fatigue alarm failed", zap.Error(err))
		// ʹʧӦ𣬷ն˻ش
	} else {
		h.logger.Info("fatigue drive alarm received",
			zap.String("phone", session.GetPhone()),
			zap.Float64("speed", float64(alarmMsg.Speed)),
			zap.Float64("lat", alarmMsg.Latitude),
			zap.Float64("lon", alarmMsg.Longitude))
	}

	// AUTO-FIX-2026-06-29 [P1]:  0x8001 ƽ̨ͨӦ𣬱նشƣͼʻ
	resp := &jt808.GeneralResponse{
		RespSeqNum: msg.Header.SeqNum,
		RespMsgID:  jt808.MsgIDFatigueDriveAlarm,
		Result:     0x00,
	}
	h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
}

// handleFireAreaAlarm  0x0500 򱨾նˡƽ̨
func (h *MessageHandler) handleFireAreaAlarm(session *gateway.Session, msg *protocol.Message) {
	alarmMsg, ok := msg.Body.(*jt808.FireAreaAlarmMessage)
	if !ok {
		h.logger.Error("invalid fire area alarm message body")
		return
	}
	alarm := &storage.AlarmData{
		ID:         fmt.Sprintf("firearea_%d", time.Now().UnixNano()),
		VehicleID:  session.ID,
		Phone:      session.GetPhone(),
		Type:       "jt808_fire_area",
		Level:      3,
		Latitude:   float64(alarmMsg.Lat) / jt808.JT808CoordScaleFactor,
		Longitude:  float64(alarmMsg.Lng) / jt808.JT808CoordScaleFactor,
		ReceivedAt: time.Now(),
		Source:     "jt808",
		Additional: []byte(fmt.Sprintf("area_type:%d,area_id:%d,dir:%d", alarmMsg.AreaType, alarmMsg.AreaID, alarmMsg.Dir)),
	}
	if err := h.merge.MergeAlarm(context.Background(), alarm); err != nil {
		h.logger.Error("merge fire area alarm failed", zap.Error(err))
		// ʹʧӦ𣬷ն˻ش
	} else {
		h.logger.Info("fire area alarm received",
			zap.String("phone", session.GetPhone()),
			zap.Uint32("area_id", alarmMsg.AreaID),
			zap.Uint8("area_type", alarmMsg.AreaType))
	}

	// AUTO-FIX-2026-06-29 [P1]:  0x8001 ƽ̨ͨӦ𣬱նش򱨾
	resp := &jt808.GeneralResponse{
		RespSeqNum: msg.Header.SeqNum,
		RespMsgID:  jt808.MsgIDFireAreaAlarm,
		Result:     0x00,
	}
	h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
}

// handleStorageMediaSearch  0x0803 洢ýնˡƽ̨
func (h *MessageHandler) handleStorageMediaSearch(session *gateway.Session, msg *protocol.Message) {
	searchMsg, ok := msg.Body.(*jt808.StorageMediaSearchMessage)
	if !ok {
		h.logger.Error("invalid storage media search message body")
		return
	}
	h.logger.Info("storage media search received",
		zap.String("phone", session.GetPhone()),
		zap.Uint32("multimedia_id", searchMsg.MultimediaID),
		zap.Uint8("multimedia_type", searchMsg.MultimediaType),
		zap.Uint8("channel_id", searchMsg.ChannelID),
		zap.String("start_time", searchMsg.StartTime),
		zap.String("end_time", searchMsg.EndTime))
	// ¼ý
	media := &storage.MultimediaData{
		VehicleID:  session.ID,
		Phone:      session.GetPhone(),
		MediaType:  int(searchMsg.MultimediaType),
		ChannelID:  int(searchMsg.ChannelID),
		ReceivedAt: time.Now(),
		Source:     "jt808",
	}
	if err := h.store.SaveMultimedia(context.Background(), media); err != nil {
		h.logger.Error("save storage media search failed", zap.Error(err))
	}

	// AUTO-FIX-2026-06-29 [P1]:  0x8001 ƽ̨ͨӦ𣬱նش洢ý
	resp := &jt808.GeneralResponse{
		RespSeqNum: msg.Header.SeqNum,
		RespMsgID:  jt808.MsgIDStorageMediaSearch,
		Result:     0x00,
	}
	h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
}

// handleStorageMediaUpload  0x0804 洢ýϴնˡƽ̨
func (h *MessageHandler) handleStorageMediaUpload(session *gateway.Session, msg *protocol.Message) {
	uploadMsg, ok := msg.Body.(*jt808.StorageMediaUploadMessage)
	if !ok {
		h.logger.Error("invalid storage media upload message body")
		return
	}
	h.logger.Info("storage media upload received",
		zap.String("phone", session.GetPhone()),
		zap.Uint32("multimedia_id", uploadMsg.MultimediaID),
		zap.Uint8("multimedia_type", uploadMsg.MultimediaType),
		zap.Uint8("channel_id", uploadMsg.ChannelID),
		zap.String("start_time", uploadMsg.StartTime),
		zap.String("end_time", uploadMsg.EndTime),
		zap.Uint8("delete_flag", uploadMsg.DeleteFlag))
	// ¼ý
	media := &storage.MultimediaData{
		VehicleID:  session.ID,
		Phone:      session.GetPhone(),
		MediaType:  int(uploadMsg.MultimediaType),
		ChannelID:  int(uploadMsg.ChannelID),
		ReceivedAt: time.Now(),
		Source:     "jt808",
	}
	if err := h.store.SaveMultimedia(context.Background(), media); err != nil {
		h.logger.Error("save storage media upload failed", zap.Error(err))
	}

	// AUTO-FIX-2026-06-29 [P1]:  0x8001 ƽ̨ͨӦ𣬱նش洢ýϴ
	resp := &jt808.GeneralResponse{
		RespSeqNum: msg.Header.SeqNum,
		RespMsgID:  jt808.MsgIDStorageMediaUpload,
		Result:     0x00,
	}
	h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
}

// AUTO-FIX-2026-07-04: handlePhotoCommandResp  0x0805 ͷӦնˡƽ̨
// նյ 0x8801 ͷԴϢӦƽ̨
// ӦӦˮšɹ/ʧܣýIDشIDб
func (h *MessageHandler) handlePhotoCommandResp(session *gateway.Session, msg *protocol.Message) {
	respMsg, ok := msg.Body.(*jt808.PhotoCommandRespMessage)
	if !ok {
		h.logger.Error("invalid photo command resp message body")
		return
	}
	h.logger.Info("photo command response received",
		zap.String("phone", session.GetPhone()),
		zap.Uint16("resp_seq", respMsg.RespSeqNum),
		zap.Uint8("result", respMsg.Result),
		zap.Uint32("multimedia_id", respMsg.MultimediaID),
		zap.Int("retransmit_count", len(respMsg.RetransmitIDs)))

	// ɹʱ¼ýID 0x0802 ýϴ
	if respMsg.Result == 0 {
		media := &storage.MultimediaData{
			VehicleID:    session.ID,
			Phone:        session.GetPhone(),
			MultimediaID: respMsg.MultimediaID,
			ReceivedAt:   time.Now(),
			Source:       "jt808_photo",
		}
		if err := h.store.SaveMultimedia(context.Background(), media); err != nil {
			h.logger.Error("save photo command resp multimedia failed",
				zap.Uint32("multimedia_id", respMsg.MultimediaID),
				zap.Error(err))
		}
	}

	// شID¼־ƽ̨෢ش
	if len(respMsg.RetransmitIDs) > 0 {
		h.logger.Warn("photo command response has retransmit packets",
			zap.String("phone", session.GetPhone()),
			zap.Uint32("multimedia_id", respMsg.MultimediaID),
			zap.Int("retransmit_count", len(respMsg.RetransmitIDs)))
	}
}

// AUTO-FIX-2026-06-28: handlePassengerCount  0x0A00 ͳƣնˡƽ̨
// նϱ³¼־רô洢ӿڣɺչΪ¼⣩
// ע0x0A00 ѻع 808-2019 ׼壨ն RSA Կ˺ɷ֧ݣ
//     ·л handleRSAPublicKey
func (h *MessageHandler) handlePassengerCount(session *gateway.Session, msg *protocol.Message) {
	countMsg, ok := msg.Body.(*jt808.PassengerCountMessage)
	if !ok {
		h.logger.Error("invalid passenger count message body")
		return
	}
	totalUp, totalDown := 0, 0
	for _, item := range countMsg.CountData {
		totalUp += int(item.UpCount)
		totalDown += int(item.DownCount)
	}
	h.logger.Info("passenger count received",
		zap.String("phone", session.GetPhone()),
		zap.Uint8("count_type", countMsg.CountType),
		zap.Int("door_count", len(countMsg.CountData)),
		zap.Int("total_up", totalUp),
		zap.Int("total_down", totalDown))
}

// AUTO-FIX-2026-06-28: handleRSAPublicKey  0x0A00 ն RSA Կնˡƽ̨
// նϱ RSA ģ빫Կָƽ̨ݴԿЭ̣ں SRTP ܵȳ
// ǰ¼־ԿַҵյϢ󴥷 0x8A00 ·ɡ
func (h *MessageHandler) handleRSAPublicKey(session *gateway.Session, msg *protocol.Message) {
	rsaMsg, ok := msg.Body.(*jt808.RSAPublicKeyMessage)
	if !ok {
		h.logger.Error("invalid rsa public key message body")
		return
	}
	h.logger.Info("rsa public key received",
		zap.String("phone", session.GetPhone()),
		zap.Int("modulus_len", len(rsaMsg.Euler)),
		zap.Uint32("public_exponent", rsaMsg.PublicExponent))

	// AUTO-FIX-2026-06-29 [P1]:  0x8001 ƽ̨ͨӦȷյն RSA Կ
	resp := &jt808.GeneralResponse{
		RespSeqNum: msg.Header.SeqNum,
		RespMsgID:  jt808.MsgIDRSAPublicKey,
		Result:     0x00,
	}
	h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
}

// AUTO-FIX-2026-06-28: handleBillOperate  0x0B00 Ƽնˡƽ̨
// նϱƼ¼¼־רô洢ӿڣɺչΪ¼⣩
func (h *MessageHandler) handleBillOperate(session *gateway.Session, msg *protocol.Message) {
	billMsg, ok := msg.Body.(*jt808.BillOperateMessage)
	if !ok {
		h.logger.Error("invalid bill operate message body")
		return
	}
	h.logger.Info("bill operate received",
		zap.String("phone", session.GetPhone()),
		zap.Uint8("operate_type", billMsg.OperateType),
		zap.Int("data_len", len(billMsg.OperateData)))

	// AUTO-FIX-2026-06-29 [P1]:  0x8001 ƽ̨ͨӦȷյƼ¼
	resp := &jt808.GeneralResponse{
		RespSeqNum: msg.Header.SeqNum,
		RespMsgID:  jt808.MsgIDBillOperate,
		Result:     0x00,
	}
	h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
}

// beijingLocation JT/T 808 BCD ʱΪʱ䣨CST, UTC+8
// AUTO-FIX-2026-06-29 [P1]: ԭ parseBCDTime  time.LocalUTC ƫ 8 Сʱ
var beijingLocation = time.FixedZone("CST", 8*3600)

// parseBCDTime  BCD ʱַYYMMDDHHmmss  time.Time
// AUTO-FIX-2026-06-28:  808 е BCD ʱתΪ time.Time
func parseBCDTime(bcd string) time.Time {
	if len(bcd) < 12 {
		return time.Time{}
	}
	// BCD ʽ: YYMMDDHHmmss
	year := 2000 + atoi(bcd[0:2])
	month := atoi(bcd[2:4])
	day := atoi(bcd[4:6])
	hour := atoi(bcd[6:8])
	min := atoi(bcd[8:10])
	sec := atoi(bcd[10:12])
	// AUTO-FIX-2026-06-29 [P1]: ʽʹñʱ UTC ʱʱƫ 8 Сʱ
	return time.Date(year, time.Month(month), day, hour, min, sec, 0, beijingLocation)
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// handleAlarmAttachment  0x0901 ϴ
func (h *MessageHandler) handleAlarmAttachment(session *gateway.Session, msg *protocol.Message) {
	attMsg, ok := msg.Body.(*jt808.AlarmAttachmentMessage)
	if !ok {
		h.logger.Error("invalid alarm attachment message body")
		return
	}

	h.logger.Info("alarm attachment received",
		zap.String("phone", session.GetPhone()),
		zap.Uint32("alarm_id", attMsg.AlarmID),
		zap.Int("attachment_count", len(attMsg.Attachments)))

	// 洢Ԫݵ Additional ֶ
	for _, att := range attMsg.Attachments {
		typeName := "unknown"
		switch att.Type {
		case 0:
			typeName = "image"
		case 1:
			typeName = "audio"
		case 2:
			typeName = "video"
		case 3:
			typeName = "text"
		case 4:
			typeName = "other"
		}
		h.logger.Info("alarm attachment item",
			zap.String("phone", session.GetPhone()),
			zap.String("type", typeName),
			zap.Uint32("size", att.Size))

		// 渽Ԫݵý洢
		if h.store != nil && len(att.Data) > 0 {
			media := &storage.MultimediaData{
				Phone:      session.GetPhone(),
				MediaType:  int(att.Type),
				ReceivedAt: time.Now(),
				Source:     "alarm_attachment",
			}
			_ = h.store.SaveMultimedia(context.Background(), media)
		}
	}

	//  0x9001 ϴӦ
	resp := &jt808.AlarmAttachmentRespMessage{
		RespSeqNum: msg.Header.SeqNum,
		Result:     0x00,
	}
	h.send808Response(session, jt808.MsgIDAlarmAttachmentResp, resp, msg.Header.SeqNum)
}

func (h *MessageHandler) handleTerminalCancel(session *gateway.Session, msg *protocol.Message) {
	if err := h.store.UpdateVehicleOnline(context.Background(), session.ID, false); err != nil {
		h.logger.Error("update vehicle offline failed", zap.Error(err))
	}

	// AUTO-FIX-2026-06-30 [P0-1]: նעʱȨ룬ֹ뱻
	if h.authCodeMgr != nil {
		h.authCodeMgr.Revoke(session.GetPhone())
	}

	resp := &jt808.TerminalCancelResponse{
		Result: 0x00,
	}
	h.send808Response(session, jt808.MsgIDTerminalCancelResp, resp, msg.Header.SeqNum)

	h.sessions.Remove(session.ID)
	h.logger.Info("terminal cancelled",
		zap.String("session_id", session.ID),
		zap.String("phone", session.GetPhone()))
}

func (h *MessageHandler) handleLocationBatch(session *gateway.Session, msg *protocol.Message) {
	batch, ok := msg.Body.(*jt808.LocationBatchMessage)
	if !ok {
		h.logger.Error("invalid location batch message body")
		return
	}
	for i, loc := range batch.Locations {
		// AUTO-FIX-2026-06-29 [P1]: ն˲ɼʱ loc.Timeԭʵֽʱ䣩
		locationData := &storage.LocationData{
			VehicleID:  session.ID,
			Phone:      session.GetPhone(),
			Latitude:   loc.Latitude,
			Longitude:  loc.Longitude,
			Altitude:   float64(loc.Altitude),
			Speed:      float64(loc.Speed) / 10.0,
			Direction:  int(loc.Direction),
			AlarmFlag:  loc.AlarmFlag,
			StatusFlag: loc.StatusFlag,
			Time:       parseBCDTime(loc.Time),
			ReceivedAt: time.Now(),
			Source:     "jt808_batch",
		}
		if err := h.merge.Merge(context.Background(), locationData); err != nil {
			h.logger.Error("merge batch location failed",
				zap.Error(err),
				zap.Int("index", i))
			continue
		}
	}
	h.logger.Debug("batch location received",
		zap.String("phone", session.GetPhone()),
		zap.Int("count", len(batch.Locations)))

	// AUTO-FIX-2026-06-29 [P1]: 0x0704 λϱͬҪ 0x8001 ƽ̨ͨӦ
	resp := &jt808.GeneralResponse{
		RespSeqNum: msg.Header.SeqNum,
		RespMsgID:  jt808.MsgIDLocationBatch,
		Result:     0x00,
	}
	h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
}

func (h *MessageHandler) handleLocationQueryResp(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt808.LocationQueryResponse)
	if !ok {
		h.logger.Error("invalid location query response body")
		return
	}
	locationData := &storage.LocationData{
		VehicleID:  session.ID,
		Phone:      session.GetPhone(),
		Latitude:   resp.Location.Latitude,
		Longitude:  resp.Location.Longitude,
		Altitude:   float64(resp.Location.Altitude),
		Speed:      float64(resp.Location.Speed) / 10.0,
		Direction:  int(resp.Location.Direction),
		AlarmFlag:  resp.Location.AlarmFlag,
		StatusFlag: resp.Location.StatusFlag,
		ReceivedAt: time.Now(),
		Source:     "jt808_query",
	}
	if err := h.merge.Merge(context.Background(), locationData); err != nil {
		h.logger.Error("merge query location failed", zap.Error(err))
	}
}

func (h *MessageHandler) handleMultimedia(session *gateway.Session, msg *protocol.Message) {
	mm, ok := msg.Body.(*jt808.MultimediaMessage)
	if !ok {
		h.logger.Error("invalid multimedia message body")
		return
	}
	h.logger.Info("multimedia event received",
		zap.String("phone", session.GetPhone()),
		zap.Uint32("multimedia_id", mm.MultimediaID),
		zap.Uint8("type", mm.MultimediaType),
		zap.Uint8("channel", mm.ChannelID))

	// AUTO-FIX-2026-06-29 [P1]:  0x8001 ƽ̨ͨӦȷյý¼֪ͨ
	resp := &jt808.GeneralResponse{
		RespSeqNum: msg.Header.SeqNum,
		RespMsgID:  jt808.MsgIDMultimedia,
		Result:     0x00,
	}
	h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
}

func (h *MessageHandler) handleMultimediaUpload(session *gateway.Session, msg *protocol.Message) {
	mm, ok := msg.Body.(*jt808.MultimediaUploadMessage)
	if !ok {
		h.logger.Error("invalid multimedia upload message body")
		return
	}
	h.logger.Info("multimedia data uploaded",
		zap.String("phone", session.GetPhone()),
		zap.Uint32("multimedia_id", mm.MultimediaID),
		zap.Uint16("packet", mm.PacketIndex),
		zap.Uint16("total", mm.PacketTotal),
		zap.Int("data_len", len(mm.MediaData)))
}

func (h *MessageHandler) handleDriverID(session *gateway.Session, msg *protocol.Message) {
	driver, ok := msg.Body.(*jt808.DriverIDMessage)
	if !ok {
		h.logger.Error("invalid driver ID message body")
		return
	}
	h.logger.Info("driver identity received",
		zap.String("phone", session.GetPhone()),
		zap.String("driver_id", driver.DriverID),
		zap.Uint8("status", driver.Status))

	driverInfo := &storage.DriverInfoData{
		ID:            fmt.Sprintf("driver_%d", time.Now().UnixNano()),
		VehicleID:     session.ID,
		Phone:         session.GetPhone(),
		IDCard:        driver.DriverID,
		Source:        "jt808",
		ReceivedAt:    time.Now(),
	}
	if err := h.store.SaveDriverInfo(context.Background(), driverInfo); err != nil {
		h.logger.Error("save driver info failed", zap.Error(err))
	}

	// AUTO-FIX-2026-06-29 [P1]:  0x8001 ƽ̨ͨӦȷյʻԱϢ
	resp := &jt808.GeneralResponse{
		RespSeqNum: msg.Header.SeqNum,
		RespMsgID:  jt808.MsgIDDriverID,
		Result:     0x00,
	}
	h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
}

func (h *MessageHandler) handleCanData(session *gateway.Session, msg *protocol.Message) {
	can, ok := msg.Body.(*jt808.CanDataMessage)
	if !ok {
		h.logger.Error("invalid CAN data message body")
		return
	}
	h.logger.Debug("CAN data received",
		zap.String("phone", session.GetPhone()),
		zap.Uint8("can_count", can.CanCount))

	canData := &storage.CanBusData{
		ID:         fmt.Sprintf("can_%d", time.Now().UnixNano()),
		VehicleID:  session.ID,
		Phone:      session.GetPhone(),
		Source:     "jt808",
		ReceivedAt: time.Now(),
	}
	for _, item := range can.CanItems {
		canData.Items = append(canData.Items, storage.CanBusItem{
			CanID: item.CANID,
			Value: item.Data,
		})
	}
	if err := h.store.SaveCanData(context.Background(), canData); err != nil {
		h.logger.Error("save CAN data failed", zap.Error(err))
	}

	// AUTO-FIX-2026-06-29 [P1]:  0x8001 ƽ̨ͨӦȷյ CAN ݡ
	resp := &jt808.GeneralResponse{
		RespSeqNum: msg.Header.SeqNum,
		RespMsgID:  jt808.MsgIDCanData,
		Result:     0x00,
	}
	h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
}

func (h *MessageHandler) handleElectronicWaybill(session *gateway.Session, msg *protocol.Message) {
	waybill, ok := msg.Body.(*jt808.ElectronicWaybillMessage)
	if !ok {
		h.logger.Error("invalid electronic waybill message body")
		return
	}
	h.logger.Info("electronic waybill received",
		zap.String("phone", session.GetPhone()),
		zap.Int("data_len", len(waybill.WaybillData)))
	if err := h.store.SaveElectronicWaybill(context.Background(), &storage.ElectronicWaybillData{
		ID:         fmt.Sprintf("wb_%s_%d", session.GetPhone(), time.Now().UnixMilli()),
		Phone:      session.GetPhone(),
		WaybillNo:  "",
		Content:    string(waybill.WaybillData),
		ReceivedAt: time.Now(),
	}); err != nil {
		h.logger.Error("failed to save electronic waybill", zap.Error(err))
	}

	// AUTO-FIX-2026-06-29 [P1]:  0x8001 ƽ̨ͨӦȷյ·
	resp := &jt808.GeneralResponse{
		RespSeqNum: msg.Header.SeqNum,
		RespMsgID:  jt808.MsgIDElectronicWaybill,
		Result:     0x00,
	}
	h.send808Response(session, jt808.MsgIDGeneralResp, resp, msg.Header.SeqNum)
}

func (h *MessageHandler) handleInfoMenuResp(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt808.InfoMenuRespMessage)
	if !ok {
		h.logger.Error("invalid info menu response body")
		return
	}
	h.logger.Info("info menu response received",
		zap.String("phone", session.GetPhone()),
		zap.Uint8("info_type", resp.InfoType),
		zap.Uint32("info_id", resp.InfoID))
	if err := h.store.SaveInfoMenuResp(context.Background(), &storage.InfoMenuRespData{
		ID:         fmt.Sprintf("infomenu_%s_%d", session.GetPhone(), time.Now().UnixMilli()),
		Phone:      session.GetPhone(),
		InfoType:   int(resp.InfoType),
		InfoID:     resp.InfoID,
		InfoData:   string(resp.InfoData),
		ReceivedAt: time.Now(),
	}); err != nil {
		h.logger.Error("failed to save info menu response", zap.Error(err))
	}
}


func (h *MessageHandler) handleSMSForwardResp(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt808.SMSForwardRespMessage)
	if !ok {
		h.logger.Error("invalid SMS forward response body")
		return
	}
	h.logger.Info("SMS forward response received",
		zap.String("phone", session.GetPhone()),
		zap.Int("sms_len", len(resp.SMSContent)))
	if err := h.store.SaveSMSForwardResp(context.Background(), &storage.SMSForwardRespData{
		ID:         fmt.Sprintf("smsfwd_%s_%d", session.GetPhone(), time.Now().UnixMilli()),
		Phone:      session.GetPhone(),
		SMSContent: string(resp.SMSContent),
		ReceivedAt: time.Now(),
	}); err != nil {
		h.logger.Error("failed to save SMS forward response", zap.Error(err))
	}
}

func (h *MessageHandler) handleEventResp(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt808.EventRespMessage)
	if !ok {
		h.logger.Error("invalid event response body")
		return
	}
	// AUTO-FIX-2026-06-27: EventRespMessage.EventID  uint32 Ϊ uint160x0301׼
	h.logger.Info("event response received",
		zap.String("phone", session.GetPhone()),
		zap.Uint16("event_id", resp.EventID))
	if err := h.store.SaveEventResp(context.Background(), &storage.EventRespData{
		ID:         fmt.Sprintf("event_%s_%d", session.GetPhone(), time.Now().UnixMilli()),
		Phone:      session.GetPhone(),
		EventID:    uint32(resp.EventID),
		ReceivedAt: time.Now(),
	}); err != nil {
		h.logger.Error("failed to save event response", zap.Error(err))
	}
}

func (h *MessageHandler) handleQuestionResp(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt808.QuestionRespMessage)
	if !ok {
		h.logger.Error("invalid question response body")
		return
	}
	h.logger.Info("question response received",
		zap.String("phone", session.GetPhone()),
		zap.Uint16("answer_id", resp.AnswerID))
}

// handleTerminalGeneralResp  0x0001 նͨӦ
// Ӧ͸ CommandSender SendAndWait  pending Уָͬʱ
func (h *MessageHandler) handleTerminalGeneralResp(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt808.TerminalGeneralRespMessage)
	if !ok {
		h.logger.Error("invalid terminal general response body")
		return
	}
	h.logger.Debug("terminal general response received",
		zap.String("phone", session.GetPhone()),
		zap.Uint16("resp_seq", resp.RespSeqNum),
		zap.Uint16("resp_msg_id", resp.RespMsgID),
		zap.Uint8("result", resp.RespResult))

	// ͨӦ CommandSender SendAndWait  pending 
	if h.onCommandResp != nil {
		h.onCommandResp(resp)
	}
}

// handleTempLocationTrackResp  0x0202 ʱλøӦ
func (h *MessageHandler) handleTempLocationTrackResp(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt808.TempLocationTrackRespMessage)
	if !ok {
		h.logger.Error("invalid temp location track response body")
		return
	}
	h.logger.Info("temp location track response received",
		zap.String("phone", session.GetPhone()),
		zap.Uint8("result", resp.Result))

	// ͬ SendAndWait  pending 
	if h.onCommandResp != nil {
		genResp := &jt808.TerminalGeneralRespMessage{
			RespSeqNum:   msg.Header.SeqNum,
			RespMsgID:    jt808.MsgIDTempLocationTrack,
			RespResult:   resp.Result,
		}
		h.onCommandResp(genResp)
	}
}

// handleParamResp  0x0104 ѯӦ
func (h *MessageHandler) handleParamResp(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt808.ParamRespMessage)
	if !ok {
		h.logger.Error("invalid param response body")
		return
	}
	h.logger.Info("param response received",
		zap.String("phone", session.GetPhone()),
		zap.Uint16("seq", resp.SeqNum),
		zap.Int("param_count", len(resp.Params)))

	if err := h.store.SaveCommandResp(context.Background(), &storage.CommandRespData{
		ID:         fmt.Sprintf("paramresp_%s_%d", session.GetPhone(), time.Now().UnixMilli()),
		Phone:      session.GetPhone(),
		CommandID:  fmt.Sprintf("%d", resp.SeqNum),
		Result:     0,
		ReceivedAt: time.Now(),
	}); err != nil {
		h.logger.Error("failed to save param response", zap.Error(err))
	}
}

// handleTerminalUpgradeResp  0x0108 նӦ
func (h *MessageHandler) handleTerminalUpgradeResp(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt808.TerminalUpgradeRespMessage)
	if !ok {
		h.logger.Error("invalid terminal upgrade response body")
		return
	}
	h.logger.Info("terminal upgrade response received",
		zap.String("phone", session.GetPhone()),
		zap.Uint8("upgrade_type", resp.UpgradeType),
		zap.String("manufacturer", resp.Manufacturer),
		zap.String("version", resp.Version))

	if err := h.store.SaveTerminalProp(context.Background(), &storage.TerminalPropData{
		ID:              fmt.Sprintf("upgrade_%s_%d", session.GetPhone(), time.Now().UnixMilli()),
		Phone:           session.GetPhone(),
		ManufacturerID:  resp.Manufacturer,
		FirmwareVersion: resp.Version,
		ReceivedAt:      time.Now(),
	}); err != nil {
		h.logger.Error("failed to save terminal upgrade response", zap.Error(err))
	}
}

func (h *MessageHandler) handleCommandResp(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt808.CommandRespMessage)
	if !ok {
		h.logger.Error("invalid command response body")
		return
	}
	h.logger.Info("command response received",
		zap.String("phone", session.GetPhone()),
		zap.Uint16("resp_seq", resp.RespSeqNum),
		zap.Uint16("resp_msg_id", resp.RespMsgID))

	// ͨӦ CommandSender SendAndWait  pending 
	if h.onCommandResp != nil {
		genResp := &jt808.TerminalGeneralRespMessage{
			RespSeqNum:   resp.RespSeqNum,
			RespMsgID:    resp.RespMsgID,
			RespResult:   0,
		}
		h.onCommandResp(genResp)
	}

	if err := h.store.SaveCommandResp(context.Background(), &storage.CommandRespData{
		ID:         fmt.Sprintf("cmdresp_%s_%d", session.GetPhone(), time.Now().UnixMilli()),
		Phone:      session.GetPhone(),
		CommandID:  fmt.Sprintf("%d", resp.RespSeqNum),
		Result:     int(resp.RespMsgID),
		ReceivedAt: time.Now(),
	}); err != nil {
		h.logger.Error("failed to save command response", zap.Error(err))
	}
}

func (h *MessageHandler) handleTerminalPropResp(session *gateway.Session, msg *protocol.Message) {
	prop, ok := msg.Body.(*jt808.TerminalPropRespMessage)
	if !ok {
		h.logger.Error("invalid terminal property response body")
		return
	}
	h.logger.Info("terminal property received",
		zap.String("phone", session.GetPhone()),
		zap.String("producer", prop.Manufacturer),
		zap.String("model", prop.Model),
		zap.String("hw_version", prop.HardwareVer),
		zap.String("fw_version", prop.FirmwareVer))
	if err := h.store.SaveTerminalProp(context.Background(), &storage.TerminalPropData{
		ID:              fmt.Sprintf("tprop_%s_%d", session.GetPhone(), time.Now().UnixMilli()),
		Phone:           session.GetPhone(),
		ManufacturerID:  prop.Manufacturer,
		Model:           prop.Model,
		HardwareVersion: prop.HardwareVer,
		FirmwareVersion: prop.FirmwareVer,
		ReceivedAt:      time.Now(),
	}); err != nil {
		h.logger.Error("failed to save terminal property", zap.Error(err))
	}
}

func (h *MessageHandler) handle809(session *gateway.Session, msg *protocol.Message) {
	handler, ok := h.handlerRegistry.Get(protocol.ProtocolJT809)
	if !ok {
		h.logger.Warn("809 protocol handler not registered, install module-protocol-809",
			zap.Uint16("msg_id", msg.Header.MsgID),
			zap.String("session", session.ID))
		return
	}
	if err := handler.HandleMessage(gatewaySessionAdapter{session}, msg, h.protocolHub); err != nil {
		h.logger.Error("809 handler error", zap.Error(err), zap.String("session", session.ID))
	}
}

func (h *MessageHandler) handle1078(session *gateway.Session, msg *protocol.Message) {
	switch msg.Header.MsgID {
	case jt1078.MsgIDRealtimeRequest:
		h.handle1078RealtimeRequest(session, msg)
	// AUTO-FIX-2026-06-27: 0x9105 ΪƵԭ MsgIDControlRequest
	case jt1078.MsgIDAVStatusNotification:
		h.handle1078AVStatusNotification(session, msg)
	case jt1078.MsgIDPlaybackRequest:
		h.handle1078PlaybackRequest(session, msg)
	case jt1078.MsgIDRTPData:
		h.handle1078RTPData(session, msg)
	case jt1078.MsgIDAlarmVideoRequest:
		h.handle1078AlarmVideo(session, msg)
	case jt1078.MsgIDPTZControl:
		h.handle1078PTZControl(session, msg)
	case jt1078.MsgIDAVParamSet:
		h.handle1078ParamSet(session, msg)
	case jt1078.MsgIDAVParamQuery:
		h.handle1078ParamQuery(session, msg)
	case jt1078.MsgIDAVParamResponse:
		h.handle1078ParamResponse(session, msg)
	case jt1078.MsgIDPlaybackControl:
		h.handle1078PlaybackControl(session, msg)
	case jt1078.MsgIDDownloadResponse:
		h.handle1078DownloadResponse(session, msg)
	// AUTO-FIX-2026-06-28: ȫնˡƽ̨ӦϢԭȱʧ default 
	case jt1078.MsgIDTermAVRequest:
		h.handle1078TermAVRequest(session, msg)
	case jt1078.MsgIDAVStatusNotificationResponse:
		h.handle1078AVStatusNotificationResponse(session, msg)
	case jt1078.MsgIDPlaybackResponse:
		h.handle1078PlaybackResponse(session, msg)
	case jt1078.MsgIDPlaybackControlAck:
		h.handle1078PlaybackControlAck(session, msg)
	case jt1078.MsgIDPTZControlAck:
		h.handle1078PTZControlAck(session, msg)
	case jt1078.MsgIDAlarmVideoResponse:
		h.handle1078AlarmVideoResponse(session, msg)
	case jt1078.MsgIDFileUploadRequest:
		h.handle1078FileUploadRequest(session, msg)
	case jt1078.MsgIDTerminalLogResp:
		h.handle1078TerminalLogResp(session, msg)
	// AUTO-FIX-2026-06-28: ȫƽ̨Ϣ0x1A00/0x1A01/0x1B00/0x1B01
	// ЩϢԭ codec.go ע ParseBody  handler ȱʧ case default 
	case jt1078.MsgIDAVNegotiate:
		h.handle1078AVNegotiate(session, msg)
	case jt1078.MsgIDAVNegotiateResp:
		h.handle1078AVNegotiateResp(session, msg)
	case jt1078.MsgIDAVForward:
		h.handle1078AVForward(session, msg)
	case jt1078.MsgIDAVForwardResp:
		h.handle1078AVForwardResp(session, msg)
	default:
		h.logger.Debug("unhandled 1078 message",
			zap.Uint16("msg_id", msg.Header.MsgID),
			zap.String("session", session.ID))
	}
}

func (h *MessageHandler) handle1078RealtimeRequest(session *gateway.Session, msg *protocol.Message) {
	req, ok := msg.Body.(*jt1078.RealtimeRequestMessage)
	if !ok {
		h.logger.Error("invalid 1078 realtime request body")
		return
	}
	h.logger.Info("1078 realtime video request",
		zap.String("phone", session.GetPhone()),
		zap.Uint8("channel", req.LogicChannel),
		zap.Uint8("media_type", req.MediaType),
		zap.Uint8("stream_type", req.StreamType))

	resp := &jt1078.RealtimeResponseMessage{
		SeqNum:       msg.Header.SeqNum,
		LogicChannel: req.LogicChannel,
		Result:       0x00,
	}
	h.send808Response(session, jt1078.MsgIDRealtimeResponse, resp, msg.Header.SeqNum)
}

// FIXED-2026-07-17 [P0]: 0x9105 实时音视频传输状态通知（终端→平台）
// 原误实现为单条音视频检索请求，修正为状态通知处理：记录终端上报的丢包/乱序/码率等质量指标
func (h *MessageHandler) handle1078AVStatusNotification(session *gateway.Session, msg *protocol.Message) {
	req, ok := msg.Body.(*jt1078.AVStatusNotificationMessage)
	if !ok {
		h.logger.Error("invalid 1078 av status notification body")
		return
	}
	h.logger.Info("1078 av status notification",
		zap.String("phone", session.GetPhone()),
		zap.Uint8("channel", req.LogicChannel),
		zap.Uint16("seq", req.SeqNum),
		zap.Uint16("lost_packets", req.LostPackets),
		zap.Uint16("disorder_packets", req.DisorderPackets),
		zap.Uint16("loss_rate", req.LossRate),
		zap.Uint32("bitrate_bps", req.CurrentBitrate),
		zap.Uint16("terminal_status", req.TerminalStatus))

	// 回复 0x9106 状态通知应答
	resp := &jt1078.AVStatusNotificationResponseMessage{
		SeqNum:       req.SeqNum,
		LogicChannel: req.LogicChannel,
	}
	h.send808Response(session, jt1078.MsgIDAVStatusNotificationResponse, resp, msg.Header.SeqNum)
}

func (h *MessageHandler) handle1078PlaybackRequest(session *gateway.Session, msg *protocol.Message) {
	req, ok := msg.Body.(*jt1078.PlaybackRequestMessage)
	if !ok {
		h.logger.Error("invalid 1078 playback request body")
		return
	}
	h.logger.Info("1078 playback request",
		zap.String("phone", session.GetPhone()),
		zap.Uint8("channel", req.LogicChannel),
		zap.String("start", req.StartTime),
		zap.String("end", req.EndTime))

	// ͻطӦ RealtimeResponseMessage ṹSeqNum+Channel+Result
	resp := &jt1078.RealtimeResponseMessage{
		SeqNum:       msg.Header.SeqNum,
		LogicChannel: req.LogicChannel,
		Result:       0x00,
	}
	h.send808Response(session, jt1078.MsgIDPlaybackResponse, resp, msg.Header.SeqNum)
}

func (h *MessageHandler) handle1078RTPData(session *gateway.Session, msg *protocol.Message) {
	rtpMsg, ok := msg.Body.(*jt1078.RTPDataMessage)
	if !ok {
		h.logger.Debug("1078 RTP data: body not parsed, skipping",
			zap.String("phone", session.GetPhone()),
			zap.Int("raw_len", len(msg.Raw)))
		return
	}

	if h.videoEngine == nil {
		h.logger.Debug("1078 RTP data: videoEngine not available",
			zap.String("phone", session.GetPhone()))
		return
	}

	// Reconstruct the raw RTP packet (header + payload) and forward it to
	// ZLMediaKit via UDP so the stream can be played back in the browser.
	rtpData := make([]byte, 0, len(rtpMsg.RTPHeader)+len(rtpMsg.RTPPayload))
	rtpData = append(rtpData, rtpMsg.RTPHeader...)
	rtpData = append(rtpData, rtpMsg.RTPPayload...)

	streamID := fmt.Sprintf("%s_ch%d", session.GetPhone(), rtpMsg.LogicChannel)
	if err := h.videoEngine.ForwardRTP(streamID, rtpData); err != nil {
		h.logger.Debug("forward 1078 RTP to ZLMediaKit failed",
			zap.String("stream", streamID),
			zap.Int("len", len(rtpData)),
			zap.Error(err))
	}
}

func (h *MessageHandler) handle1078AlarmVideo(session *gateway.Session, msg *protocol.Message) {
	alarm := &storage.AlarmData{
		ID:         fmt.Sprintf("1078_alarm_%d", time.Now().UnixNano()),
		VehicleID:  session.ID,
		Phone:      session.GetPhone(),
		Type:       "jt1078_alarm_video",
		ReceivedAt: time.Now(),
		Source:     "jt1078",
	}
	if err := h.merge.MergeAlarm(context.Background(), alarm); err != nil {
		h.logger.Error("merge 1078 alarm video failed", zap.Error(err))
	}
	h.logger.Info("1078 alarm video request", zap.String("phone", session.GetPhone()))
}

// AUTO-FIX-2026-06-27: 0x9301 Ϊ 5B ʽChannel + 4B ControlInstruction
func (h *MessageHandler) handle1078PTZControl(session *gateway.Session, msg *protocol.Message) {
	ptz, ok := msg.Body.(*jt1078.PTZControlMessage)
	if !ok {
		h.logger.Error("invalid 1078 PTZ control body")
		return
	}
	h.logger.Info("1078 PTZ control",
		zap.String("phone", session.GetPhone()),
		zap.Uint8("channel", ptz.LogicChannel),
		zap.String("control_instruction", fmt.Sprintf("%02X %02X %02X %02X",
			ptz.ControlInstruction[0], ptz.ControlInstruction[1],
			ptz.ControlInstruction[2], ptz.ControlInstruction[3])))
}

// AUTO-FIX-2026-06-27: 0x9501 Ϊ䳤бṹԭ̶ֶ AudioType/VideoType/Resolution/FrameRate/BitRate
func (h *MessageHandler) handle1078ParamSet(session *gateway.Session, msg *protocol.Message) {
	req, ok := msg.Body.(*jt1078.AVParamSetMessage)
	if !ok {
		h.logger.Error("invalid 1078 AV param set body")
		return
	}
	h.logger.Info("1078 AV param set",
		zap.String("phone", session.GetPhone()),
		zap.Uint8("channel", req.LogicChannel),
		zap.Int("audio_params", len(req.AudioParams)),
		zap.Int("video_params", len(req.VideoParams)))

	paramValue := fmt.Sprintf("audio=%d,video=%d", len(req.AudioParams), len(req.VideoParams))
	if err := h.store.SaveAVParam(context.Background(), &storage.AVParamData{
		ID:         fmt.Sprintf("avparam_set_%s_%d", session.GetPhone(), time.Now().UnixMilli()),
		Phone:      session.GetPhone(),
		ChannelID:  int(req.LogicChannel),
		ParamType:  1, // 1=
		ParamValue: paramValue,
		ReceivedAt: time.Now(),
	}); err != nil {
		h.logger.Error("failed to save 1078 AV param set", zap.Error(err))
	}
}

func (h *MessageHandler) handle1078ParamQuery(session *gateway.Session, msg *protocol.Message) {
	req, ok := msg.Body.(*jt1078.AVParamQueryMessage)
	if !ok {
		h.logger.Error("invalid 1078 AV param query body")
		return
	}
	h.logger.Info("1078 AV param query",
		zap.String("phone", session.GetPhone()),
		zap.Uint8("channel", req.LogicChannel))
}

func (h *MessageHandler) handle1078ParamResponse(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt1078.AVParamResponseMessage)
	if !ok {
		h.logger.Error("invalid 1078 AV param response body")
		return
	}
	h.logger.Info("1078 AV param response",
		zap.String("phone", session.GetPhone()),
		zap.Uint8("channel", resp.LogicChannel),
		zap.Uint8("audio_type", resp.AudioType),
		zap.Uint8("video_type", resp.VideoType),
		zap.Uint8("resolution", resp.Resolution),
		zap.Uint8("frame_rate", resp.FrameRate),
		zap.Uint16("bit_rate", resp.BitRate))

	if err := h.store.SaveAVParam(context.Background(), &storage.AVParamData{
		ID:         fmt.Sprintf("avparam_resp_%s_%d", session.GetPhone(), time.Now().UnixMilli()),
		Phone:      session.GetPhone(),
		ChannelID:  int(resp.LogicChannel),
		ParamType:  2, // 2=ѯӦ
		ParamValue: fmt.Sprintf("audio=%d,video=%d,res=%d,fps=%d,bitrate=%d", resp.AudioType, resp.VideoType, resp.Resolution, resp.FrameRate, resp.BitRate),
		ReceivedAt: time.Now(),
	}); err != nil {
		h.logger.Error("failed to save 1078 AV param response", zap.Error(err))
	}
}

func (h *MessageHandler) handle1078PlaybackControl(session *gateway.Session, msg *protocol.Message) {
	req, ok := msg.Body.(*jt1078.PlaybackControlMessage)
	if !ok {
		h.logger.Error("invalid 1078 playback control body")
		return
	}
	h.logger.Info("1078 playback control",
		zap.String("phone", session.GetPhone()),
		zap.Uint8("channel", req.LogicChannel),
		zap.Uint8("command", req.Command),
		zap.Uint8("speed", req.Speed))

	// ͻطſӦ𣨸 RealtimeResponseMessage ṹ
	resp := &jt1078.RealtimeResponseMessage{
		SeqNum:       msg.Header.SeqNum,
		LogicChannel: req.LogicChannel,
		Result:       0x00,
	}
	h.send808Response(session, jt1078.MsgIDPlaybackControlAck, resp, msg.Header.SeqNum)
}

func (h *MessageHandler) handle1078DownloadResponse(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt1078.DownloadResponseMessage)
	if !ok {
		h.logger.Error("invalid 1078 download response body")
		return
	}
	h.logger.Info("1078 download response",
		zap.String("phone", session.GetPhone()),
		zap.Uint16("resp_seq", resp.RespSeqNum),
		zap.Uint8("channel", resp.LogicChannel),
		zap.Uint8("result", resp.Result))
}

// AUTO-FIX-2026-06-28: ȫ 1078 նˡƽ̨ӦϢ
// ԭ handle1078 switch ȱʧЩ caseն˵Ӧᱻƽ̨޷ִ֪н

// handle1078TermAVRequest  0x9103 նʵʱƵ
func (h *MessageHandler) handle1078TermAVRequest(session *gateway.Session, msg *protocol.Message) {
	req, ok := msg.Body.(*jt1078.TermAVRequestMessage)
	if !ok {
		h.logger.Error("invalid 1078 term av request body")
		return
	}
	h.logger.Info("1078 terminal initiated av request",
		zap.String("phone", session.GetPhone()),
		zap.Uint8("channel", req.LogicChannel),
		zap.Uint8("media_type", req.MediaType),
		zap.Uint8("stream_type", req.StreamType))
	// նƵƽ̨· 0x9102 Ӧ
	resp := &jt1078.RealtimeResponseMessage{
		SeqNum:       msg.Header.SeqNum,
		LogicChannel: req.LogicChannel,
		Result:       0x00, // 0=ɹ
	}
	h.send808Response(session, jt1078.MsgIDRealtimeResponse, resp, msg.Header.SeqNum)
}

// handle1078AVStatusNotificationResponse 0x9106 状态通知应答（平台→终端的应答确认）
func (h *MessageHandler) handle1078AVStatusNotificationResponse(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt1078.AVStatusNotificationResponseMessage)
	if !ok {
		h.logger.Error("invalid 1078 av status notification response body")
		return
	}
	h.logger.Info("1078 av status notification response",
		zap.String("phone", session.GetPhone()),
		zap.Uint16("seq", resp.SeqNum),
		zap.Uint8("channel", resp.LogicChannel))
}

// handle1078PlaybackResponse  0x9202 طӦ
func (h *MessageHandler) handle1078PlaybackResponse(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt1078.PlaybackResponseMessage)
	if !ok {
		h.logger.Error("invalid 1078 playback response body")
		return
	}
	h.logger.Info("1078 playback response",
		zap.String("phone", session.GetPhone()),
		zap.Uint16("resp_seq", resp.RespSeqNum),
		zap.Uint8("channel", resp.LogicChannel),
		zap.Uint8("result", resp.Result),
		zap.Int("resource_count", len(resp.Items)))
}

// handle1078PlaybackControlAck  0x9204 طſӦ
func (h *MessageHandler) handle1078PlaybackControlAck(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt1078.PlaybackControlAckMessage)
	if !ok {
		h.logger.Error("invalid 1078 playback control ack body")
		return
	}
	h.logger.Info("1078 playback control ack",
		zap.String("phone", session.GetPhone()),
		zap.Uint16("resp_seq", resp.RespSeqNum),
		zap.Uint8("channel", resp.LogicChannel),
		zap.Uint8("result", resp.Result))
}

// handle1078PTZControlAck  0x9302 PTZ Ӧ
func (h *MessageHandler) handle1078PTZControlAck(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt1078.PTZControlAckMessage)
	if !ok {
		h.logger.Error("invalid 1078 ptz control ack body")
		return
	}
	h.logger.Info("1078 ptz control ack",
		zap.String("phone", session.GetPhone()),
		zap.Uint16("seq", resp.SeqNum),
		zap.Uint8("channel", resp.LogicChannel),
		zap.Uint8("result", resp.Result))
}

// handle1078AlarmVideoResponse  0x9402 ƵӦ
func (h *MessageHandler) handle1078AlarmVideoResponse(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt1078.AlarmVideoResponseMessage)
	if !ok {
		h.logger.Error("invalid 1078 alarm video response body")
		return
	}
	h.logger.Info("1078 alarm video response",
		zap.String("phone", session.GetPhone()),
		zap.Uint16("seq", resp.SeqNum),
		zap.Uint8("channel", resp.LogicChannel),
		zap.Uint8("result", resp.Result))
}

// handle1078FileUploadRequest  0x9403 ļϴնˡƽ̨
func (h *MessageHandler) handle1078FileUploadRequest(session *gateway.Session, msg *protocol.Message) {
	req, ok := msg.Body.(*jt1078.FileUploadRequestMessage)
	if !ok {
		h.logger.Error("invalid 1078 file upload request body")
		return
	}
	h.logger.Info("1078 file upload request",
		zap.String("phone", session.GetPhone()),
		zap.Uint8("channel", req.LogicChannel),
		zap.String("start_time", req.StartTime),
		zap.String("end_time", req.EndTime),
		zap.Uint32("alarm_flag", req.AlarmFlag),
		zap.Uint8("media_type", req.MediaType),
		zap.Uint8("stream_type", req.StreamType),
		zap.Uint8("storage_type", req.StorageType),
		zap.Uint8("download_type", req.DownloadType),
		zap.String("ip", req.IPAddress),
		zap.Uint16("tcp_port", req.TcpPort),
		zap.Uint16("udp_port", req.UdpPort),
		zap.String("file_path", req.FilePath))
}

// handle1078TerminalLogResp  0x9602 ն־Ӧ
func (h *MessageHandler) handle1078TerminalLogResp(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt1078.TerminalLogResponseMessage)
	if !ok {
		h.logger.Error("invalid 1078 terminal log response body")
		return
	}
	h.logger.Info("1078 terminal log response",
		zap.String("phone", session.GetPhone()),
		zap.Uint16("resp_seq", resp.RespSeqNum),
		zap.Uint8("channel", resp.LogicChannel),
		zap.Uint8("result", resp.Result),
		zap.Uint8("log_count", resp.LogCount))
}

// AUTO-FIX-2026-06-28: ƽ̨Ϣ0x1A00/0x1A01/0x1B00/0x1B01handler
// ЩϢ JT/T 809-2019 ж壬ڿƽ̨ƵЭת
// ͨ 809 ·ϵͳ 1078 codec Ϊרýṹ壬ڴ˴

func (h *MessageHandler) handle1078AVNegotiate(session *gateway.Session, msg *protocol.Message) {
	req, ok := msg.Body.(*jt1078.PlatformNegotiateMessage)
	if !ok {
		h.logger.Error("invalid 1078 platform negotiate body")
		return
	}
	h.logger.Info("1078 platform AV negotiate request",
		zap.String("phone", req.Phone),
		zap.Uint8("channel", req.LogicChannel),
		zap.Uint8("av_type", req.AVType),
		zap.Uint8("stream_type", req.StreamType),
		zap.Uint8("protocol_type", req.ProtocolType),
		zap.String("ip", req.IPAddress),
		zap.Uint16("port", req.Port))
	if h.eventBus != nil {
		h.eventBus.Publish("platform.av.negotiate", req)
	}
}

func (h *MessageHandler) handle1078AVNegotiateResp(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt1078.PlatformNegotiateResponse)
	if !ok {
		h.logger.Error("invalid 1078 platform negotiate response body")
		return
	}
	h.logger.Info("1078 platform AV negotiate response",
		zap.String("phone", resp.Phone),
		zap.Uint8("channel", resp.LogicChannel),
		zap.Uint8("result", resp.Result),
		zap.String("ip", resp.IPAddress),
		zap.Uint16("port", resp.Port))
	if h.eventBus != nil {
		h.eventBus.Publish("platform.av.negotiate.resp", resp)
	}
}

func (h *MessageHandler) handle1078AVForward(session *gateway.Session, msg *protocol.Message) {
	req, ok := msg.Body.(*jt1078.PlatformForwardMessage)
	if !ok {
		h.logger.Error("invalid 1078 platform forward body")
		return
	}
	h.logger.Info("1078 platform AV forward request",
		zap.String("phone", req.Phone),
		zap.Uint8("channel", req.LogicChannel),
		zap.Uint8("av_type", req.AVType),
		zap.Uint8("stream_type", req.StreamType),
		zap.String("start_time", req.StartTime),
		zap.String("end_time", req.EndTime))
	if h.eventBus != nil {
		h.eventBus.Publish("platform.av.forward", req)
	}
}

func (h *MessageHandler) handle1078AVForwardResp(session *gateway.Session, msg *protocol.Message) {
	resp, ok := msg.Body.(*jt1078.PlatformForwardResponse)
	if !ok {
		h.logger.Error("invalid 1078 platform forward response body")
		return
	}
	h.logger.Info("1078 platform AV forward response",
		zap.String("phone", resp.Phone),
		zap.Uint8("channel", resp.LogicChannel),
		zap.Uint8("result", resp.Result))
	if h.eventBus != nil {
		h.eventBus.Publish("platform.av.forward.resp", resp)
	}
}

func (h *MessageHandler) handle1045(session *gateway.Session, msg *protocol.Message) {
	handler, ok := h.handlerRegistry.Get(protocol.ProtocolJT1045)
	if !ok {
		h.logger.Warn("1045 protocol handler not registered, install module-protocol-1045",
			zap.Uint16("msg_id", msg.Header.MsgID),
			zap.String("session", session.ID))
		return
	}
	if err := handler.HandleMessage(gatewaySessionAdapter{session}, msg, h.protocolHub); err != nil {
		h.logger.Error("1045 handler error", zap.Error(err), zap.String("session", session.ID))
	}
}

func (h *MessageHandler) handle905(session *gateway.Session, msg *protocol.Message) {
	handler, ok := h.handlerRegistry.Get(protocol.ProtocolJT905)
	if !ok {
		h.logger.Warn("905 protocol handler not registered, install module-protocol-905",
			zap.Uint16("msg_id", msg.Header.MsgID),
			zap.String("session", session.ID))
		return
	}
	if err := handler.HandleMessage(gatewaySessionAdapter{session}, msg, h.protocolHub); err != nil {
		h.logger.Error("905 handler error", zap.Error(err), zap.String("session", session.ID))
	}
}

func (h *MessageHandler) handle1253(session *gateway.Session, msg *protocol.Message) {
	handler, ok := h.handlerRegistry.Get(protocol.ProtocolJT1253)
	if !ok {
		h.logger.Warn("1253 protocol handler not registered, install module-protocol-1253",
			zap.Uint16("msg_id", msg.Header.MsgID),
			zap.String("session", session.ID))
		return
	}
	if err := handler.HandleMessage(gatewaySessionAdapter{session}, msg, h.protocolHub); err != nil {
		h.logger.Error("1253 handler error", zap.Error(err), zap.String("session", session.ID))
	}
}

func (h *MessageHandler) handle32960(session *gateway.Session, msg *protocol.Message) {
	handler, ok := h.handlerRegistry.Get(protocol.ProtocolGBT32960)
	if !ok {
		h.logger.Warn("32960 protocol handler not registered, install module-protocol-32960",
			zap.Uint16("msg_id", msg.Header.MsgID),
			zap.String("session", session.ID))
		return
	}
	if err := handler.HandleMessage(gatewaySessionAdapter{session}, msg, h.protocolHub); err != nil {
		h.logger.Error("32960 handler error", zap.Error(err), zap.String("session", session.ID))
	}
}

func (h *MessageHandler) logProtocolMessage(session *gateway.Session, msg *protocol.Message, direction string) {
	var rawHex string
	if msg.Raw != nil {
		rawHex = hex.EncodeToString(msg.Raw)
	} else {
		rawHex = ""
	}

	protoLog := &storage.ProtocolLog{
		ID:         fmt.Sprintf("%d", time.Now().UnixNano()),
		SessionID:  session.ID,
		Phone:      session.GetPhone(),
		Protocol:   string(session.GetProtocol()),
		MsgType:    msg.Header.MsgID,
		MsgName:    msgNameByID(session.GetProtocol(), msg.Header.MsgID),
		Direction:  direction,
		RawHex:     rawHex,
		Length:     len(msg.Raw),
		ReceivedAt: time.Now(),
	}

	if err := h.store.SaveProtocolLog(context.Background(), protoLog); err != nil {
		h.logger.Debug("save protocol log failed", zap.Error(err))
	}
}

func msgNameByID(proto protocol.ProtocolType, msgID uint16) string {
	switch proto {
	case protocol.ProtocolJT808:
		switch msgID {
		case jt808.MsgIDRegister:
			return "նע"
		case jt808.MsgIDAuth:
			return "ն˼Ȩ"
		case jt808.MsgIDHeartbeat:
			return ""
		case jt808.MsgIDLocation:
			return "λϱ"
		case jt808.MsgIDLocationBatch:
			return "λϱ"
		case jt808.MsgIDAlarm:
			return "ն˱"
		case jt808.MsgIDAlarmAttachment:
			return "ϴ"
		case jt808.MsgIDAlarmAttachmentResp:
			return "ϴӦ"
		case jt808.MsgIDTerminalCancel:
			return "նע"
		case jt808.MsgIDMultimedia:
			return "ý¼"
		case jt808.MsgIDMultimediaUpload:
			return "ýϴ"
		case jt808.MsgIDDriverID:
			return "ʻԱ"
		case jt808.MsgIDCanData:
			return "CAN"
		case jt808.MsgIDElectronicWaybill:
			return "˵"
		case jt808.MsgIDGeneralResp:
			return "ͨӦ"
		case jt808.MsgIDTerminalGeneralResp:
			return "նͨӦ"
		case jt808.MsgIDCommandResp:
			return "ָӦ"
		case jt808.MsgIDParamResp:
			return "ѯӦ"
		case jt808.MsgIDTerminalPropResp:
			return "նӦ"
		case jt808.MsgIDTerminalUpgradeResp:
			return "նӦ"
		case jt808.MsgIDInfoMenuResp:
			return "Ϣ˵Ӧ"
		case jt808.MsgIDSMSForwardResp:
			return "תӦ"
		case jt808.MsgIDEventResp:
			return "¼Ӧ"
		case jt808.MsgIDQuestionResp:
			return "Ӧ"
		case jt808.MsgIDLocationQueryResp:
			return "λòѯӦ"
		case jt808.MsgIDAlarmAck:
			return "ȷ"
		case jt808.MsgIDStorageMediaSearch:
			return "洢ý"
		case jt808.MsgIDStorageMediaUpload:
			return "洢ýϴ"
		// AUTO-FIX-2026-06-28: 0x0A00 ѻع RSA Կ
		// MsgIDRSAPublicKey  MsgIDPassengerCount ֵͬ0x0A00һ duplicate case
		case jt808.MsgIDRSAPublicKey:
			return "RSAԿ"
		case jt808.MsgIDRSADistribute:
			return "RSAԿ·"
		case jt808.MsgIDBillOperate:
			return "Ƽ"
		case jt808.MsgIDTempLocationTrackResp:
			return "ʱλøӦ"
		case jt808.MsgIDOverspeedAlarm:
			return "ٱ"
		case jt808.MsgIDFatigueDriveAlarm:
			return "ƣͼʻ"
		case jt808.MsgIDFireAreaAlarm:
			return "򱨾"
		}
	case protocol.ProtocolJT809:
		switch msgID {
		case 0x1001:
			return "·¼"
		case 0x1002:
			return "·ע"
		case 0x1003:
			return "·"
		case 0x1006:
			return "·Ͽ"
		case 0x1201:
			return "ϴλ"
		case 0x1202:
			return "λϴ"
		case 0x1204:
			return "λ"
		case 0x1300:
			return "·Ϣ"
		case 0x1301:
			return "ϱʻԱ"
		case 0x1401:
			return "Ϣ"
		case 0x9004:
			return "·¼Ӧ"
		case 0x9006:
			return "·ϿӦ"
		case 0x9204:
			return "Ӧ"
		case 0x9205:
			return "ʻԱϢӦ"
		case 0x9206:
			return "Ӧ"
		case 0x9207:
			return "Ӧ"
		case 0x9300:
			return "·ϢӦ"
		case 0x9401:
			return "ȷ"
		case 0x9500:
			return "Ӧ"
		case 0x9700:
			return "ƽ̨ϢӦ"
		case 0x1B03:
			return "ƵʷӦ"
		case 0x1B05:
			return "ƵӦ"
		}
	case protocol.ProtocolJT1078:
		// AUTO-FIX-2026-06-28:  1078 ϢIDӳ䣬ԭʹ 0x1200-0x1207809ϢID
		// ʹ JT/T 1078-2016/2022 ׼Ϣ ID0x9xxx + 0x1Axx/0x1Bxx ƽ̨Ϣ
		switch msgID {
		case 0x9101:
			return "ʵʱƵ"
		case 0x9102:
			return "ʵʱƵӦ"
		case 0x9103:
			return "նʵʱƵ"
		case 0x9104:
			return "նʵʱƵӦ"
		case 0x9105:
			return "Ƶ"
		case 0x9106:
			return "ƵӦ"
		case 0x9201:
			return "Ƶط"
		case 0x9202:
			return "ƵطӦ"
		case 0x9203:
			return "Ƶطſ"
		case 0x9204:
			return "ƵطſӦ"
		case 0x9205:
			return "¼"
		case 0x9206:
			return "¼Ӧ"
		case 0x9207:
			return "¼ؿ"
		case 0x9301:
			return "PTZ"
		case 0x9302:
			return "PTZӦ"
		case 0x9401:
			return "Ƶ"
		case 0x9402:
			return "ƵӦ"
		case 0x9403:
			return "ļϴ"
		case 0x9404:
			return "ļϴӦ"
		case 0x9501:
			return "Ƶ"
		case 0x9502:
			return "Ƶѯ"
		case 0x9503:
			return "ƵӦ"
		case 0x9601:
			return "ն־"
		case 0x9602:
			return "ն־Ӧ"
		case 0x9603:
			return "ն־ϴ"
		case 0x1200:
			return "RTP"
		case 0x1A00:
			return "ƽ̨ƵЭ"
		case 0x1A01:
			return "ƽ̨ƵЭӦ"
		case 0x1B00:
			return "ƽ̨Ƶת"
		case 0x1B01:
			return "ƽ̨ƵתӦ"
		}
	case protocol.ProtocolJT1045:
		switch msgID {
		case 0x0900:
			return "DSM"
		case 0x0901:
			return "ADAS"
		case 0x0902:
			return "ä"
		case 0x0903:
			return "̥"
		case 0x0904:
			return "DSM״̬"
		case 0x0905:
			return "ADAS״̬"
		case 0x0906:
			return "ä״̬"
		case 0x0907:
			return "̥״̬"
		}
	case protocol.ProtocolJT905:
		switch msgID {
		//  վϢն  ƽ̨
		case 0x0001:
			return "նͨӦ"
		case 0x0002:
			return ""
		case 0x0003:
			return "նע"
		case 0x0100:
			return "նע"
		case 0x0102:
			return "ն˼Ȩ"
		case 0x0103:
			return "ָӦ"
		case 0x0104:
			return "ѯӦ"
		case 0x0107:
			return "նӦ"
		case 0x0200:
			return "λϱ"
		case 0x0201:
			return "λòѯӦ"
		case 0x0301:
			return "״̬Ӧ"
		case 0x0302:
			return "źӦ"
		case 0x0303:
			return "ʱͬӦ"
		case 0x0400:
			return ""
		case 0x0700:
			return "Ϣ"
		case 0x0701:
			return "Ӧ"
		case 0x0800:
			return ""
		case 0x0900:
			return "Ƽ"
		case 0x0901:
			return "Ϣ"
		case 0x0A00:
			return "CAN"
		case 0x0A01:
			return "Ӧ"
		case 0x0B00:
			return ""
		//  վϢƽ̨  նˣ
		case 0x8001:
			return "ƽ̨ͨӦ"
		case 0x8003:
			return "նעӦ"
		case 0x8100:
			return "עӦ"
		case 0x8103:
			return "ն˲"
		case 0x8104:
			return "ѯն˲"
		case 0x8105:
			return "ն˿"
		case 0x8106:
			return ""
		case 0x8107:
			return "նԲѯ"
		case 0x8108:
			return "ı·"
		case 0x8200:
			return "λòѯ"
		case 0x8201:
			return "λø"
		case 0x8300:
			return "Ӧ"
		case 0x8301:
			return "״̬ѯ"
		case 0x8302:
			return "źŲѯ"
		case 0x8303:
			return "ʱͬ"
		case 0x8400:
			return "Ӧ"
		case 0x8500:
			return ""
		case 0x8600:
			return "Բ"
		case 0x8601:
			return "Բɾ"
		case 0x8602:
			return ""
		case 0x8603:
			return "ɾ"
		case 0x8604:
			return ""
		case 0x8605:
			return "ɾ"
		case 0x8606:
			return "·"
		case 0x8607:
			return "·ɾ"
		case 0x8608:
			return "ٱ"
		case 0x8609:
			return "ƣͼʻ"
		case 0x8700:
			return "Ϣ·"
		case 0x8801:
			return ""
		case 0x8900:
			return "Ӧ"
		}
	case protocol.ProtocolGBT32960:
		switch msgID {
		case 0x01:
			return "¼"
		case 0x02:
			return "ǳ"
		case 0x03:
			return ""
		case 0x04:
			return "ʵʱ"
		case 0x05:
			return ""
		case 0x06:
			return ""
		case 0x07:
			return "ʼ"
		case 0x08:
			return ""
		case 0x80:
			return "ն˲"
		case 0x81:
			return "ѯ"
		case 0x82:
			return "ն˵¼"
		case 0x83:
			return "ն˵ǳ"
		case 0x84:
			return "Ӧ"
		case 0x85:
			return "նУ"
		case 0x86:
			return "ʱͬ"
		case 0x87:
			return "־Ӧ"
		}
	}
	return fmt.Sprintf("0x%04X", msgID)
}
