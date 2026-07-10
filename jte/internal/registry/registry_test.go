package registry

import "testing"

func TestNewFeatureRegistry(t *testing.T) {
	fr := NewFeatureRegistry()

	for _, f := range FreeFeatures {
		if !fr.HasFeature(f) {
			t.Errorf("expected free feature %s to be enabled", f)
		}
	}
}

func TestFeatureRegistry_Register(t *testing.T) {
	fr := NewFeatureRegistry()

	if fr.HasFeature(FeatureDBStorage) {
		t.Error("expected FeatureDBStorage to be disabled")
	}

	fr.Register(FeatureDBStorage)

	if !fr.HasFeature(FeatureDBStorage) {
		t.Error("expected FeatureDBStorage to be enabled after register")
	}
}

func TestFeatureRegistry_Unregister(t *testing.T) {
	fr := NewFeatureRegistry()

	fr.Unregister(FeatureJT808)

	if fr.HasFeature(FeatureJT808) {
		t.Error("expected FeatureJT808 to be disabled after unregister")
	}
}

func TestFeatureRegistry_ListFeatures(t *testing.T) {
	fr := NewFeatureRegistry()

	features := fr.ListFeatures()
	if len(features) < len(FreeFeatures) {
		t.Errorf("ListFeatures returned %d features, expected at least %d", len(features), len(FreeFeatures))
	}
}