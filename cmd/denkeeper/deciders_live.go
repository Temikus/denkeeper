package main

import (
	"context"
	"log/slog"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/eval"
	"github.com/Temikus/denkeeper/internal/llm"
)

// syncDeciders makes the live decider set match cfg, then rebinds every
// running agent's supervisor decider stage. The decide tool and the eval judge
// look deciders up themselves (per call, and in judgeConfigFrom).
func syncDeciders(cfg *config.Config, set *llm.DeciderSet, dispatcher *agent.Dispatcher, logger *slog.Logger) {
	if set == nil {
		return
	}
	set.Sync(deciderConfigs(cfg))
	for _, ac := range cfg.Agents {
		if e := dispatcher.Agent(ac.Name); e != nil {
			bindSupervisorDecider(e, ac, set, logger)
		}
	}
}

// bindSupervisorDecider points an agent's decider stage at the running decider
// it names, or clears the stage. An unchanged binding is left alone;
// applySupervisorKnobs re-tunes its mode and thresholds.
func bindSupervisorDecider(e *agent.Engine, ac config.AgentInstanceConfig, set *llm.DeciderSet, logger *slog.Logger) {
	want := set.Get(ac.SupervisorDecider)
	if ac.SupervisorDecider != "" && want == nil {
		logger.Warn("supervisor decider not running; reviews skip the decider stage", "agent", ac.Name, "decider", ac.SupervisorDecider)
	}
	if e.SupervisorDecider() == want {
		return
	}
	e.SetSupervisorDecider(want, deciderStageFrom(ac))
	if want == nil {
		logger.Info("supervisor decider unbound", "agent", ac.Name)
		return
	}
	logger.Info("supervisor decider bound", "agent", ac.Name, "decider", want.Name(), "model", want.Model(), "mode", ac.SupervisorDeciderMode)
}

// liveDeciderRuntime is the API's handle on the running deciders.
type liveDeciderRuntime struct {
	set        *llm.DeciderSet
	providers  *llm.ProviderSet
	dispatcher *agent.Dispatcher
	judge      *eval.Judge
	logger     *slog.Logger
}

// Apply syncs the set and rebinds supervisor stages and the eval judge.
func (l liveDeciderRuntime) Apply(snap *config.Config) {
	syncDeciders(snap, l.set, l.dispatcher, l.logger)
	if l.judge != nil {
		l.judge.SetConfig(judgeConfigFrom(snap, l.set, l.logger))
	}
}

// Test asks dc one fixed question. It bills no agent: the cost is reported
// back to the caller instead.
func (l liveDeciderRuntime) Test(ctx context.Context, dc config.DeciderConfig) (*llm.DecisionResponse, error) {
	d := llm.NewLiveDecider(deciderConfig(dc), l.providers, nil)
	return d.Decide(ctx, "decider-test", map[string]any{"message": map[string]string{"text": "Hello there!"}},
		map[string]llm.Question{"greeting": {Type: llm.QuestionNoul, Instructions: "`message.text` is a greeting."}})
}
