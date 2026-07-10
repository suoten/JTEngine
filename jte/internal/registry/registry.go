// Package registry re-exports the public pkg/registry types via type aliases.
//
// AUTO-FIX-2026-06-29 [P2]: The FeatureRegistry was originally defined here
// (internal/). External modules (jte-modules/module-*) could not import it due
// to Go's internal package rule, so each defined its own shadow FeatureRegistry
// type — breaking the runtime type assertion app.(JTEApp) in module.Init().
//
// The canonical implementation now lives in github.com/suoten/jt-engine/pkg/registry
// (public, importable by all modules). This file re-exports everything via type
// aliases and var/const re-declarations so existing internal callers
// (internal/gateway, internal/api, cmd/jte, etc.) continue to work unchanged.
// Because type aliases make internal/registry.FeatureRegistry and
// pkg/registry.FeatureRegistry the SAME type, the module type assertions now
// succeed at runtime.
package registry

import (
	pkgregistry "github.com/suoten/jt-engine/pkg/registry"
)

// Type aliases — make internal/registry types identical to pkg/registry types.
type Feature = pkgregistry.Feature
type FeatureRegistry = pkgregistry.FeatureRegistry

// ConfigProvider is re-exported so the main engine can expose it to modules
// via App.GetConfigProvider() without a separate pkg/registry import.
type ConfigProvider = pkgregistry.ConfigProvider

// Var alias — constructor.
var NewFeatureRegistry = pkgregistry.NewFeatureRegistry

// Var alias — free features list.
var FreeFeatures = pkgregistry.FreeFeatures

// Const re-exports — feature identifiers.
const (
	FeatureJT808       = pkgregistry.FeatureJT808
	FeatureJT1078      = pkgregistry.FeatureJT1078
	FeatureHTTPAPI     = pkgregistry.FeatureHTTPAPI
	FeatureWebSocket   = pkgregistry.FeatureWebSocket
	FeatureMemoryStore = pkgregistry.FeatureMemoryStore
	FeatureDashboard   = pkgregistry.FeatureDashboard

	FeatureProtocol809   = pkgregistry.FeatureProtocol809
	FeatureProtocol1045  = pkgregistry.FeatureProtocol1045
	FeatureProtocol905   = pkgregistry.FeatureProtocol905
	FeatureProtocol1253  = pkgregistry.FeatureProtocol1253
	FeatureProtocol32960 = pkgregistry.FeatureProtocol32960
	FeatureDBStorage     = pkgregistry.FeatureDBStorage
	FeatureCrypto        = pkgregistry.FeatureCrypto
	FeatureAdapter       = pkgregistry.FeatureAdapter
	FeatureCluster       = pkgregistry.FeatureCluster
	FeatureMonitor       = pkgregistry.FeatureMonitor
	FeatureLegacy        = pkgregistry.FeatureLegacy
	FeatureAI            = pkgregistry.FeatureAI
	FeatureAINLP         = pkgregistry.FeatureAINLP
	FeatureUnlimited     = pkgregistry.FeatureUnlimited

	FeatureTimeSeriesStorage = pkgregistry.FeatureTimeSeriesStorage
	FeatureCacheStorage      = pkgregistry.FeatureCacheStorage
	FeatureObjectStorage     = pkgregistry.FeatureObjectStorage
)
