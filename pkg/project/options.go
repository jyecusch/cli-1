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

package project

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pterm/pterm"

	"github.com/nitrictech/cli/pkg/runtime"
	"github.com/nitrictech/cli/pkg/utils"

	"github.com/docker/distribution/reference"
)

var stackPath string

func FromConfig(c *Config, isStart bool) (*Project, error) {
	p := New(c.BaseConfig)

	for _, h := range c.ConcreteHandlers {
		maybeFile := filepath.Join(p.Dir, h.Match)

		if _, err := os.Stat(maybeFile); err != nil {
			fs, err := utils.GlobInDir(stackPath, h.Match)
			if err != nil {
				return nil, err
			}

			for _, f := range fs {
				fn, err := FunctionFromHandler(f, h.Type, isStart)
				if err != nil {
					return nil, err
				}

				p.Functions[fn.Name] = fn
			}
		} else {
			fn, err := FunctionFromHandler(h.Match, h.Type, isStart)
			if err != nil {
				return nil, err
			}

			p.Functions[fn.Name] = fn
		}
	}

	if len(p.Functions) == 0 {
		return nil, fmt.Errorf("no functions were found within match on handlers '%+v' in dir '%s', try a new pattern", c.ConcreteHandlers, p.Dir)
	}

	return p, nil
}

func FunctionFromHandler(h string, t string, isStart bool) (Function, error) {
	_, err := reference.Parse(filepath.Base(h))
	if err != nil {
		return Function{}, fmt.Errorf("handler filepath \"%s\" is invalid, must be valid ASCII containing lowercase and uppercase letters, digits, underscores, periods and hyphens", h)
	}

	pterm.Debug.Println("Using function from " + h)

	rt, err := runtime.NewRunTimeFromHandler(h, isStart)
	if err != nil {
		return Function{}, err
	}

	return Function{
		ComputeUnit: ComputeUnit{
			Name: rt.ContainerName(),
			Type: t,
		},
		Handler: h,
	}, nil
}
