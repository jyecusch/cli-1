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
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/samber/lo"
	"github.com/spf13/afero"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	goruntime "runtime"

	"github.com/nitrictech/cli/pkg/cloud"
	"github.com/nitrictech/cli/pkg/collector"
	"github.com/nitrictech/cli/pkg/preview"
	"github.com/nitrictech/cli/pkg/project/localconfig"
	"github.com/nitrictech/cli/pkg/project/runtime"
	"github.com/nitrictech/nitric/core/pkg/logger"
	apispb "github.com/nitrictech/nitric/core/pkg/proto/apis/v1"
	httppb "github.com/nitrictech/nitric/core/pkg/proto/http/v1"
	kvstorepb "github.com/nitrictech/nitric/core/pkg/proto/kvstore/v1"
	queuespb "github.com/nitrictech/nitric/core/pkg/proto/queues/v1"
	resourcespb "github.com/nitrictech/nitric/core/pkg/proto/resources/v1"
	schedulespb "github.com/nitrictech/nitric/core/pkg/proto/schedules/v1"
	secretspb "github.com/nitrictech/nitric/core/pkg/proto/secrets/v1"
	sqlpb "github.com/nitrictech/nitric/core/pkg/proto/sql/v1"
	storagepb "github.com/nitrictech/nitric/core/pkg/proto/storage/v1"
	topicspb "github.com/nitrictech/nitric/core/pkg/proto/topics/v1"
	websocketspb "github.com/nitrictech/nitric/core/pkg/proto/websockets/v1"
)

type Project struct {
	Name        string
	Directory   string
	Preview     []preview.Feature
	LocalConfig localconfig.LocalConfiguration

	services []Service
}

func (p *Project) GetServices() []Service {
	return p.services
}

// BuildServices - Builds all the services in the project
func (p *Project) BuildServices(fs afero.Fs) (chan ServiceBuildUpdate, error) {
	updatesChan := make(chan ServiceBuildUpdate)

	if len(p.services) == 0 {
		return nil, fmt.Errorf("no services found in project, nothing to build. This may indicate misconfigured `match` patterns in your nitric.yaml file")
	}

	maxConcurrentBuilds := make(chan struct{}, min(goruntime.NumCPU(), goruntime.GOMAXPROCS(0)))

	waitGroup := sync.WaitGroup{}

	for _, service := range p.services {
		waitGroup.Add(1)
		// Create writer
		serviceBuildUpdateWriter := NewBuildUpdateWriter(service.Name, updatesChan)

		go func(svc Service, writer io.Writer) {
			// Acquire a token by filling the maxConcurrentBuilds channel
			// this will block once the buffer is full
			maxConcurrentBuilds <- struct{}{}

			// Start goroutine
			if err := svc.BuildImage(fs, writer); err != nil {
				updatesChan <- ServiceBuildUpdate{
					ServiceName: svc.Name,
					Err:         err,
					Message:     err.Error(),
					Status:      ServiceBuildStatus_Error,
				}
			} else {
				updatesChan <- ServiceBuildUpdate{
					ServiceName: svc.Name,
					Message:     "Build Complete",
					Status:      ServiceBuildStatus_Complete,
				}
			}

			// release our lock
			<-maxConcurrentBuilds

			waitGroup.Done()
		}(service, serviceBuildUpdateWriter)
	}

	go func() {
		waitGroup.Wait()
		// Drain the semaphore to make sure all goroutines have finished
		for i := 0; i < cap(maxConcurrentBuilds); i++ {
			maxConcurrentBuilds <- struct{}{}
		}

		close(updatesChan)
	}()

	return updatesChan, nil
}

func (p *Project) collectServiceRequirements(service Service) (*collector.ServiceRequirements, error) {
	serviceRequirements := collector.NewServiceRequirements(service.Name, service.GetFilePath(), service.Type)

	// start a grpc service with this registered
	grpcServer := grpc.NewServer()

	resourcespb.RegisterResourcesServer(grpcServer, serviceRequirements)
	apispb.RegisterApiServer(grpcServer, serviceRequirements.ApiServer)
	schedulespb.RegisterSchedulesServer(grpcServer, serviceRequirements)
	topicspb.RegisterTopicsServer(grpcServer, serviceRequirements)
	topicspb.RegisterSubscriberServer(grpcServer, serviceRequirements)
	websocketspb.RegisterWebsocketHandlerServer(grpcServer, serviceRequirements)
	storagepb.RegisterStorageListenerServer(grpcServer, serviceRequirements)
	httppb.RegisterHttpServer(grpcServer, serviceRequirements)
	storagepb.RegisterStorageServer(grpcServer, serviceRequirements)
	queuespb.RegisterQueuesServer(grpcServer, serviceRequirements)
	kvstorepb.RegisterKvStoreServer(grpcServer, serviceRequirements)
	sqlpb.RegisterSqlServer(grpcServer, serviceRequirements)
	secretspb.RegisterSecretManagerServer(grpcServer, serviceRequirements)

	listener, err := net.Listen("tcp", ":")
	if err != nil {
		return nil, err
	}

	// register non-blocking
	go func() {
		err := grpcServer.Serve(listener)
		if err != nil {
			logger.Errorf("unable to start local Nitric collection server: %s", err)
		}
	}()

	defer grpcServer.Stop()

	// run the service we want to collect for targeting the grpc server
	// TODO: load and run .env files, etc.
	stopChannel := make(chan bool)
	updatesChannel := make(chan ServiceRunUpdate)

	go func() {
		// TODO: elevate env for tmp diretory and reuse
		tmpCollectDir := "./.nitric/collect"

		err := os.MkdirAll(tmpCollectDir, os.ModePerm)
		if err != nil {
			log.Fatalf("unable to create collect log directory %s", err)
		}

		// Create a temporary log file for this service
		logFile, err := afero.TempFile(afero.NewOsFs(), tmpCollectDir, fmt.Sprintf("nitric-%s-*.log", service.Name))
		if err != nil {
			log.Fatalf("unable to create collect log file: %s", err)
		}

		defer logFile.Close()

		for update := range updatesChannel {
			_, err = logFile.WriteString(update.Message)
			if err != nil {
				log.Fatalf("unable to write update log %s", err)
			}

			if update.Err != nil {
				_, err = logFile.WriteString(update.Err.Error())
				if err != nil {
					log.Fatalf("unable to write update error log %s", err)
				}
			}
		}
	}()

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return nil, fmt.Errorf("unable to split host and port for local Nitric collection server: %w", err)
	}

	err = service.RunContainer(stopChannel, updatesChannel, WithNitricPort(port), WithNitricEnvironment("build"))
	if err != nil {
		return nil, err
	}

	if serviceRequirements.HasDatabases() && !slices.Contains(p.Preview, preview.Feature_SqlDatabases) {
		return nil, fmt.Errorf("service %s requires a database, but the project does not have the 'sql-databases' preview feature enabled. Please add sql-databases to the preview field of your nitric.yaml file to enable this feature", service.GetFilePath())
	}

	return serviceRequirements, nil
}

func (p *Project) CollectServicesRequirements() ([]*collector.ServiceRequirements, error) {
	allServiceRequirements := []*collector.ServiceRequirements{}
	serviceErrors := []error{}

	reqLock := sync.Mutex{}
	errorLock := sync.Mutex{}
	wg := sync.WaitGroup{}

	for _, service := range p.services {
		svc := service

		wg.Add(1)

		go func(s Service) {
			defer wg.Done()

			serviceRequirements, err := p.collectServiceRequirements(s)
			if err != nil {
				errorLock.Lock()
				defer errorLock.Unlock()

				serviceErrors = append(serviceErrors, err)

				return
			}

			reqLock.Lock()
			defer reqLock.Unlock()

			allServiceRequirements = append(allServiceRequirements, serviceRequirements)
		}(svc)
	}

	wg.Wait()

	if len(serviceErrors) > 0 {
		return nil, errors.Join(serviceErrors...)
	}

	return allServiceRequirements, nil
}

// DefaultMigrationImage - Returns the default migration image name for the project
// Also returns ok if image is required or not
func (p *Project) DefaultMigrationImage(fs afero.Fs) (string, bool) {
	ok, _ := afero.DirExists(fs, "./migrations")

	return fmt.Sprintf("%s-nitric-migrations", p.Name), ok
}

// RunServicesWithCommand - Runs all the services locally using a startup command
// use the stop channel to stop all running services
func (p *Project) RunServicesWithCommand(localCloud *cloud.LocalCloud, stop <-chan bool, updates chan<- ServiceRunUpdate, env map[string]string) error {
	stopChannels := lo.FanOut[bool](len(p.services), 1, stop)

	group, _ := errgroup.WithContext(context.TODO())

	for i, service := range p.services {
		idx := i
		svc := service

		// start the service with the given file reference from its projects CWD
		group.Go(func() error {
			port, err := localCloud.AddService(svc.GetFilePath())
			if err != nil {
				return err
			}

			envVariables := map[string]string{
				"PYTHONUNBUFFERED":   "TRUE", // ensure all print statements print immediately for python
				"NITRIC_ENVIRONMENT": "run",
				"SERVICE_ADDRESS":    "localhost:" + strconv.Itoa(port),
			}

			for key, value := range env {
				envVariables[key] = value
			}

			return svc.Run(stopChannels[idx], updates, envVariables)
		})
	}

	return group.Wait()
}

// RunServices - Runs all the services as containers
// use the stop channel to stop all running services
func (p *Project) RunServices(localCloud *cloud.LocalCloud, stop <-chan bool, updates chan<- ServiceRunUpdate, env map[string]string) error {
	stopChannels := lo.FanOut[bool](len(p.services), 1, stop)

	group, _ := errgroup.WithContext(context.TODO())

	for i, service := range p.services {
		idx := i
		svc := service

		group.Go(func() error {
			port, err := localCloud.AddService(svc.GetFilePath())
			if err != nil {
				return err
			}

			return svc.RunContainer(stopChannels[idx], updates, WithNitricPort(strconv.Itoa(port)), WithEnvVars(env))
		})
	}

	return group.Wait()
}

func (pc *ProjectConfiguration) pathToNormalizedServiceName(servicePath string) string {
	// Add the project name as a prefix to group service images
	servicePath = fmt.Sprintf("%s_%s", pc.Name, servicePath)
	// replace path separators with dashes
	servicePath = strings.ReplaceAll(servicePath, string(os.PathSeparator), "-")
	// remove the file extension
	servicePath = strings.ReplaceAll(servicePath, filepath.Ext(servicePath), "")
	// replace dots with dashes
	servicePath = strings.ReplaceAll(servicePath, ".", "-")
	// replace all non-word characters
	servicePath = strings.ReplaceAll(servicePath, "[^\\w]", "-")

	return strings.ToLower(servicePath)
}

// fromProjectConfiguration creates a new Instance of a nitric Project from a configuration files contents
func fromProjectConfiguration(projectConfig *ProjectConfiguration, localConfig *localconfig.LocalConfiguration, fs afero.Fs) (*Project, error) {
	services := []Service{}

	matches := map[string]string{}

	for _, serviceSpec := range projectConfig.Services {
		serviceMatch := filepath.Join(serviceSpec.Basedir, serviceSpec.Match)

		files, err := afero.Glob(fs, serviceMatch)
		if err != nil {
			return nil, fmt.Errorf("unable to match service files for pattern %s: %w", serviceMatch, err)
		}

		for _, f := range files {
			relativeServiceEntrypointPath, _ := filepath.Rel(filepath.Join(projectConfig.Directory, serviceSpec.Basedir), f)
			projectRelativeServiceFile := filepath.Join(projectConfig.Directory, f)

			serviceName := projectConfig.pathToNormalizedServiceName(projectRelativeServiceFile)

			var buildContext *runtime.RuntimeBuildContext

			otherEntryPointFiles := lo.Filter(files, func(file string, index int) bool {
				return file != f
			})

			if serviceSpec.Runtime != "" {
				// We have a custom runtime
				customRuntime, ok := projectConfig.Runtimes[serviceSpec.Runtime]
				if !ok {
					return nil, fmt.Errorf("unable to find runtime %s", serviceSpec.Runtime)
				}

				buildContext, err = runtime.NewBuildContext(
					relativeServiceEntrypointPath,
					customRuntime.Dockerfile,
					// will default to the project directory if not set
					lo.Ternary(customRuntime.Context != "", customRuntime.Context, serviceSpec.Basedir),
					customRuntime.Args,
					otherEntryPointFiles,
					fs,
				)
				if err != nil {
					return nil, fmt.Errorf("unable to create build context for custom service file %s: %w", f, err)
				}
			} else {
				buildContext, err = runtime.NewBuildContext(
					relativeServiceEntrypointPath,
					"",
					serviceSpec.Basedir,
					map[string]string{},
					otherEntryPointFiles,
					fs,
				)
				if err != nil {
					return nil, fmt.Errorf("unable to create build context for service file %s: %w", f, err)
				}
			}

			if matches[f] != "" {
				return nil, fmt.Errorf("service file %s matched by multiple patterns: %s and %s, services must only be matched by a single pattern", f, matches[f], serviceSpec.Match)
			}

			matches[f] = serviceSpec.Match

			relativeFilePath, err := filepath.Rel(serviceSpec.Basedir, f)
			if err != nil {
				return nil, fmt.Errorf("unable to get relative file path for service %s: %w", f, err)
			}

			newService := NewService(serviceName, serviceSpec.Type, relativeFilePath, *buildContext, serviceSpec.Start)

			if serviceSpec.Type == "" {
				serviceSpec.Type = "default"
			}

			services = append(services, *newService)
		}
	}

	// create an empty local configuration if none is provided
	if localConfig == nil {
		localConfig = &localconfig.LocalConfiguration{}
	}

	return &Project{
		Name:        projectConfig.Name,
		Directory:   projectConfig.Directory,
		Preview:     projectConfig.Preview,
		LocalConfig: *localConfig,
		services:    services,
	}, nil
}

// FromFile - Loads a nitric project from a nitric.yaml file
// If no filepath is provided, the default location './nitric.yaml' is used
func FromFile(fs afero.Fs, filepath string) (*Project, error) {
	projectConfig, err := ConfigurationFromFile(fs, filepath)
	if err != nil {
		return nil, fmt.Errorf("error loading nitric.yaml: %w", err)
	}

	// load local configuration
	localConfig, err := localconfig.LocalConfigurationFromFile(fs, "")
	if err != nil {
		return nil, fmt.Errorf("error loading local.nitric.yaml: %w", err)
	}

	return fromProjectConfiguration(projectConfig, localConfig, fs)
}
