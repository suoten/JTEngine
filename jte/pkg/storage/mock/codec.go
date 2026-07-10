package mock

import "encoding/json"

// encode 将任意值序列化为 JSON 字节（mock 缓存通用 K/V 用）。
func encode(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// decode 将 JSON 字节反序列化到 out 指针。
func decode(data []byte, out interface{}) error {
	return json.Unmarshal(data, out)
}
