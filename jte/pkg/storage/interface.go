package storage

import (
	"context"
	"time"
)

type Vehicle struct {
	ID           string    `json:"id"`
	Phone        string    `json:"phone"`
	Protocol     string    `json:"protocol"`
	PlateNo      string    `json:"plate_no"`
	PlateColor   int       `json:"plate_color"`
	TerminalID   string    `json:"terminal_id"`
	TerminalType string    `json:"terminal_type"`
	Manufacturer string    `json:"manufacturer"`
	ProvinceID   int       `json:"province_id"`
	CityID       int       `json:"city_id"`
	Online       bool      `json:"online"`
	RegisteredAt time.Time `json:"registered_at"`
	LastActive   time.Time `json:"last_active"`
}

type LocationData struct {
	VehicleID  string    `json:"vehicle_id"`
	Phone      string    `json:"phone"`
	Latitude   float64   `json:"latitude"`
	Longitude  float64   `json:"longitude"`
	Altitude   float64   `json:"altitude"`
	Speed      float64   `json:"speed"`
	Direction  int       `json:"direction"`
	Time       time.Time `json:"time"`
	AlarmFlag  uint32    `json:"alarm_flag"`
	StatusFlag uint32    `json:"status_flag"`
	Mileage    float64   `json:"mileage,omitempty"`
	Fuel       float64   `json:"fuel,omitempty"`
	ReceivedAt time.Time `json:"received_at"`
	Source     string    `json:"source"`
}

type AlarmData struct {
	ID         string    `json:"id"`
	VehicleID  string    `json:"vehicle_id"`
	Phone      string    `json:"phone"`
	Type       string    `json:"type"`
	Level      int       `json:"level"`
	AlarmFlag  uint32    `json:"alarm_flag"`
	Latitude   float64   `json:"latitude"`
	Longitude  float64   `json:"longitude"`
	Altitude   float64   `json:"altitude"`
	Speed      float64   `json:"speed"`
	Direction  int       `json:"direction"`
	Time       time.Time `json:"time"`
	Additional []byte    `json:"additional,omitempty"`
	ReceivedAt time.Time `json:"received_at"`
	Source     string    `json:"source"`
	// AUTO-FIX-2026-06-26: 第一轮协议完整性审计 - AI 过滤置信度与原因
	// Confidence 由 AIEngine.FilterAlarm 写入，取值 [0,1]，0 表示未经过 AI 过滤。
	Confidence float64 `json:"confidence,omitempty"`
	// AIReason AI 判定原因（如 vehicle_stationary / burst_alarms 等）。
	AIReason string `json:"ai_reason,omitempty"`
	// AUTO-FIX-2026-06-28: AI 责任边界标记，安全类报警必须人工复核
	// 由 AIEngine.AnalyzeAlarmDetailed 写入，true 表示需人工复核后才能处理/关闭
	RequireManualReview bool `json:"require_manual_review,omitempty"`
	// AUTO-FIX-2026-07-02 [P1]: 报警来源下级平台 ID（809 级联转发用）。
	// 当报警由下级 809 平台转发而来（0x1401/0x1400），此字段记录源平台 ID，
	// 供 StartAutoForward 传递给 shouldForward 进行平台间定向转发规则匹配。
	// 空字符串表示报警来自本平台直连终端（非级联），规则中 SourcePlatformID="" 匹配所有。
	SourcePlatformID string `json:"source_platform_id,omitempty"`
}

type SessionData struct {
	ID           string    `json:"id"`
	Phone        string    `json:"phone"`
	Protocol     string    `json:"protocol"`
	RemoteAddr   string    `json:"remote_addr"`
	Status       string    `json:"status"`
	RegisteredAt time.Time `json:"registered_at"`
	LastActive   time.Time `json:"last_active"`
}

type ProtocolLog struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	Phone      string    `json:"phone"`
	Protocol   string    `json:"protocol"`
	MsgType    uint16    `json:"msg_type"`
	MsgName    string    `json:"msg_name"`
	Direction  string    `json:"direction"`
	RawHex     string    `json:"raw_hex"`
	Length     int       `json:"length"`
	ReceivedAt time.Time `json:"received_at"`
}

type DriverInfoData struct {
	ID            string    `json:"id"`
	VehicleID     string    `json:"vehicle_id"`
	Phone         string    `json:"phone"`
	DriverName    string    `json:"driver_name"`
	LicenseID     string    `json:"license_id"`
	LicenseOrg    string    `json:"license_org"`
	LicenseExpiry string    `json:"license_expiry"`
	IDCard        string    `json:"id_card"`
	ReceivedAt    time.Time `json:"received_at"`
	Source        string    `json:"source"`
}

type MultimediaData struct {
	ID            string    `json:"id"`
	VehicleID     string    `json:"vehicle_id"`
	Phone         string    `json:"phone"`
	MultimediaID  uint32    `json:"multimedia_id"` // AUTO-FIX-2026-07-04: 多媒体ID（0x0805/0x0802 关联用）
	MediaType     int       `json:"media_type"`
	MediaFormat   int       `json:"media_format"`
	EventItem     int       `json:"event_item"`
	ChannelID     int       `json:"channel_id"`
	Latitude      float64   `json:"latitude"`
	Longitude     float64   `json:"longitude"`
	// AUTO-FIX-2026-07-04 [P0]: 附件二进制数据（1045 报警附件上传 / 1078 截图等）
	Data          []byte    `json:"data,omitempty"`
	ReceivedAt    time.Time `json:"received_at"`
	Source        string    `json:"source"`
}

type CanBusData struct {
	ID         string       `json:"id"`
	VehicleID  string       `json:"vehicle_id"`
	Phone      string       `json:"phone"`
	Items      []CanBusItem `json:"items"`
	ReceivedAt time.Time    `json:"received_at"`
	Source     string       `json:"source"`
}

type CanBusItem struct {
	CanID uint32 `json:"can_id"`
	Value []byte `json:"value"`
}

type BDNavData struct {
	ID         string    `json:"id"`
	VehicleID  string    `json:"vehicle_id"`
	Phone      string    `json:"phone"`
	SatCount   int       `json:"sat_count"`
	Latitude   float64   `json:"latitude"`
	Longitude  float64   `json:"longitude"`
	Altitude   uint16    `json:"altitude"`
	Speed      uint16    `json:"speed"`
	Direction  uint16    `json:"direction"`
	BDTime     string    `json:"bd_time"`
	ReceivedAt time.Time `json:"received_at"`
	Source     string    `json:"source"`
}

type MeterData struct {
	ID         string    `json:"id"`
	VehicleID  string    `json:"vehicle_id"`
	Phone      string    `json:"phone"`
	MeterValue float64   `json:"meter_value"`
	// AUTO-FIX-2026-07-04 [905 计价器数据完整持久化]: 补全计价器全字段
	MeterStatus uint8   `json:"meter_status,omitempty"`  // 计价器状态（0:空车 1:重车 2:暂停 3:停运）
	EmptyFlag   uint8   `json:"empty_flag,omitempty"`    // 空重车标志
	PriceID     uint16  `json:"price_id,omitempty"`      // 单价ID
	TotalKm     uint32  `json:"total_km,omitempty"`      // 营业里程（0.1km）
	EmptyKm     uint32  `json:"empty_km,omitempty"`      // 空驶里程（0.1km）
	TotalFare   uint32  `json:"total_fare,omitempty"`    // 营业金额（分）
	EmptyFare   uint32  `json:"empty_fare,omitempty"`    // 空驶金额（分）
	WaitTime    uint16  `json:"wait_time,omitempty"`     // 等待时间（秒）
	StartTime   string  `json:"start_time,omitempty"`    // 营业开始时间 BCD
	EndTime     string  `json:"end_time,omitempty"`      // 营业结束时间 BCD
	ReceivedAt  time.Time `json:"received_at"`
	Source      string    `json:"source"`
}

type DispatchData struct {
	ID         string    `json:"id"`
	VehicleID  string    `json:"vehicle_id"`
	Phone      string    `json:"phone"`
	Content    string    `json:"content"`
	ReceivedAt time.Time `json:"received_at"`
	Source     string    `json:"source"`
}

type ElectronicWaybillData struct {
	ID         string    `json:"id"`
	Phone      string    `json:"phone"`
	WaybillNo  string    `json:"waybill_no"`
	Content    string    `json:"content"`
	ReceivedAt time.Time `json:"received_at"`
}

type CommandRespData struct {
	ID         string    `json:"id"`
	Phone      string    `json:"phone"`
	CommandID  string    `json:"command_id"`
	Result     int       `json:"result"`
	ReceivedAt time.Time `json:"received_at"`
}

type TerminalPropData struct {
	ID              string    `json:"id"`
	Phone           string    `json:"phone"`
	ManufacturerID  string    `json:"manufacturer_id"`
	Model           string    `json:"model"`
	HardwareVersion string    `json:"hardware_version"`
	FirmwareVersion string    `json:"firmware_version"`
	GNSSSupport     int       `json:"gnss_support"`
	CommModule      int       `json:"comm_module"`
	ReceivedAt      time.Time `json:"received_at"`
}

type AVParamData struct {
	ID         string    `json:"id"`
	Phone      string    `json:"phone"`
	ChannelID  int       `json:"channel_id"`
	ParamType  int       `json:"param_type"`
	ParamValue string    `json:"param_value"`
	ReceivedAt time.Time `json:"received_at"`
}

// InfoMenuRespData 信息菜单应答数据
type InfoMenuRespData struct {
	ID         string    `json:"id"`
	Phone      string    `json:"phone"`
	InfoType   int       `json:"info_type"`
	InfoID     uint32    `json:"info_id"`
	InfoData   string    `json:"info_data"`
	ReceivedAt time.Time `json:"received_at"`
}

// AUTO-FIX-2026-06-29: GB/T 32960 电动汽车数据持久化结构
// EVData 统一存储 32960 协议中整车/电机/燃料电池/发动机/极值/充电等数据组。
// Data 字段为各数据组结构体 JSON 序列化后的字节流，便于灵活扩展字段而不频繁变更表结构。
// DataType 取值: vehicle/motor/fuelcell/engine/extreme/charging/battery
type EVData struct {
	ID         string    `json:"id"`
	VehicleID  string    `json:"vehicle_id"`
	Phone      string    `json:"phone"`
	DataType   string    `json:"data_type"`
	Data       []byte    `json:"data"`
	ReceivedAt time.Time `json:"received_at"`
	Source     string    `json:"source"`
}

// SMSForwardRespData 短信转发应答数据
type SMSForwardRespData struct {
	ID         string    `json:"id"`
	Phone      string    `json:"phone"`
	SMSContent string    `json:"sms_content"`
	ReceivedAt time.Time `json:"received_at"`
}

// EventRespData 事件应答数据
type EventRespData struct {
	ID         string    `json:"id"`
	Phone      string    `json:"phone"`
	EventID    uint32    `json:"event_id"`
	ReceivedAt time.Time `json:"received_at"`
}

type ListOptions struct {
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
	Offset   int    `json:"offset"`
	OrderBy  string `json:"order_by"`
	Phone    string `json:"phone,omitempty"`
	Online   *bool  `json:"online,omitempty"`
	Start    string `json:"start,omitempty"`
	End      string `json:"end,omitempty"`
	// OrgID 组织过滤（电子围栏按组织隔离使用）
	OrgID string `json:"org_id,omitempty"`
	// AlarmID 按报警 ID 精确查询（用于 GetAlarm / AIFalseAlarmCheck 等按 ID 查报警场景）
	AlarmID string `json:"alarm_id,omitempty"`
}

// Geofence 电子围栏（圆形/矩形/多边形）
// Params 字段为 JSON 字符串：
//   - 圆形: {"center":{"lat":..,"lng":..},"radius":1000}
//   - 矩形: {"sw":{"lat":..,"lng":..},"ne":{"lat":..,"lng":..}}
//   - 多边形: {"points":[{"lat":..,"lng":..},...]}
type Geofence struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      int       `json:"type"` // 1:圆形 2:矩形 3:多边形
	OrgID     string    `json:"org_id"`
	Params    string    `json:"params"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ListResult struct {
	Items interface{} `json:"items"`
	Total int64       `json:"total"`
	Page  int         `json:"page"`
	Size  int         `json:"size"`
}

// ForwardRule 809 转发规则（持久化到关系库，支持运行期热更新）。
// AUTO-FIX-2026-06-29 [P1-6]: 原转发规则仅 YAML 配置，无法运行期修改且缺报警类型/时间过滤。
// AUTO-FIX-2026-07-02: 新增 SourcePlatformID 字段支持平台间定向转发；
// DataType "video" 支持 0x1B00 视频转发规则过滤。
// 每条规则描述"将某源平台的某类数据转发到某目标上级平台"的过滤条件。
type ForwardRule struct {
	ID               string    `json:"id"`
	SourcePlatformID string    `json:"source_platform_id"` // 源下级平台 ID，空=所有平台（对应 809 登录 UserID）
	PlatformID       string    `json:"platform_id"`        // 目标上级平台 ID（对应 JT809PlatformConfig.ID）
	DataType         string    `json:"data_type"`          // "location" | "alarm" | "video"
	Phone            string    `json:"phone"`              // 车辆手机号，空=全部车辆
	AlarmTypes       string    `json:"alarm_types"`        // 逗号分隔报警类型，空=全部类型（如 "overspeed,emergency"）
	MinLevel         int       `json:"min_level"`          // 最低报警级别（0=全部，1=一般，2=严重，3=紧急）
	TimeStart        string    `json:"time_start"`         // 每日生效起始时间 HH:MM:SS，空=不限
	TimeEnd          string    `json:"time_end"`           // 每日生效结束时间 HH:MM:SS，空=不限
	Enabled          bool      `json:"enabled"`            // 是否启用
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Platform 下级/上级 809 平台配置（AUTO-FIX-2026-07-02 [P1]）。
// 持久化存储 809 级联平台接入信息，替代原 YAML-only 的 DownstreamPlatformConfig/JT809PlatformConfig。
// 支持下级平台（Role="downstream"，本平台作为上级接收连接）和上级平台
//（Role="upstream"，本平台作为下级主动连接）两种角色。
// Enabled=false 的平台不会被加载，已连接的会被断开。
type Platform struct {
	ID         string    `json:"id"`          // 平台唯一标识（如 "plat_1001"），对应 809 登录 UserID
	Name       string    `json:"name"`        // 平台名称（显示用）
	UserID     string    `json:"user_id"`     // 809 用户 ID（数字字符串，登录时用）
	Password   string    `json:"password"`    // 809 登录密码
	Role       string    `json:"role"`        // "downstream"（下级，被动接收） | "upstream"（上级，主动连接）
	Host       string    `json:"host"`        // 上级平台地址（Role="upstream" 时使用）
	Port       int       `json:"port"`        // 上级平台端口（Role="upstream" 时使用）
	LinkType   int       `json:"link_type"`   // 链路类型（0=主链路，1=从链路）
	DownLinkID string    `json:"downlink_id"` // 下级平台标识（Role="downstream" 时使用）
	Enabled    bool      `json:"enabled"`     // 是否启用
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Interface interface {
	SaveVehicle(ctx context.Context, vehicle *Vehicle) error
	GetVehicle(ctx context.Context, id string) (*Vehicle, error)
	GetVehicleByPhone(ctx context.Context, phone string) (*Vehicle, error)
	ListVehicles(ctx context.Context, opts ListOptions) (*ListResult, error)
	DeleteVehicle(ctx context.Context, id string) error
	UpdateVehicleOnline(ctx context.Context, id string, online bool) error

	SaveLocation(ctx context.Context, loc *LocationData) error
	GetLatestLocation(ctx context.Context, vehicleID string) (*LocationData, error)
	GetLocationTrack(ctx context.Context, vehicleID string, start, end time.Time) ([]*LocationData, error)
	ListOnlineLocations(ctx context.Context) ([]*LocationData, error)

	SaveAlarm(ctx context.Context, alarm *AlarmData) error
	// UpdateAlarm 更新报警数据（用于 AI 过滤结果回写等场景）
	UpdateAlarm(ctx context.Context, alarm *AlarmData) error
	ListAlarms(ctx context.Context, opts ListOptions) (*ListResult, error)

	SaveSession(ctx context.Context, session *SessionData) error
	GetSession(ctx context.Context, id string) (*SessionData, error)
	ListSessions(ctx context.Context, opts ListOptions) (*ListResult, error)
	DeleteSession(ctx context.Context, id string) error

	SaveProtocolLog(ctx context.Context, log *ProtocolLog) error
	ListProtocolLogs(ctx context.Context, opts ListOptions) (*ListResult, error)

	GetOnlineCount(ctx context.Context) (int64, error)
	GetAlarmCount(ctx context.Context, start, end time.Time) (int64, error)
	GetAlarmCountBySource(ctx context.Context, source string, start, end time.Time) (int64, error)
	// 批量写入位置数据（事务+批量INSERT）
	BatchSaveLocations(ctx context.Context, locations []*LocationData) error
	// 批量写入报警数据
	BatchSaveAlarms(ctx context.Context, alarms []*AlarmData) error
	// 批量写入协议日志
	BatchSaveProtocolLogs(ctx context.Context, logs []*ProtocolLog) error

	GetOfflineCount(ctx context.Context) (int64, error)

	SaveDriverInfo(ctx context.Context, info *DriverInfoData) error
	// QueryDrivers 查询驾驶员信息列表
	QueryDrivers(ctx context.Context, opts ListOptions) (*ListResult, error)
	// DeleteDriver 删除驾驶员信息
	DeleteDriver(ctx context.Context, id string) error

	// 电子围栏 CRUD
	SaveGeofence(ctx context.Context, g *Geofence) error
	GetGeofence(ctx context.Context, id string) (*Geofence, error)
	ListGeofences(ctx context.Context, opts ListOptions) (*ListResult, error)
	DeleteGeofence(ctx context.Context, id string) error

	// AUTO-FIX-2026-06-29 [P1-6]: 809 转发规则 CRUD（持久化到关系库）
	SaveForwardRule(ctx context.Context, rule *ForwardRule) error
	GetForwardRule(ctx context.Context, id string) (*ForwardRule, error)
	ListForwardRules(ctx context.Context, platformID string) ([]*ForwardRule, error)
	DeleteForwardRule(ctx context.Context, id string) error

	// AUTO-FIX-2026-07-02 [P1]: 809 级联平台配置 CRUD（持久化到关系库）
	// 替代 YAML-only 配置，支持运行时动态增删上下级平台
	SavePlatform(ctx context.Context, platform *Platform) error
	GetPlatform(ctx context.Context, id string) (*Platform, error)
	ListPlatforms(ctx context.Context, role string) ([]*Platform, error)
	DeletePlatform(ctx context.Context, id string) error

	SaveMultimedia(ctx context.Context, media *MultimediaData) error
	// QueryMultimedia 查询音视频/多媒体记录，用于 809 平台查音视频资源目录（0x1B02/0x1B03）。
	// vehicleID 为空时忽略该过滤项；channelID<0 时忽略通道过滤；limit<=0 时默认 100。
	QueryMultimedia(ctx context.Context, vehicleID string, channelID int, start, end time.Time, limit int) ([]*MultimediaData, error)
	SaveCanData(ctx context.Context, can *CanBusData) error
	SaveBDNavData(ctx context.Context, bd *BDNavData) error
	SaveMeterData(ctx context.Context, meter *MeterData) error
	SaveDispatch(ctx context.Context, dispatch *DispatchData) error

	SaveElectronicWaybill(ctx context.Context, wb *ElectronicWaybillData) error
	SaveCommandResp(ctx context.Context, resp *CommandRespData) error
	SaveTerminalProp(ctx context.Context, prop *TerminalPropData) error
	SaveAVParam(ctx context.Context, param *AVParamData) error
	ListTerminalProps(ctx context.Context, opts ListOptions) (*ListResult, error)

	SaveInfoMenuResp(ctx context.Context, resp *InfoMenuRespData) error
	SaveSMSForwardResp(ctx context.Context, resp *SMSForwardRespData) error
	SaveEventResp(ctx context.Context, resp *EventRespData) error

	// AUTO-FIX-2026-06-29: GB/T 32960 电动汽车数据持久化（电池/电机/燃料电池/发动机/极值/充电）
	// SaveEVData 持久化 EV 数据组（整车/电机/燃料电池/发动机/极值/充电/电池单体）
	SaveEVData(ctx context.Context, data *EVData) error
	// QueryEVData 查询指定车辆的 EV 历史数据，dataType 为空时查询全部类型
	QueryEVData(ctx context.Context, vehicleID string, dataType string, start, end time.Time, limit int) ([]*EVData, error)

	// AUTO-FIX-2026-06-26: 第五轮存储修复 - 数据归档/清理
	// CleanupOldLocations 删除指定时间之前的位置数据，返回删除行数。
	CleanupOldLocations(ctx context.Context, before time.Time) (int64, error)
	// CleanupOldAlarms 删除指定时间之前的报警数据，返回删除行数。
	CleanupOldAlarms(ctx context.Context, before time.Time) (int64, error)
	// CleanupOldProtocolLogs 删除指定时间之前的协议日志，返回删除行数。
	CleanupOldProtocolLogs(ctx context.Context, before time.Time) (int64, error)
	// CleanupOldEVData 删除指定时间之前的电动汽车数据，返回删除行数。
	CleanupOldEVData(ctx context.Context, before time.Time) (int64, error)

	Close() error
}
