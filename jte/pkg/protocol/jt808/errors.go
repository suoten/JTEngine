package jt808

import "errors"

var (
	ErrDataTooShort = errors.New("message data too short")
	ErrInvalidChecksum = errors.New("invalid checksum")
	ErrInvalidDelimiter = errors.New("invalid message delimiter")
)

func trimNull(data []byte) string {
	end := len(data)
	for end > 0 && data[end-1] == 0 {
		end--
	}
	return string(data[:end])
}