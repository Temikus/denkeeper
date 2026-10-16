package main

import (
	"context"
	"slices"
	"sync/atomic"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/api"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/task"
)

// newTaskService returns nil unless [tasks] enabled is set. A nil service
// leaves the task endpoints, the task_* tools and the Tasks page off.
func newTaskService(cfg *config.Config, st *task.Store, dir task.Directory, auditor audit.Emitter) *task.Service {
	if !cfg.Tasks.Enabled {
		return nil
	}
	return task.NewService(st, dir, auditor)
}

// taskDirectory lists task members: every den agent, plus every live API key
// that lists tasks:write itself and not admin. An admin key is the operator
// (REST treats it as "human"), and REST matches scopes exactly, so the
// dashboard's key lists tasks:write too and would otherwise be an assignee.
//
// main builds a placeholder dispatcher and replaces it once the engines exist,
// and opens the key store later still, so both are set after construction.
type taskDirectory struct {
	cfg        *config.Holder
	dispatcher atomic.Pointer[agent.Dispatcher]
	keys       atomic.Pointer[api.KeyStore]
}

func (d *taskDirectory) setDispatcher(disp *agent.Dispatcher) { d.dispatcher.Store(disp) }

func (d *taskDirectory) setKeyStore(ks *api.KeyStore) { d.keys.Store(ks) }

func (d *taskDirectory) Members(ctx context.Context) ([]task.Member, error) {
	var out []task.Member
	if disp := d.dispatcher.Load(); disp != nil {
		agents := disp.Agents()
		slices.Sort(agents)
		for _, name := range agents {
			out = append(out, task.Member{ID: task.AgentID(name), Name: name, Kind: "agent"})
		}
	}
	seen := map[string]bool{}
	addKey := func(name string, scopes []string) {
		if name == "" || seen[name] || !slices.Contains(scopes, "tasks:write") || slices.Contains(scopes, "admin") {
			return
		}
		seen[name] = true
		out = append(out, task.Member{ID: task.ExternalID(name), Name: name, Kind: "external"})
	}
	if ks := d.keys.Load(); ks != nil {
		recs, err := ks.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range recs {
			if !r.Revoked {
				addKey(r.Name, r.Scopes)
			}
		}
	}
	if d.cfg != nil {
		for _, k := range d.cfg.Get().API.Keys {
			addKey(k.Name, k.Scopes)
		}
	}
	return out, nil
}
