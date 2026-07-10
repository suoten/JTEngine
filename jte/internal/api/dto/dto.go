package dto

import "time"

type VehicleDTO struct {
	ID           string `json:"id"`
	Phone        string `json:"phone"`
	Protocol     string `json:"protocol"`
	PlateNo      string `json:"plate_no"`
	PlateColor   int    `json:"plate_color"`
	TerminalID   string `json:"terminal_id"`
	TerminalType string `json:"terminal_type"`
	Manufacturer string `json:"manufacturer"`
	ProvinceID   int    `json:"province_id"`
	CityID       int    `json:"city_id"`
	Online       bool   `json:"online"`
	RegisteredAt string `json:"registered_at"`
	LastActive   string `json:"last_active"`
}

type LocationDTO struct {
	VehicleID  string  `json:"vehicle_id"`
	Phone      string  `json:"phone"`
	Latitude   float64 `json:"latitude"`
	Longitude  float64 `json:"longitude"`
	Altitude   float64 `json:"altitude"`
	Speed      float64 `json:"speed"`
	Direction  int     `json:"direction"`
	Time       string  `json:"time,omitempty"`
	AlarmFlag  uint32  `json:"alarm_flag"`
	StatusFlag uint32  `json:"status_flag"`
	Mileage    float64 `json:"mileage,omitempty"`
	Fuel       float64 `json:"fuel,omitempty"`
	ReceivedAt string  `json:"received_at"`
	Source     string  `json:"source"`
}

type AlarmDTO struct {
	ID         string  `json:"id"`
	VehicleID  string  `json:"vehicle_id"`
	Phone      string  `json:"phone"`
	Type       string  `json:"type"`
	Level      int     `json:"level"`
	AlarmFlag  uint32  `json:"alarm_flag"`
	Latitude   float64 `json:"latitude"`
	Longitude  float64 `json:"longitude"`
	Altitude   float64 `json:"altitude"`
	Speed      float64 `json:"speed"`
	Direction  int     `json:"direction"`
	Time       string  `json:"time,omitempty"`
	ReceivedAt string  `json:"received_at"`
	Source     string  `json:"source"`
}

type SessionDTO struct {
	ID           string `json:"id"`
	Phone        string `json:"phone"`
	Protocol     string `json:"protocol"`
	RemoteAddr   string `json:"remote_addr"`
	Status       string `json:"status"`
	RegisteredAt string `json:"registered_at"`
	LastActive   string `json:"last_active"`
}

type ListQueryDTO struct {
	Page     int    `form:"page" json:"page"`
	PageSize int    `form:"page_size" json:"page_size"`
	Phone    string `form:"phone" json:"phone,omitempty"`
	Online   *bool  `form:"online" json:"online,omitempty"`
	Start    string `form:"start" json:"start,omitempty"`
	End      string `form:"end" json:"end,omitempty"`
	OrderBy  string `form:"order_by" json:"order_by,omitempty"`
}

type ListResultDTO struct {
	Items interface{} `json:"items"`
	Total int64       `json:"total"`
	Page  int         `json:"page"`
	Size  int         `json:"size"`
}

type CommandDTO struct {
	Phone  string                 `json:"phone"`
	MsgID  uint16                 `json:"msg_id"`
	Params map[string]interface{} `json:"params,omitempty"`
}

type StreamDTO struct {
	Phone       string `json:"phone"`
	Channel     byte   `json:"channel"`
	StreamType  byte   `json:"stream_type"`
	MediaType   byte   `json:"media_type"`
	Protocol    string `json:"protocol"`
}

type StreamResponseDTO struct {
	Phone    string `json:"phone"`
	Channel  byte   `json:"channel"`
	URL      string `json:"url"`
	Protocol string `json:"protocol"`
}

type StatsDTO struct {
	OnlineCount  int64     `json:"online_count"`
	AlarmCount   int64     `json:"alarm_count"`
	TotalVehicles int64    `json:"total_vehicles"`
	Timestamp    time.Time `json:"timestamp"`
}

type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type SuccessResponse struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}