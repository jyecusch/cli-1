package codeconfig

import (
	"fmt"
	"strings"
	"sync"

	"github.com/nitrictech/cli/pkg/utils"
	v1 "github.com/nitrictech/nitric/core/pkg/api/nitric/v1"
)

type Api struct {
	parent              *FunctionDependencies
	securityDefinitions map[string]*v1.ApiSecurityDefinition
	security            map[string][]string
	workers             []*v1.ApiWorker
	lock                sync.RWMutex
}

func (a *Api) String() string {
	return fmt.Sprintf("workers: %+v", a.workers)
}

func newApi(parent *FunctionDependencies) *Api {
	return &Api{
		parent:              parent,
		workers:             make([]*v1.ApiWorker, 0),
		securityDefinitions: make(map[string]*v1.ApiSecurityDefinition),
		security:            make(map[string][]string),
	}
}

func normalizePath(path string) string {
	parts := utils.SplitPath(path)
	for i, part := range parts {
		if strings.HasPrefix(part, ":") {
			parts[i] = ":param"

			continue
		}

		parts[i] = strings.ToLower(part)
	}

	return strings.Join(parts, "/")
}

func matchingWorkers(a *v1.ApiWorker, b *v1.ApiWorker) bool {
	if normalizePath(a.GetPath()) == normalizePath(b.GetPath()) {
		for _, aMethod := range a.GetMethods() {
			for _, bMethod := range b.GetMethods() {
				if aMethod == bMethod {
					return true
				}
			}
		}
	}

	return false
}

func (a *Api) AddWorker(worker *v1.ApiWorker) {
	a.lock.Lock()
	defer a.lock.Unlock()

	// Ensure the worker is unique
	for _, existing := range a.workers {
		if matchingWorkers(existing, worker) {
			a.parent.AddError("overlapping worker %v already registered, can't add new worker %v")
			return
		}
	}

	a.workers = append(a.workers, worker)
}

func (a *Api) AddSecurityDefinition(name string, sd *v1.ApiSecurityDefinition) {
	a.lock.Lock()
	defer a.lock.Unlock()

	a.securityDefinitions[name] = sd
}

func (a *Api) AddSecurity(name string, scopes []string) {
	a.lock.Lock()
	defer a.lock.Unlock()

	if scopes != nil {
		a.security[name] = scopes
	} else {
		// default to empty scopes for a nil assignment
		a.security[name] = []string{}
	}
}
