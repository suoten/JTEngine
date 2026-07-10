package handler

// AUTO-FIX-2026-06-26: 第一轮协议完整性审计 - 新增 AlarmFilter 接口
// 用于解耦 1045 报警处理与 module-ai，使 module-protocol-1045 无需直接依赖 module-ai。
// module-ai 的 AIModule 已实现 AnalyzeAlarm 方法，自动满足该接口（Go duck typing）。
type AlarmFilter interface {
	// AnalyzeAlarm 分析报警是否为误报。
	// alarmID: 报警唯一标识；alarmType: 报警类型；data: 报警上下文（speed/vehicle_id 等）。
	// 返回: isFalseAlarm 是否误报, confidence 置信度[0,1], reason 判定原因, err 错误。
	AnalyzeAlarm(alarmID, alarmType string, data map[string]interface{}) (isFalseAlarm bool, confidence float64, reason string, err error)
}

// AlarmFilterSetter 可选接口：支持注入 AI 报警过滤器的协议 Handler 应实现此接口。
// 宿主在所有模块启动完成后，通过类型断言调用 SetAlarmFilter 完成注入，
// 避免依赖模块初始化顺序（map 遍历顺序不确定）。
type AlarmFilterSetter interface {
	SetAlarmFilter(AlarmFilter)
}

// AUTO-FIX-2026-06-28: 1045 报警接入 AI 过滤链路 - 责任边界详细结果
// AlarmAnalysisDetail 镜像 module-ai/engine.AlarmAnalysisResult 的关键字段，
// 在 pkg/handler 内独立定义以避免循环依赖（pkg/handler 不能导入 module-ai/engine）。
// 由 AIModule.AnalyzeAlarmWithReview 填充并返回，供 1045 handler 回写存储。
type AlarmAnalysisDetail struct {
	IsFalseAlarm        bool    `json:"is_false_alarm"`         // 最终判定是否为误报（安全类报警即使 AI 判定误报也会被强制改写为 false）
	AISuspectedFalse    bool    `json:"ai_suspected_false"`     // AI 原始判定（仅作为"疑似误报"标记）
	Confidence          float64 `json:"confidence"`             // AI 置信度[0,1]
	Reason              string  `json:"reason"`                 // 决策原因
	Source              string  `json:"source"`                 // 来源（rule_engine/deepseek/onnx/...）
	RequireManualReview bool    `json:"require_manual_review"`  // 是否需要人工复核（安全类报警必须为 true）
	AuditTrail          string  `json:"audit_trail"`            // 审计日志（AI 决策依据）
	ModelVersion        string  `json:"model_version"`          // 使用的模型版本
	AlarmTypeCode       int     `json:"alarm_type_code"`        // 报警类型数值编码
}

// AlarmDetailedFilter 可选接口：扩展 AlarmFilter，返回含责任边界字段的详细分析结果。
// module-ai 的 AIModule 通过新增 AnalyzeAlarmWithReview 方法自动满足此接口（Go duck typing）。
// 1045 handler 在 filterAlarm 中优先类型断言此接口以获取 RequireManualReview 标记，
// 若注入的 AlarmFilter 未实现此接口则回退到 AnalyzeAlarm（保持向后兼容）。
type AlarmDetailedFilter interface {
	// AnalyzeAlarmWithReview 返回含责任边界字段的详细分析结果。
	// 方法名故意区别于 engine.AlarmAnalysisResult.AnalyzeAlarmDetailed 以避免命名冲突。
	AnalyzeAlarmWithReview(alarmID, alarmType string, data map[string]interface{}) (*AlarmAnalysisDetail, error)
}
