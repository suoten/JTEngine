package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/suoten/jt-engine/internal/util"
	"github.com/suoten/jt-engine/pkg/storage"
)

type MemoryStore struct {
	mu               sync.RWMutex
	vehicles         map[string]*storage.Vehicle
	byPhone          map[string]string
	locations        map[string][]*storage.LocationData
	latest           map[string]*storage.LocationData
	alarms           []*storage.AlarmData
	sessions         map[string]*storage.SessionData
	protoLogs        []*storage.ProtocolLog
	driverInfo       []*storage.DriverInfoData
	geofences        map[string]*storage.Geofence
	multimedia       []*storage.MultimediaData
	canData          []*storage.CanBusData
	bdNavData        []*storage.BDNavData
	meterData        []*storage.MeterData
	dispatchData     []*storage.DispatchData
	waybills         []*storage.ElectronicWaybillData
	commandResps     []*storage.CommandRespData
	terminalProps    []*storage.TerminalPropData
	avParams         []*storage.AVParamData
	infoMenuResps    []*storage.InfoMenuRespData
	smsForwardResps  []*storage.SMSForwardRespData
	eventResps       []*storage.EventRespData
	evData           []*storage.EVData // AUTO-FIX-2026-06-29: GB/T 32960 电动汽车数据
	forwardRules     map[string]*storage.ForwardRule // AUTO-FIX-2026-06-29 [P1-6]: 809 转发规则
	platforms        map[string]*storage.Platform    // AUTO-FIX-2026-07-02 [P1]: 809 级联平台配置
	maxDevices       int
	locationExpiry   time.Duration
	maxLocationCount int
	maxAlarmCount    int
	cleanupInterval  time.Duration
	stopCleanup      chan struct{}
	stopOnce         sync.Once
}

func NewMemoryStore(maxDevices int) *MemoryStore {
	if maxDevices <= 0 {
		maxDevices = 20
	}
	m := &MemoryStore{
		vehicles:         make(map[string]*storage.Vehicle),
		byPhone:          make(map[string]string),
		locations:        make(map[string][]*storage.LocationData),
		latest:           make(map[string]*storage.LocationData),
		sessions:         make(map[string]*storage.SessionData),
		geofences:        make(map[string]*storage.Geofence),
		forwardRules:     make(map[string]*storage.ForwardRule),
		platforms:        make(map[string]*storage.Platform),
		maxDevices:       maxDevices,
		locationExpiry:   7 * 24 * time.Hour,
		maxLocationCount: 1000000,
		maxAlarmCount:    500000,
		cleanupInterval:  6 * time.Hour,
		stopCleanup:      make(chan struct{}),
	}
	util.SafeGo(nil, "storage.memory.cleanupLoop", m.cleanupLoop)
	return m
}

func (m *MemoryStore) cleanupLoop() {
	ticker := time.NewTicker(m.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.cleanupExpiredLocations()
		case <-m.stopCleanup:
			return
		}
	}
}

func (m *MemoryStore) cleanupExpiredLocations() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()

	// 1. 清理过期数据
	totalCount := 0
	for id, locs := range m.locations {
		var kept []*storage.LocationData
		for _, loc := range locs {
			if now.Sub(loc.ReceivedAt) <= m.locationExpiry {
				kept = append(kept, loc)
			}
		}
		m.locations[id] = kept
		totalCount += len(kept)
	}

	// FIXED: [P1-3] 超限时按全局最旧优先驱逐，而非随机 map 遍历
	// 原实现遍历 map（顺序随机），随机丢弃不同车辆的数据，可能导致关键车辆轨迹丢失
	if totalCount > m.maxLocationCount {
		// 收集所有位置点并按时间排序，从最旧的开始驱逐
		type locEntry struct {
			vehicleID string
			idx       int
			loc       *storage.LocationData
		}
		var allEntries []locEntry
		for id, locs := range m.locations {
			for i, loc := range locs {
				allEntries = append(allEntries, locEntry{id, i, loc})
			}
		}
		// 按接收时间从旧到新排序
		sort.Slice(allEntries, func(i, j int) bool {
			return allEntries[i].loc.ReceivedAt.Before(allEntries[j].loc.ReceivedAt)
		})

		excess := totalCount - m.maxLocationCount
		if excess > len(allEntries) {
			excess = len(allEntries)
		}

		// 标记需要删除的条目（按时间最旧优先）
		// 由于逐个删除会影响后续 idx，改为重建各车辆的切片
		toDelete := make(map[string]map[int]bool) // vehicleID -> set of idx to delete
		for i := 0; i < excess; i++ {
			e := allEntries[i]
			if toDelete[e.vehicleID] == nil {
				toDelete[e.vehicleID] = make(map[int]bool)
			}
			toDelete[e.vehicleID][e.idx] = true
		}

		// 重建被影响的车辆的位置切片
		for id, idxs := range toDelete {
			locs := m.locations[id]
			var kept []*storage.LocationData
			for i, loc := range locs {
				if !idxs[i] {
					kept = append(kept, loc)
				}
			}
			if len(kept) == 0 {
				delete(m.locations, id)
			} else {
				m.locations[id] = kept
			}
		}
	}
}

func (m *MemoryStore) StopCleanup() {
	m.stopOnce.Do(func() { close(m.stopCleanup) })
}

func (m *MemoryStore) SaveVehicle(ctx context.Context, vehicle *storage.Vehicle) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.vehicles[vehicle.ID]; !exists {
		onlineCount := 0
		for _, v := range m.vehicles {
			if v.Online {
				onlineCount++
			}
		}
		if onlineCount >= m.maxDevices {
			return fmt.Errorf("device limit reached: max %d devices", m.maxDevices)
		}
	}

	m.vehicles[vehicle.ID] = vehicle
	m.byPhone[vehicle.Phone] = vehicle.ID
	return nil
}

func (m *MemoryStore) GetVehicle(ctx context.Context, id string) (*storage.Vehicle, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.vehicles[id]
	if !ok {
		return nil, fmt.Errorf("vehicle not found: %s", id)
	}
	return v, nil
}

func (m *MemoryStore) GetVehicleByPhone(ctx context.Context, phone string) (*storage.Vehicle, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id, ok := m.byPhone[phone]
	if !ok {
		return nil, fmt.Errorf("vehicle not found by phone: %s", phone)
	}
	return m.vehicles[id], nil
}

func (m *MemoryStore) ListVehicles(ctx context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var items []*storage.Vehicle
	for _, v := range m.vehicles {
		if opts.Phone != "" && v.Phone != opts.Phone {
			continue
		}
		if opts.Online != nil && v.Online != *opts.Online {
			continue
		}
		items = append(items, v)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].LastActive.After(items[j].LastActive)
	})

	total := int64(len(items))
	page := opts.Page
	if page < 1 {
		page = 1
	}
	pageSize := opts.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(items) {
		start = len(items)
	}
	if end > len(items) {
		end = len(items)
	}

	return &storage.ListResult{
		Items: items[start:end],
		Total: total,
		Page:  page,
		Size:  pageSize,
	}, nil
}

func (m *MemoryStore) DeleteVehicle(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vehicles[id]
	if !ok {
		return fmt.Errorf("vehicle not found: %s", id)
	}
	delete(m.byPhone, v.Phone)
	delete(m.vehicles, id)
	delete(m.locations, id)
	delete(m.latest, id)
	return nil
}

func (m *MemoryStore) UpdateVehicleOnline(ctx context.Context, id string, online bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vehicles[id]
	if !ok {
		return fmt.Errorf("vehicle not found: %s", id)
	}
	v.Online = online
	if online {
		v.LastActive = time.Now()
	}
	return nil
}

func (m *MemoryStore) SaveLocation(ctx context.Context, loc *storage.LocationData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.locations[loc.VehicleID] = append(m.locations[loc.VehicleID], loc)
	m.latest[loc.VehicleID] = loc

	const maxLocationsPerVehicle = 10000
	if len(m.locations[loc.VehicleID]) > maxLocationsPerVehicle {
		m.locations[loc.VehicleID] = m.locations[loc.VehicleID][len(m.locations[loc.VehicleID])-maxLocationsPerVehicle:]
	}

	totalCount := 0
	for _, locs := range m.locations {
		totalCount += len(locs)
	}
	if totalCount > m.maxLocationCount {
		excess := totalCount - m.maxLocationCount
		for id, locs := range m.locations {
			if excess <= 0 {
				break
			}
			if len(locs) > 0 {
				remove := excess
				if remove > len(locs) {
					remove = len(locs)
				}
				m.locations[id] = locs[remove:]
				excess -= remove
			}
		}
	}

	return nil
}

func (m *MemoryStore) GetLatestLocation(ctx context.Context, vehicleID string) (*storage.LocationData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	loc, ok := m.latest[vehicleID]
	if !ok {
		return nil, fmt.Errorf("location not found for vehicle: %s", vehicleID)
	}
	return loc, nil
}

func (m *MemoryStore) GetLocationTrack(ctx context.Context, vehicleID string, start, end time.Time) ([]*storage.LocationData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	locations, ok := m.locations[vehicleID]
	if !ok {
		return []*storage.LocationData{}, nil
	}
	var result []*storage.LocationData
	for _, loc := range locations {
		if (loc.Time.Equal(start) || loc.Time.After(start)) && (loc.Time.Equal(end) || loc.Time.Before(end)) {
			result = append(result, loc)
		}
	}
	return result, nil
}

func (m *MemoryStore) SaveAlarm(ctx context.Context, alarm *storage.AlarmData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alarms = append(m.alarms, alarm)
	if len(m.alarms) > m.maxAlarmCount {
		m.alarms = m.alarms[len(m.alarms)-m.maxAlarmCount:]
	}
	return nil
}

// UpdateAlarm 按 ID 更新报警数据（用于 AI 过滤结果回写）
func (m *MemoryStore) UpdateAlarm(ctx context.Context, alarm *storage.AlarmData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, a := range m.alarms {
		if a.ID == alarm.ID {
			m.alarms[i] = alarm
			return nil
		}
	}
	return nil
}

func (m *MemoryStore) ListAlarms(ctx context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var items []*storage.AlarmData
	for _, a := range m.alarms {
		if opts.Phone != "" && a.Phone != opts.Phone {
			continue
		}
		// FIXED-2026-07-17 [P0]: 支持按报警 ID 精确查询
		if opts.AlarmID != "" && a.ID != opts.AlarmID {
			continue
		}
		items = append(items, a)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].ReceivedAt.After(items[j].ReceivedAt)
	})

	total := int64(len(items))
	page := opts.Page
	if page < 1 {
		page = 1
	}
	pageSize := opts.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(items) {
		start = len(items)
	}
	if end > len(items) {
		end = len(items)
	}

	return &storage.ListResult{
		Items: items[start:end],
		Total: total,
		Page:  page,
		Size:  pageSize,
	}, nil
}

func (m *MemoryStore) SaveSession(ctx context.Context, session *storage.SessionData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.ID] = session
	return nil
}

func (m *MemoryStore) GetSession(ctx context.Context, id string) (*storage.SessionData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	return s, nil
}

func (m *MemoryStore) ListSessions(ctx context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var items []*storage.SessionData
	for _, s := range m.sessions {
		items = append(items, s)
	}

	total := int64(len(items))
	page := opts.Page
	if page < 1 {
		page = 1
	}
	pageSize := opts.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(items) {
		start = len(items)
	}
	if end > len(items) {
		end = len(items)
	}

	return &storage.ListResult{
		Items: items[start:end],
		Total: total,
		Page:  page,
		Size:  pageSize,
	}, nil
}

func (m *MemoryStore) DeleteSession(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
	return nil
}

func (m *MemoryStore) GetOnlineCount(ctx context.Context) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := int64(0)
	for _, v := range m.vehicles {
		if v.Online {
			count++
		}
	}
	return count, nil
}

func (m *MemoryStore) GetAlarmCount(ctx context.Context, start, end time.Time) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := int64(0)
	for _, a := range m.alarms {
		if (a.ReceivedAt.Equal(start) || a.ReceivedAt.After(start)) && (a.ReceivedAt.Equal(end) || a.ReceivedAt.Before(end)) {
			count++
		}
	}
	return count, nil
}

func (m *MemoryStore) GetAlarmCountBySource(ctx context.Context, source string, start, end time.Time) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := int64(0)
	for _, a := range m.alarms {
		if a.Source == source && (a.ReceivedAt.Equal(start) || a.ReceivedAt.After(start)) && (a.ReceivedAt.Equal(end) || a.ReceivedAt.Before(end)) {
			count++
		}
	}
	return count, nil
}

func (m *MemoryStore) ListOnlineLocations(ctx context.Context) ([]*storage.LocationData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*storage.LocationData
	for id, v := range m.vehicles {
		if v.Online {
			if loc, ok := m.latest[id]; ok {
				result = append(result, loc)
			}
		}
	}
	return result, nil
}

func (m *MemoryStore) SaveProtocolLog(ctx context.Context, log *storage.ProtocolLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.protoLogs = append(m.protoLogs, log)
	const maxProtoLogs = 50000
	if len(m.protoLogs) > maxProtoLogs {
		m.protoLogs = m.protoLogs[len(m.protoLogs)-maxProtoLogs:]
	}
	return nil
}

func (m *MemoryStore) ListProtocolLogs(ctx context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var items []*storage.ProtocolLog
	for _, l := range m.protoLogs {
		if opts.Phone != "" && l.Phone != opts.Phone {
			continue
		}
		items = append(items, l)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].ReceivedAt.After(items[j].ReceivedAt)
	})

	total := int64(len(items))
	page := opts.Page
	if page < 1 {
		page = 1
	}
	pageSize := opts.PageSize
	if pageSize < 1 {
		pageSize = 50
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(items) {
		start = len(items)
	}
	if end > len(items) {
		end = len(items)
	}

	return &storage.ListResult{
		Items: items[start:end],
		Total: total,
		Page:  page,
		Size:  pageSize,
	}, nil
}

func (m *MemoryStore) Close() error {
	return nil
}

// AUTO-FIX-2026-06-26: 第五轮存储修复 - 数据归档/清理方法（内存版）
func (m *MemoryStore) CleanupOldLocations(ctx context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var deleted int64
	for vid, locs := range m.locations {
		filtered := locs[:0]
		for _, loc := range locs {
			if loc.ReceivedAt.Before(before) {
				deleted++
			} else {
				filtered = append(filtered, loc)
			}
		}
		m.locations[vid] = filtered
	}
	return deleted, nil
}

func (m *MemoryStore) CleanupOldAlarms(ctx context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	filtered := m.alarms[:0]
	var deleted int64
	for _, alarm := range m.alarms {
		if alarm.ReceivedAt.Before(before) {
			deleted++
		} else {
			filtered = append(filtered, alarm)
		}
	}
	m.alarms = filtered
	return deleted, nil
}

func (m *MemoryStore) CleanupOldProtocolLogs(ctx context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	filtered := m.protoLogs[:0]
	var deleted int64
	for _, log := range m.protoLogs {
		if log.ReceivedAt.Before(before) {
			deleted++
		} else {
			filtered = append(filtered, log)
		}
	}
	m.protoLogs = filtered
	return deleted, nil
}

// CleanupOldEVData 删除指定时间之前的电动汽车数据，返回删除行数。
func (m *MemoryStore) CleanupOldEVData(ctx context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	filtered := m.evData[:0]
	var deleted int64
	for _, d := range m.evData {
		if d.ReceivedAt.Before(before) {
			deleted++
		} else {
			filtered = append(filtered, d)
		}
	}
	m.evData = filtered
	return deleted, nil
}

func (m *MemoryStore) GetOfflineCount(ctx context.Context) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := int64(0)
	for _, v := range m.vehicles {
		if !v.Online {
			count++
		}
	}
	return count, nil
}

func (m *MemoryStore) SaveDriverInfo(ctx context.Context, info *storage.DriverInfoData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.driverInfo = append(m.driverInfo, info)
	return nil
}

func (m *MemoryStore) SaveMultimedia(ctx context.Context, media *storage.MultimediaData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.multimedia = append(m.multimedia, media)
	return nil
}

func (m *MemoryStore) QueryMultimedia(ctx context.Context, vehicleID string, channelID int, start, end time.Time, limit int) ([]*storage.MultimediaData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	result := make([]*storage.MultimediaData, 0, limit)
	// 倒序遍历，优先返回最近记录
	for i := len(m.multimedia) - 1; i >= 0 && len(result) < limit; i-- {
		media := m.multimedia[i]
		if vehicleID != "" && media.VehicleID != vehicleID {
			continue
		}
		if channelID >= 0 && media.ChannelID != channelID {
			continue
		}
		if !start.IsZero() && media.ReceivedAt.Before(start) {
			continue
		}
		if !end.IsZero() && media.ReceivedAt.After(end) {
			continue
		}
		result = append(result, media)
	}
	return result, nil
}

func (m *MemoryStore) SaveCanData(ctx context.Context, can *storage.CanBusData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.canData = append(m.canData, can)
	return nil
}

func (m *MemoryStore) SaveBDNavData(ctx context.Context, bd *storage.BDNavData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bdNavData = append(m.bdNavData, bd)
	return nil
}

func (m *MemoryStore) SaveMeterData(ctx context.Context, meter *storage.MeterData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.meterData = append(m.meterData, meter)
	return nil
}

func (m *MemoryStore) SaveDispatch(ctx context.Context, dispatch *storage.DispatchData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dispatchData = append(m.dispatchData, dispatch)
	return nil
}

func (m *MemoryStore) SaveElectronicWaybill(ctx context.Context, wb *storage.ElectronicWaybillData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.waybills = append(m.waybills, wb)
	return nil
}

func (m *MemoryStore) SaveCommandResp(ctx context.Context, resp *storage.CommandRespData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commandResps = append(m.commandResps, resp)
	return nil
}

func (m *MemoryStore) SaveTerminalProp(ctx context.Context, prop *storage.TerminalPropData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.terminalProps = append(m.terminalProps, prop)
	return nil
}

func (m *MemoryStore) SaveAVParam(ctx context.Context, param *storage.AVParamData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.avParams = append(m.avParams, param)
	return nil
}

func (m *MemoryStore) ListTerminalProps(ctx context.Context, opts storage.ListOptions) (*storage.ListResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := make([]*storage.TerminalPropData, len(m.terminalProps))
	copy(items, m.terminalProps)
	return &storage.ListResult{Items: items, Total: int64(len(items))}, nil
}

func (m *MemoryStore) SaveInfoMenuResp(ctx context.Context, resp *storage.InfoMenuRespData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.infoMenuResps = append(m.infoMenuResps, resp)
	return nil
}

func (m *MemoryStore) SaveSMSForwardResp(ctx context.Context, resp *storage.SMSForwardRespData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.smsForwardResps = append(m.smsForwardResps, resp)
	return nil
}

func (m *MemoryStore) BatchSaveLocations(ctx context.Context, locations []*storage.LocationData) error {
	if len(locations) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	const maxLocationsPerVehicle = 10000
	for _, loc := range locations {
		m.locations[loc.VehicleID] = append(m.locations[loc.VehicleID], loc)
		m.latest[loc.VehicleID] = loc
		if len(m.locations[loc.VehicleID]) > maxLocationsPerVehicle {
			m.locations[loc.VehicleID] = m.locations[loc.VehicleID][len(m.locations[loc.VehicleID])-maxLocationsPerVehicle:]
		}
	}
	totalCount := 0
	for _, locs := range m.locations {
		totalCount += len(locs)
	}
	if totalCount > m.maxLocationCount {
		excess := totalCount - m.maxLocationCount
		for id, locs := range m.locations {
			if excess <= 0 {
				break
			}
			if len(locs) > 0 {
				remove := excess
				if remove > len(locs) {
					remove = len(locs)
				}
				m.locations[id] = locs[remove:]
				excess -= remove
			}
		}
	}
	return nil
}

func (m *MemoryStore) BatchSaveAlarms(ctx context.Context, alarms []*storage.AlarmData) error {
	if len(alarms) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alarms = append(m.alarms, alarms...)
	if len(m.alarms) > m.maxAlarmCount {
		m.alarms = m.alarms[len(m.alarms)-m.maxAlarmCount:]
	}
	return nil
}

func (m *MemoryStore) BatchSaveProtocolLogs(ctx context.Context, logs []*storage.ProtocolLog) error {
	if len(logs) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.protoLogs = append(m.protoLogs, logs...)
	const maxProtoLogs = 50000
	if len(m.protoLogs) > maxProtoLogs {
		m.protoLogs = m.protoLogs[len(m.protoLogs)-maxProtoLogs:]
	}
	return nil
}

func (m *MemoryStore) SaveEventResp(ctx context.Context, resp *storage.EventRespData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.eventResps = append(m.eventResps, resp)
	return nil
}

// AUTO-FIX-2026-06-29: GB/T 32960 电动汽车数据持久化
func (m *MemoryStore) SaveEVData(ctx context.Context, data *storage.EVData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.evData = append(m.evData, data)
	return nil
}

func (m *MemoryStore) QueryEVData(ctx context.Context, vehicleID string, dataType string, start, end time.Time, limit int) ([]*storage.EVData, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	var result []*storage.EVData
	for _, d := range m.evData {
		if d.VehicleID != vehicleID {
			continue
		}
		if dataType != "" && d.DataType != dataType {
			continue
		}
		if !d.ReceivedAt.Before(end) || d.ReceivedAt.Before(start) {
			continue
		}
		result = append(result, d)
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}
