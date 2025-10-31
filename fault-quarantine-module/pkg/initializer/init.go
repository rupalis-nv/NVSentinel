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

package initializer

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/nvidia/nvsentinel/commons/pkg/configmanager"
	"github.com/nvidia/nvsentinel/fault-quarantine-module/pkg/breaker"
	"github.com/nvidia/nvsentinel/fault-quarantine-module/pkg/config"
	"github.com/nvidia/nvsentinel/fault-quarantine-module/pkg/informer"
	"github.com/nvidia/nvsentinel/fault-quarantine-module/pkg/mongodb"
	"github.com/nvidia/nvsentinel/fault-quarantine-module/pkg/reconciler"
	"github.com/nvidia/nvsentinel/store-client-sdk/pkg/storewatcher"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type InitializationParams struct {
	KubeconfigPath        string
	TomlConfigPath        string
	DryRun                bool
	CircuitBreakerEnabled bool
}

type Components struct {
	Reconciler     *reconciler.Reconciler
	EventWatcher   *mongodb.EventWatcher
	K8sClient      *informer.FaultQuarantineClient
	CircuitBreaker breaker.CircuitBreaker
}

func InitializeAll(ctx context.Context, params InitializationParams) (*Components, error) {
	slog.Info("Starting fault quarantine module initialization")

	mongoConfig, tokenConfig, err := storewatcher.LoadConfigFromEnv("fault-quarantine-module")
	if err != nil {
		return nil, fmt.Errorf("failed to load MongoDB configuration: %w", err)
	}

	pipeline := createMongoPipeline()

	var tomlCfg config.TomlConfig
	if err := configmanager.LoadTOMLConfig(params.TomlConfigPath, &tomlCfg); err != nil {
		return nil, fmt.Errorf("error while loading the toml config: %w", err)
	}

	if params.DryRun {
		slog.Info("Running in dry-run mode")
	}

	k8sClient, err := informer.NewFaultQuarantineClient(params.KubeconfigPath, params.DryRun, 30*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("error while initializing kubernetes client: %w", err)
	}

	slog.Info("Successfully initialized kubernetes client with embedded node informer")

	var circuitBreaker breaker.CircuitBreaker

	if params.CircuitBreakerEnabled {
		cb, err := initializeCircuitBreaker(
			ctx,
			k8sClient,
			tomlCfg.CircuitBreaker,
		)
		if err != nil {
			return nil, fmt.Errorf("error while initializing circuit breaker: %w", err)
		}

		circuitBreaker = cb

		slog.Info("Successfully initialized circuit breaker")
	} else {
		slog.Info("Circuit breaker is disabled, skipping initialization")
	}

	reconcilerCfg := createReconcilerConfig(
		tomlCfg,
		params.DryRun,
		params.CircuitBreakerEnabled,
	)

	reconcilerInstance := reconciler.NewReconciler(
		reconcilerCfg,
		k8sClient,
		circuitBreaker,
	)

	healthEventCollection, err := initializeMongoCollection(ctx, mongoConfig)
	if err != nil {
		return nil, fmt.Errorf("error while initializing mongo collection: %w", err)
	}

	eventWatcher := mongodb.NewEventWatcher(
		mongoConfig,
		tokenConfig,
		pipeline,
		healthEventCollection,
		reconcilerInstance,
	)

	reconcilerInstance.SetEventWatcher(eventWatcher)

	slog.Info("Initialization completed successfully")

	return &Components{
		Reconciler:     reconcilerInstance,
		EventWatcher:   eventWatcher,
		K8sClient:      k8sClient,
		CircuitBreaker: circuitBreaker,
	}, nil
}

func createMongoPipeline() mongo.Pipeline {
	return mongo.Pipeline{
		bson.D{
			bson.E{Key: "$match", Value: bson.D{
				bson.E{Key: "operationType", Value: bson.D{
					bson.E{Key: "$in", Value: bson.A{"insert"}},
				}},
			}},
		},
	}
}

func createReconcilerConfig(
	tomlCfg config.TomlConfig,
	dryRun bool,
	circuitBreakerEnabled bool,
) reconciler.ReconcilerConfig {
	return reconciler.ReconcilerConfig{
		TomlConfig:            tomlCfg,
		DryRun:                dryRun,
		CircuitBreakerEnabled: circuitBreakerEnabled,
	}
}

func initializeMongoCollection(
	ctx context.Context,
	mongoConfig storewatcher.MongoDBConfig,
) (*mongo.Collection, error) {
	collection, err := storewatcher.GetCollectionClient(ctx, mongoConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get MongoDB collection: %w", err)
	}

	return collection, nil
}

func initializeCircuitBreaker(
	ctx context.Context,
	k8sClient *informer.FaultQuarantineClient,
	cbConfig config.CircuitBreaker,
) (breaker.CircuitBreaker, error) {
	circuitBreakerName := "fault-quarantine-circuit-breaker"

	namespace := os.Getenv("POD_NAMESPACE")

	duration, err := time.ParseDuration(cbConfig.Duration)
	if err != nil {
		return nil, fmt.Errorf("invalid circuit breaker duration %q: %w", cbConfig.Duration, err)
	}

	slog.Info("Initializing circuit breaker",
		"configMap", circuitBreakerName,
		"namespace", namespace,
		"percentage", cbConfig.Percentage,
		"duration", cbConfig.Duration)

	cb, err := breaker.NewSlidingWindowBreaker(ctx, breaker.Config{
		Window:             duration,
		TripPercentage:     float64(cbConfig.Percentage),
		K8sClient:          k8sClient,
		ConfigMapName:      circuitBreakerName,
		ConfigMapNamespace: namespace,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize circuit breaker: %w", err)
	}

	return cb, nil
}
