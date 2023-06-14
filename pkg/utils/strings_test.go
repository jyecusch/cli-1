// Copyright Nitric Pty Ltd.
//
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package utils

import (
	"testing"
)

func TestStringTrunc(t *testing.T) {
	tests := []struct {
		name string
		s    string
		max  int
		want string
	}{
		{
			name: "less than",
			s:    "1234567890",
			max:  20,
			want: "1234567890",
		},
		{
			name: "max len",
			s:    "1234567890",
			max:  10,
			want: "1234567890",
		},
		{
			name: "trunc",
			s:    "1234567890",
			max:  7,
			want: "1234567",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StringTrunc(tt.s, tt.max); got != tt.want {
				t.Errorf("StringTrunc() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatProjectName(t *testing.T) {
	tests := []struct {
		projectName string
		want        string
	}{
		{
			projectName: "camelCase",
			want:        "camel-case",
		},
		{
			projectName: "PascalCase",
			want:        "pascal-case",
		},
		{
			projectName: "ALLCAPS",
			want:        "allcaps",
		},
		{
			projectName: "TeStInG",
			want:        "te-st-in-g",
		},
		{
			projectName: "kebab-case",
			want:        "kebab-case",
		},
		{
			projectName: "Sentence case",
			want:        "sentence-case",
		},
	}

	for _, tt := range tests {
		t.Run(tt.projectName, func(t *testing.T) {
			if got := FormatProjectName(tt.projectName); got != tt.want {
				t.Errorf("StringTrunc() = %v, want %v", got, tt.want)
			}
		})
	}
}
