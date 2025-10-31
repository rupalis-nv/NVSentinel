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

package stringutil

import "testing"

func TestIsTruthyValue(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		// Truthy values
		{name: "true lowercase", value: "true", want: true},
		{name: "true uppercase", value: "TRUE", want: true},
		{name: "true mixed case", value: "TrUe", want: true},
		{name: "enabled lowercase", value: "enabled", want: true},
		{name: "enabled uppercase", value: "ENABLED", want: true},
		{name: "1 string", value: "1", want: true},
		{name: "yes lowercase", value: "yes", want: true},
		{name: "yes uppercase", value: "YES", want: true},

		// With whitespace (should be trimmed)
		{name: "true with leading space", value: " true", want: true},
		{name: "true with trailing space", value: "true ", want: true},
		{name: "true with both spaces", value: " true ", want: true},
		{name: "enabled with tabs", value: "\tenabled\t", want: true},
		{name: "1 with newline", value: "1\n", want: true},

		// Falsy values
		{name: "false", value: "false", want: false},
		{name: "disabled", value: "disabled", want: false},
		{name: "0 string", value: "0", want: false},
		{name: "no", value: "no", want: false},
		{name: "empty string", value: "", want: false},
		{name: "whitespace only", value: "   ", want: false},
		{name: "random string", value: "random", want: false},
		{name: "partial match", value: "truely", want: false},
		{name: "completed", value: "completed", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTruthyValue(tt.value); got != tt.want {
				t.Errorf("IsTruthyValue(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}
