// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package reconciler

import (
	"strings"

	protos "github.com/nvidia/nvsentinel/data-models/pkg/protos"
	config "github.com/nvidia/nvsentinel/health-events-analyzer/pkg/config"
)

const (
	entityTypeGPUUUID         = "GPU_UUID"
	entityTypeGPU             = "GPU"
	entityTypePCI             = "PCI"
	entityTypeGPC             = "GPC"
	entityTypeTPC             = "TPC"
	entityTypeNVLINK          = "NVLINK"
	entityTypeNIC             = "NIC"
	entityTypeNICPort         = "NICPort"
	entitiesImpactedFieldName = "entitiesimpacted"
)

// metricSafeEntityTypes maps a case-insensitive entity type to the documented
// Prometheus label spelling. GPU UUID is excluded: it is unbounded from
// Prometheus's point of view, and a replaced GPU changes it.
var metricSafeEntityTypes = map[string]string{
	"gpu":     entityTypeGPU,
	"pci":     entityTypePCI,
	"gpc":     entityTypeGPC,
	"tpc":     entityTypeTPC,
	"nvlink":  entityTypeNVLINK,
	"nic":     entityTypeNIC,
	"nicport": entityTypeNICPort,
}

// ruleSelectsOnEntity reports whether the rule's aggregation keys on an
// impacted entity. Node-scoped rules do not mention entitiesimpacted, so they
// do not emit rule_matched_entity_total even when the triggering event happens
// to carry GPU or NIC entities.
func ruleSelectsOnEntity(rule config.HealthEventsAnalyzerRule) bool {
	for _, stage := range rule.Stage {
		if strings.Contains(strings.ToLower(stage), entitiesImpactedFieldName) {
			return true
		}
	}

	return false
}

func canonicalMetricEntityType(entityType string) (string, bool) {
	canonical, ok := metricSafeEntityTypes[strings.ToLower(entityType)]

	return canonical, ok
}

// metricSafeEntities returns the triggering event's impacted entities that are
// safe Prometheus labels: stable slot identity, not GPU UUID, SM, or register
// values. Types are rewritten to the documented canonical spelling so mixed
// case cannot split a series. Duplicates are dropped. The returned entities
// are copies and do not mutate the triggering event.
func metricSafeEntities(event *protos.HealthEvent) []*protos.Entity {
	seen := make(map[string]struct{}, len(event.GetEntitiesImpacted()))
	out := make([]*protos.Entity, 0, len(event.GetEntitiesImpacted()))

	for _, entity := range event.GetEntitiesImpacted() {
		entityType := entity.GetEntityType()
		entityValue := entity.GetEntityValue()

		if entityType == "" || entityValue == "" {
			continue
		}

		if strings.EqualFold(entityType, entityTypeGPUUUID) {
			continue
		}

		canonicalType, ok := canonicalMetricEntityType(entityType)
		if !ok {
			continue
		}

		key := canonicalType + "\x00" + entityValue
		if _, exists := seen[key]; exists {
			continue
		}

		seen[key] = struct{}{}

		out = append(out, &protos.Entity{
			EntityType:  canonicalType,
			EntityValue: entityValue,
		})
	}

	return out
}

func recordMatchedEntityMetric(ruleName, nodeName string, event *protos.HealthEvent) {
	for _, entity := range metricSafeEntities(event) {
		recordRuleMatchedEntity(ruleName, nodeName, entity.GetEntityType(), entity.GetEntityValue())
	}
}

func recordMatchedEntityMetricForRule(rule config.HealthEventsAnalyzerRule, event *protos.HealthEvent) {
	if !ruleSelectsOnEntity(rule) {
		return
	}

	recordMatchedEntityMetric(rule.Name, event.GetNodeName(), event)
}
