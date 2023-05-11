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

package build

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pterm/pterm"

	"github.com/nitrictech/cli/pkg/containerengine"
	"github.com/nitrictech/cli/pkg/project"
	"github.com/nitrictech/cli/pkg/runtime"
)

func dynamicDockerfile(dir, name string) (*os.File, error) {
	// create a more stable file name for the hashing
	return os.Create(filepath.Join(dir, fmt.Sprintf("%s.nitric.dynamic.dockerfile", name)))
}

// Build base non-nitric wrapped docker image
// These will also be used for config as code runs
func BuildBaseImages(s *project.Project) error {
	ce, err := containerengine.Discover()
	if err != nil {
		return err
	}

	finalFunctions := make(map[string]project.Function)

	for key, fun := range s.Functions {
		if fun.Image != "" {
			newImageName := fmt.Sprintf("%s-%s", s.Name, fun.Name)

			// tag the name
			err = ce.ImageTag(fun.Image, newImageName)
			if err != nil {
				return err
			}

			finalFunctions[fun.Name] = fun
		} else if fun.Dockerfile != "" {
			pterm.Debug.Println("Building image for dockerfile " + fun.Dockerfile)

			originalImageName := fmt.Sprintf("%s-%s", s.Name, fun.Name)

			if err := ce.Build(fun.Dockerfile, fun.Context, originalImageName, fun.Args, []string{}); err != nil {
				return err
			}

			name, err := ce.TagImageToNitricName(originalImageName, s.Name)
			if err != nil {
				return err
			}

			fun.Name = name
			finalFunctions[name] = fun
		} else {
			rt, err := runtime.NewRunTimeFromHandler(fun.Handler)
			if err != nil {
				return err
			}

			f, err := dynamicDockerfile(s.Dir, fun.Name)
			if err != nil {
				return err
			}

			defer func() {
				f.Close()
				os.Remove(f.Name())
			}()

			if err := rt.BaseDockerFile(f); err != nil {
				return err
			}

			pterm.Debug.Println("Building image for" + f.Name())

			if err := ce.Build(filepath.Base(f.Name()), s.Dir, fmt.Sprintf("%s-%s", s.Name, fun.Name), rt.BuildArgs(), rt.BuildIgnore()); err != nil {
				return err
			}

			finalFunctions[key] = fun
		}
	}

	s.Functions = finalFunctions

	return nil
}

func List(s *project.Project) ([]containerengine.Image, error) {
	cr, err := containerengine.Discover()
	if err != nil {
		return nil, err
	}

	images := []containerengine.Image{}

	for _, f := range s.Functions {
		imgs, err := cr.ListImages(s.Name, f.Name)
		if err != nil {
			fmt.Println("Error: ", err)
		} else {
			images = append(images, imgs...)
		}
	}

	for _, c := range s.Containers {
		imgs, err := cr.ListImages(s.Name, c.Name)
		if err != nil {
			fmt.Println("Error: ", err)
		} else {
			images = append(images, imgs...)
		}
	}

	return images, nil
}
