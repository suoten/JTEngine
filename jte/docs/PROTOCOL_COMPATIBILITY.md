# 协议兼容性矩阵

本文档记录 JTE 支持的各协议版本间的消息 ID 字段差异，特别标注已知的非标准实现。

## 目录

- [JT/T 808 协议版本差异](#jtt-808-协议版本差异)
- [JT/T 809 协议版本差异](#jtt-809-协议版本差异)
- [JT/T 1078 RTP 扩展](#jtt-1078-rtp-扩展)
- [GB/T 32960 数据项映射](#gbt-32960-数据项映射)
- [已知非标准实现](#已知非标准实现)

---

## JT/T 808 协议版本差异

### 版本对照表

| 消息 ID | 消息名称 | 2011 | 2013 | 2019 | 差异说明 |
|---------|----------|------|------|------|----------|
| 0x0001 | 终端通用响应 | ✅ | ✅ | ✅ | 无差异 |
| 0x0002 | 终端心跳 | ✅ | ✅ | ✅ | 无差异 |
| 0x0003 | 终端注销 | ✅ | ✅ | ✅ | 无差异 |
| 0x0100 | 终端注册 | ✅ | ✅ | ✅ | 2019 增加协议版本号字段(1B) |
| 0x0102 | 终端鉴权 | ✅ | ✅ | ✅ | 无差异 |
| 0x0200 | 位置信息汇报 | ✅ | ✅ | ✅ | 附加项 ID 定义不同，2019 增加更多扩展项 |
| 0x0201 | 位置查询应答 | ✅ | ✅ | ✅ | 无差异 |
| 0x0704 | 批量位置上报 | ✅ | ✅ | ✅ | 项格式一致，Count 字段为 uint16 |
| 0x8103 | 终端参数设置 | ✅ | ✅ | ✅ | 参数总数为 1B(2019)/2B(2011) |
| 0x8104 | 终端参数查询 | ✅ | ✅ | ✅ | 无差异 |
| 0x0104 | 参数查询应答 | ✅ | ✅ | ✅ | 参数总数为 1B(2019)/2B(2011) |

### 头部结构差异

| 字段 | 2011 | 2013 | 2019 |
|------|------|------|------|
| 消息头长度 | 12B | 12B | 13B（增加协议版本号 1B） |
| 流水号偏移 | offset+10 | offset+10 | offset+11 |
| 协议版本号 | 无 | 无 | data[4] (1B) |

### 位置消息附加项 ID 差异

| 附加项 ID | 2011 | 2013 | 2019 | 说明 |
|-----------|------|------|------|------|
| 0x01 | 里程 | 里程 | 里程 | 单位: 1/10 km |
| 0x02 | 油量 | 油量 | 油量 | 单位: 1/10 L |
| 0x03 | 速度 | 速度 | 速度 | 单位: 1/10 km/h |
| 0x25 | — | — | 道路限速标志 | 2019 新增 |
| 0x2B | — | — | 人工报警确认 | 2019 新增 |
| 0x30~0x3F | — | — | ADAS 扩展 | 2019 新增 |

---

## JT/T 809 协议版本差异

### 头部结构差异

| 字段 | 2011 | 2019 | 说明 |
|------|------|------|------|
| 头部长度 | 22B | 22B | 一致 |
| 协议版本号 | 无 | data[21] 区域中含 | 2019 增加版本探测 |
| 车牌颜色默认值 | 0x01(蓝色) | 调用方指定 | **已修复：不再硬编码默认值** |
| 转义规则 | 0x5B→0x5B 0x01, 0x5D→0x5B 0x02, 0x5E→0x5B 0x03, 0x5D→0x5E 0x01 | 同 | 4 条转义规则 |
| 加密方式 | data[4] | data[4] | 一致 |
| 车牌号编码 | GBK | GBK | 一致 |

### 关键消息 ID

| 消息 ID | 消息名称 | 2011 | 2019 | 差异 |
|---------|----------|------|------|------|
| 0x1001 | 平台登录 | ✅ | ✅ | 2019 增加 RSA/AES 密钥协商 |
| 0x1003 | 车辆新增 | ✅ | ✅ | 字段一致 |
| 0x1200 | 报警 | ✅ | ✅ | 坐标使用 uint32*10^6，负坐标取绝对值 |
| 0x1201 | 报警确认 | ✅ | ✅ | 无差异 |
| 0x1202 | 实时音视频请求 | ✅ | ✅ | 无差异 |
| 0x1205 | 历史音视频查询 | ✅ | ✅ | 无差异 |
| 0x1206 | 音视频控制 | ✅ | ✅ | 无差异 |
| 0x1212 | 路线信息上报 | ✅ | ✅ | RoutePoint 结构一致 |

---

## JT/T 1078 RTP 扩展

### RTP 头部结构

| 字段 | 偏移 | 长度 | 说明 |
|------|------|------|------|
| Version | byte 0 高 2 位 | 2 bit | 固定 2 |
| Padding | byte 0 bit 5 | 1 bit | 1=末尾有填充 |
| Extension | byte 0 bit 4 | 1 bit | 1=有扩展头 |
| CSRC Count | byte 0 低 4 位 | 4 bit | CSRC 标识数量 |
| Marker | byte 1 高 1 位 | 1 bit | 标记位 |
| Payload Type | byte 1 低 7 位 | 7 bit | 负载类型 |
| Seq Num | byte 2-3 | 2B | 序列号 |
| Timestamp | byte 4-7 | 4B | 时间戳 |
| SSRC | byte 8-11 | 4B | 同步源标识 |

### 负载类型常量

| 常量名 | 值 | 说明 |
|--------|-----|------|
| PayloadTypeH264 | 96 | H.264 视频 |
| PayloadTypeH265 | 97 | H.265 视频 |
| PayloadTypeAAC | 98 | AAC 音频 |
| PayloadTypeG711 | 99 | G.711 音频（向后兼容） |
| PayloadTypeG726 | 100 | G.726 音频 |
| PayloadTypeG722 | 101 | G.722 音频 |
| PayloadTypeG723 | 102 | G.723 音频 |

### 1078 头部版本差异

| 字段 | 2016 | 2019 |
|------|------|------|
| 头部长度 | 12B | 13B（增加协议版本号 1B） |
| 序列号偏移 | offset+10 | offset+11 |
| 协议版本号 | 无 | data[4] (1B) |

---

## GB/T 32960 数据项映射

### 数据组类型

| 类型 | 值 | 说明 |
|------|-----|------|
| DataGroupVehicle | 0x01 | 车辆数据 |
| DataGroupMotor | 0x02 | 驱动电机数据 |
| DataGroupFuelCell | 0x03 | 燃料电池数据 |
| DataGroupEngine | 0x04 | 发动机数据 |
| DataGroupPosition | 0x05 | 位置数据 |
| DataGroupExtreme | 0x06 | 极值数据 |
| DataGroupAlarm | 0x07 | 报警数据 |
| DataGroupCharging | 0x08 | 充电数据（Raw 模式，非 DataValue 列表） |

### 位置数据编码差异

| 字段 | GB/T 32960 | JT/T 808 | 说明 |
|------|-----------|----------|------|
| 纬度 | int32 / 10^6 | uint32 / 10^6 (取绝对值) | 32960 使用有符号，808 取绝对值 |
| 经度 | int32 / 10^6 | uint32 / 10^6 (取绝对值) | 同上 |
| 高度 | uint16 (1m) | uint16 (1m) | 一致 |
| 速度 | uint16 (0.1 km/h) | uint16 (0.1 km/h) | 一致 |
| 方向 | uint16 (1°) | uint16 (1°) | 一致 |

### BCD 时间编码

| 协议 | 格式 | 字节 | 说明 |
|------|------|------|------|
| JT/T 808 | YYMMDDhhmmss | 6B | 2 位年（补齐到 12 位数字） |
| JT/T 1078 | YYMMDDhhmmss | 6B | 同 808 |
| JT/T 809 | YYYYMMDDhhmmss | 6B BCD | **14 位输入截断为 12 位** |
| GB/T 32960 | YYMMDDhhmmss | 6B BCD | nibble 校验 (bcdToByteSafe) |

---

## 已知非标准实现

### 1. PassengerCountMessage (0x0A00)

- **非标准消息 ID**：0x0A00 不在 JT/T 808-2019 标准消息表中
- **用途**：乘客计数（部分省级地方标准扩展）
- **字段**：Count(uint16) + Timestamp(BCD 6B)
- **兼容性**：仅在特定省份部署中使用，标准设备不会产生此消息

### 2. FireAreaAlarmMessage (0x9xxx)

- **非标准消息 ID**：0x9xxx 范围用于消防扩展
- **用途**：消防区域报警
- **字段**：AreaID(uint32) + Lat(uint32) + Lon(uint32) + AlarmType(byte)
- **注意**：坐标编码方式与标准 CircularArea 一致

### 3. JT1078 消息 ID 复用 808 帧格式

- 1078 消息 ID 范围 0x9xxx / 0x1200 / 0x1Axx / 0x1Bxx
- 复用 808 帧格式（0x7E 分隔符 + 转义 + XOR 校验）
- 但头部使用 1078 专用 12B/13B 结构（非 808 标准 12B/13B）

### 4. 809 车牌颜色默认值

- **历史问题**：EncodeHeader 在 `PlateColor=0` 时硬编码为 `0x01`（蓝色）
- **已修复**：直接使用 `header.PlateColor`，0 是合法值
- **向后兼容**：调用方需显式设置 `PlateColor` 字段

### 5. 32960 温度探针编码

- **历史问题**：温度字段误用 2 字节/探针，应为 1 字节/探针
- **已修复**：修正为 1 字节/探针，偏移 -50℃
- **影响**：修正前解析后续子系统数据全部偏移

---

## BCD 编码跨协议一致性

所有协议的 BCD 编码函数行为一致：

| 行为 | JT808 `StringToBCD` | JT1078 `stringToBCD` | JT809 `stringToBCD809` | GB32960 `byteToBCD` |
|------|---------------------|----------------------|------------------------|---------------------|
| 过滤非数字字符 | ✅ | ✅ | ✅ | N/A |
| 补零到 12 位 | ✅ | ✅ | ✅ | N/A |
| 截断到 12 位 | ✅ | ✅ | ✅ | N/A |
| 返回 6 字节 BCD | ✅ | ✅ | ✅ | N/A |
| nibble 校验 | N/A | N/A | N/A | ✅ (bcdToByteSafe) |
| 空输入返回 error | ✅ | ✅ | ✅ | N/A |

---

## 内存防护

所有 Unmarshal 方法在声明元素数量时检查上界：

```go
const MaxElementCount = 10000

if count > protocol.MaxElementCount {
    return fmt.Errorf("count %d exceeds max %d", count, protocol.MaxElementCount)
}
```

覆盖的消息：
- CircularAreaSetMessage
- RectAreaSetMessage
- PolygonAreaSetMessage
- RouteSetMessage
- LocationBatchMessage
- CanDataMessage
- CommandMessage
- ParamRespMessage

## Panic 防护

所有协议网关解析方法（`tryParse808`、`tryParse809`、`tryParse1253`、`tryParse32960`、`tryParse1045`）均已添加 `defer recover()` 保护，panic 时记录：
- raw 数据前 256 字节
- 完整 stack trace
- 返回 error 而非崩溃 goroutine
