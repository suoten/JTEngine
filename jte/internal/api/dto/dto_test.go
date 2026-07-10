package dto

import (
	"encoding/json"
	"testing"
)

func TestVehicleDTOJSON(t *testing.T) {
	v := VehicleDTO{
		ID:           "dev-001",
		Phone:        "13800138000",
		Protocol:     "jt808",
		PlateNo:      "京A12345",
		PlateColor:   1,
		TerminalID:   "T001",
		TerminalType: "PDA",
		Manufacturer: "Test",
		ProvinceID:   11,
		CityID:       1101,
		Online:       true,
		RegisteredAt: "2024-01-01 10:00:00",
		LastActive:   "2024-01-01 12:00:00",
	}

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var loaded VehicleDTO
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if loaded.ID != "dev-001" {
		t.Errorf("ID = %q", loaded.ID)
	}
	if loaded.Phone != "13800138000" {
		t.Errorf("Phone = %q", loaded.Phone)
	}
	if !loaded.Online {
		t.Error("Online should be true")
	}
}

func TestLocationDTOJSON(t *testing.T) {
	loc := LocationDTO{
		VehicleID:  "dev-001",
		Phone:      "13800138000",
		Latitude:   39.9042,
		Longitude:  116.4074,
		Altitude:   50.5,
		Speed:      60.5,
		Direction:  180,
		Time:       "2024-01-01 10:00:00",
		AlarmFlag:   0,
		StatusFlag:  1,
		Mileage:    12345.6,
		Fuel:       80.5,
		ReceivedAt: "2024-01-01 10:00:01",
		Source:     "jt808",
	}

	data, err := json.Marshal(loc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var loaded LocationDTO
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if loaded.Latitude != 39.9042 {
		t.Errorf("Latitude = %f", loaded.Latitude)
	}
	if loaded.Longitude != 116.4074 {
		t.Errorf("Longitude = %f", loaded.Longitude)
	}
	if loaded.Source != "jt808" {
		t.Errorf("Source = %q", loaded.Source)
	}
}

func TestAlarmDTOJSON(t *testing.T) {
	alarm := AlarmDTO{
		ID:         "alarm-001",
		VehicleID:  "dev-001",
		Phone:      "13800138000",
		Type:       "overspeed",
		Level:      2,
		AlarmFlag:  0x01,
		Latitude:   39.9042,
		Longitude:  116.4074,
		Speed:      120.5,
		Direction:  90,
		Time:       "2024-01-01 10:00:00",
		ReceivedAt: "2024-01-01 10:00:01",
		Source:     "jt808",
	}

	data, err := json.Marshal(alarm)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var loaded AlarmDTO
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if loaded.Type != "overspeed" {
		t.Errorf("Type = %q", loaded.Type)
	}
	if loaded.Level != 2 {
		t.Errorf("Level = %d", loaded.Level)
	}
}

func TestSessionDTOJSON(t *testing.T) {
	sess := SessionDTO{
		ID:           "sess-001",
		Phone:        "13800138000",
		Protocol:     "jt808",
		RemoteAddr:   "192.168.1.100:5000",
		Status:       "online",
		RegisteredAt: "2024-01-01 10:00:00",
		LastActive:   "2024-01-01 12:00:00",
	}

	data, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var loaded SessionDTO
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if loaded.Status != "online" {
		t.Errorf("Status = %q", loaded.Status)
	}
}

func TestListQueryDTO(t *testing.T) {
	q := ListQueryDTO{
		Page:     1,
		PageSize: 20,
		Phone:    "13800138000",
	}

	data, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var loaded ListQueryDTO
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if loaded.Page != 1 {
		t.Errorf("Page = %d", loaded.Page)
	}
	if loaded.PageSize != 20 {
		t.Errorf("PageSize = %d", loaded.PageSize)
	}
}

func TestListResultDTO(t *testing.T) {
	result := ListResultDTO{
		Items: []VehicleDTO{{ID: "dev-001"}},
		Total: 100,
		Page:  1,
		Size:  20,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var loaded ListResultDTO
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if loaded.Total != 100 {
		t.Errorf("Total = %d", loaded.Total)
	}
	if loaded.Page != 1 {
		t.Errorf("Page = %d", loaded.Page)
	}
}

func TestCommandDTOJSON(t *testing.T) {
	cmd := CommandDTO{
		Phone: "13800138000",
		MsgID: 0x8103,
		Params: map[string]interface{}{
			"key": "value",
			"num": 123,
		},
	}

	data, err := json.Marshal(cmd)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var loaded CommandDTO
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if loaded.Phone != "13800138000" {
		t.Errorf("Phone = %q", loaded.Phone)
	}
	if loaded.MsgID != 0x8103 {
		t.Errorf("MsgID = %d", loaded.MsgID)
	}
}

func TestStreamDTOJSON(t *testing.T) {
	s := StreamDTO{
		Phone:      "13800138000",
		Channel:    1,
		StreamType: 0,
		MediaType:  0,
		Protocol:   "rtsp",
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var loaded StreamDTO
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if loaded.Protocol != "rtsp" {
		t.Errorf("Protocol = %q", loaded.Protocol)
	}
	if loaded.Channel != 1 {
		t.Errorf("Channel = %d", loaded.Channel)
	}
}

func TestErrorResponseJSON(t *testing.T) {
	e := ErrorResponse{
		Code:    400,
		Message: "bad request",
	}

	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var loaded ErrorResponse
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if loaded.Code != 400 {
		t.Errorf("Code = %d", loaded.Code)
	}
}

func TestSuccessResponseJSON(t *testing.T) {
	resp := SuccessResponse{
		Code:    0,
		Message: "success",
		Data:    map[string]interface{}{"id": "123"},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var loaded SuccessResponse
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if loaded.Code != 0 {
		t.Errorf("Code = %d", loaded.Code)
	}
	if loaded.Message != "success" {
		t.Errorf("Message = %q", loaded.Message)
	}
}

func TestStatsDTOJSON(t *testing.T) {
	s := StatsDTO{
		OnlineCount:   50,
		AlarmCount:    5,
		TotalVehicles: 100,
	}

	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var loaded StatsDTO
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if loaded.OnlineCount != 50 {
		t.Errorf("OnlineCount = %d", loaded.OnlineCount)
	}
	if loaded.TotalVehicles != 100 {
		t.Errorf("TotalVehicles = %d", loaded.TotalVehicles)
	}
}
