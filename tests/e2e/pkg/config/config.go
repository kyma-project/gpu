/*
Copyright 2026 SAP SE or an SAP affiliate company and gpu contributors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package config provides environment-driven settings for the e2e suite.
package config

import (
	"os"
	"time"
)

type Config struct {
	GpuOperatorNamespace string
	TestTimeout          time.Duration
	SkipCleanup          bool
}

func Get() *Config {
	return &Config{
		GpuOperatorNamespace: getEnvOrDefault("GPU_OPERATOR_NAMESPACE", "gpu-operator"),
		TestTimeout:          getEnvAsDuration("TEST_TIMEOUT", 15*time.Minute),
		SkipCleanup:          getEnvAsBool("SKIP_CLEANUP", false),
	}
}

func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvAsBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v == "true" || v == "1" || v == "yes"
}

func getEnvAsDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
