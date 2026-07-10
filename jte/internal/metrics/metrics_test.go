package metrics

import (
	"strings"
	"testing"
)

func TestCounter_Inc(t *testing.T) {
	c := NewCounter("test_counter_inc_total", "test counter")
	c.Inc()
	c.Inc()
	c.Inc()
	if c.Value() != 3 {
		t.Errorf("expected value 3, got %g", c.Value())
	}
}

func TestCounter_Add(t *testing.T) {
	c := NewCounter("test_counter_add_total", "test counter")
	c.Add(5)
	c.Add(3)
	if c.Value() != 8 {
		t.Errorf("expected value 8, got %g", c.Value())
	}
}

func TestCounter_WithLabels(t *testing.T) {
	c := NewCounter("test_counter_labeled_total", "test counter")
	c.IncWithLabels(map[string]string{"protocol": "jt808"})
	c.IncWithLabels(map[string]string{"protocol": "jt808"})
	c.IncWithLabels(map[string]string{"protocol": "jt809"})

	var sb strings.Builder
	c.WritePrometheus(&sb)
	output := sb.String()

	if !strings.Contains(output, `test_counter_labeled_total{protocol="jt808"} 2`) {
		t.Errorf("expected jt808 label count 2 in output:\n%s", output)
	}
	if !strings.Contains(output, `test_counter_labeled_total{protocol="jt809"} 1`) {
		t.Errorf("expected jt809 label count 1 in output:\n%s", output)
	}
}

func TestGauge_Set(t *testing.T) {
	g := NewGauge("test_gauge_set", "test gauge")
	g.Set(42)
	g.Set(100)
	// 只有无标签的最新值
	var sb strings.Builder
	g.WritePrometheus(&sb)
	output := sb.String()
	if !strings.Contains(output, "test_gauge_set 100") {
		t.Errorf("expected gauge value 100:\n%s", output)
	}
}

func TestGauge_AddDec(t *testing.T) {
	g := NewGauge("test_gauge_adddec", "test gauge")
	g.Set(10)
	g.Inc()
	g.Add(5)
	g.Dec()
	var sb strings.Builder
	g.WritePrometheus(&sb)
	output := sb.String()
	// 10 + 1 + 5 - 1 = 15
	if !strings.Contains(output, "test_gauge_adddec 15") {
		t.Errorf("expected gauge value 15:\n%s", output)
	}
}

func TestHistogram_Observe(t *testing.T) {
	h := NewHistogram("test_histogram", "test histogram",
		[]float64{0.01, 0.1, 1.0})
	h.Observe(0.005)
	h.Observe(0.05)
	h.Observe(0.5)
	h.Observe(2.0)

	var sb strings.Builder
	h.WritePrometheus(&sb)
	output := sb.String()

	if !strings.Contains(output, `test_histogram_bucket{le="0.01"} 1`) {
		t.Errorf("expected bucket le=0.01 count 1:\n%s", output)
	}
	if !strings.Contains(output, `test_histogram_bucket{le="0.1"} 2`) {
		t.Errorf("expected bucket le=0.1 count 2:\n%s", output)
	}
	if !strings.Contains(output, `test_histogram_bucket{le="1"} 3`) {
		t.Errorf("expected bucket le=1 count 3:\n%s", output)
	}
	if !strings.Contains(output, `test_histogram_bucket{le="+Inf"} 4`) {
		t.Errorf("expected bucket le=+Inf count 4:\n%s", output)
	}
	if !strings.Contains(output, "test_histogram_count 4") {
		t.Errorf("expected count 4:\n%s", output)
	}
}

func TestCollector_WritePrometheus(t *testing.T) {
	// 默认收集器应包含预定义指标
	c := DefaultCollector()
	var sb strings.Builder
	c.WritePrometheus(&sb)
	output := sb.String()

	// 验证预定义指标存在
	expectedMetrics := []string{
		"jte_connections_total",
		"jte_messages_total",
		"jte_storage_write_total",
		"jte_video_bitrate",
		"jte_online_devices",
	}
	for _, m := range expectedMetrics {
		if !strings.Contains(output, m) {
			t.Errorf("expected metric %s in output", m)
		}
	}
}

func TestWritePrometheus_Format(t *testing.T) {
	c := NewCounter("test_format_total", "format test")
	c.Inc()
	var sb strings.Builder
	c.WritePrometheus(&sb)
	output := sb.String()

	// 验证 Prometheus text format 格式
	if !strings.HasPrefix(output, "# HELP test_format_total format test\n") {
		t.Errorf("missing HELP line:\n%s", output)
	}
	if !strings.Contains(output, "# TYPE test_format_total counter\n") {
		t.Errorf("missing TYPE line:\n%s", output)
	}
}

func TestLabelKey_Sorting(t *testing.T) {
	c := NewCounter("test_sort_total", "test")
	c.IncWithLabels(map[string]string{"b": "2", "a": "1"})
	var sb strings.Builder
	c.WritePrometheus(&sb)
	output := sb.String()

	// 标签应按字母序排列：a="1",b="2"
	if !strings.Contains(output, `a="1",b="2"`) {
		t.Errorf("expected sorted labels:\n%s", output)
	}
}

func TestEscapeLabelValue(t *testing.T) {
	c := NewCounter("test_escape_total", "test")
	c.IncWithLabels(map[string]string{"path": `hello"world\n`})
	var sb strings.Builder
	c.WritePrometheus(&sb)
	output := sb.String()

	// 引号和反斜杠应被转义
	if !strings.Contains(output, `path="hello\"world\\n"`) {
		t.Errorf("expected escaped label value:\n%s", output)
	}
}
