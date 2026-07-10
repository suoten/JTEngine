package masking

import "testing"

func TestMaskPhone(t *testing.T) {
	cases := []struct {
		input, expected string
	}{
		{"13800138000", "138****8000"},
		{"+8613800138000", "138****8000"},
		{"8613800138000", "138****8000"},
		{"12345", "*****"},  // 过短，全部脱敏
		{"", ""},
	}
	for _, c := range cases {
		got := MaskPhone(c.input)
		if got != c.expected {
			t.Errorf("MaskPhone(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestMaskIDCard(t *testing.T) {
	cases := []struct {
		input, expected string
	}{
		{"110101199001011234", "110101********1234"},
		{"110101199001011234X", "110101*********234X"}, // 19位带X，末4位=234X
		{"12345", "*****"},  // 过短
		{"", ""},
	}
	for _, c := range cases {
		got := MaskIDCard(c.input)
		if got != c.expected {
			t.Errorf("MaskIDCard(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestMaskPlate(t *testing.T) {
	cases := []struct {
		input, expected string
	}{
		{"京A12345", "京A***45"},
		{"京A8", "京A8"},       // 过短不脱敏
		{"粤B12345", "粤B***45"},
		{"", ""},
	}
	for _, c := range cases {
		got := MaskPlate(c.input)
		if got != c.expected {
			t.Errorf("MaskPlate(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestMaskEmail(t *testing.T) {
	cases := []struct {
		input, expected string
	}{
		{"zhangsan@example.com", "z***@example.com"},
		{"a@b.com", "a***@b.com"},
		{"", ""},
	}
	for _, c := range cases {
		got := MaskEmail(c.input)
		if got != c.expected {
			t.Errorf("MaskEmail(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestMaskName(t *testing.T) {
	cases := []struct {
		input, expected string
	}{
		{"张", "张"},
		{"张三", "张*"},
		{"张三丰", "张*丰"},
		{"诸葛孔明", "诸**明"},
		{"", ""},
	}
	for _, c := range cases {
		got := MaskName(c.input)
		if got != c.expected {
			t.Errorf("MaskName(%q) = %q, want %q", c.input, got, c.expected)
		}
	}
}

func TestIsSensitiveField(t *testing.T) {
	cases := []struct {
		field   string
		isSense bool
		mType   string
	}{
		{"phone", true, "phone"},
		{"Phone", true, "phone"},
		{"mobile", true, "phone"},
		{"telephone", true, "phone"},
		{"id_card", true, "id_card"},
		{"IDCard", true, "id_card"},
		{"id_number", true, "id_card"},
		{"plate", true, "plate"},
		{"license", true, "plate"},
		{"car_no", true, "plate"},
		{"email", true, "email"},
		{"mail", true, "email"},
		{"name", true, "name"},
		{"driver_name", true, "name"},
		{"owner_name", true, "name"},
		{"username", false, ""},  // username 不脱敏（登录名）
		{"address", false, ""},
		{"", false, ""},
	}
	for _, c := range cases {
		isSense, mType := IsSensitiveField(c.field)
		if isSense != c.isSense || mType != c.mType {
			t.Errorf("IsSensitiveField(%q) = (%v, %q), want (%v, %q)",
				c.field, isSense, mType, c.isSense, c.mType)
		}
	}
}

func TestMaskByType(t *testing.T) {
	if MaskByType("phone", "13800138000") != "138****8000" {
		t.Error("MaskByType phone failed")
	}
	if MaskByType("id_card", "110101199001011234") != "110101********1234" {
		t.Error("MaskByType id_card failed")
	}
	if MaskByType("unknown", "test") != "test" {
		t.Error("MaskByType unknown should return as-is")
	}
}
