package pipeline

import (
	"context"

	"github.com/pkg/errors"

	"github.com/smartcontractkit/chainlink/core/logger"
)

type MemoTask struct {
	BaseTask `mapstructure:",squash"`
	Value    string `json:"value"`
}

var _ Task = (*MemoTask)(nil)

func (t *MemoTask) Type() TaskType {
	return TaskTypeMemo
}

func (t *MemoTask) Run(_ context.Context, _ logger.Logger, vars Vars, inputs []Result) (Result, RunInfo) {
	err := CheckInputsLen(inputs, 0, 1)
	if err != nil {
		return Result{Error: errors.Wrap(err, "task value missing")}, RunInfo{}
	}

	var value ObjectParam
	err = errors.Wrap(ResolveParam(&value, From(JSONWithVarExprs(t.Value, vars, false), Input(inputs, 0))), "value")
	if err != nil {
		return Result{Error: err}, RunInfo{}
	}

	return Result{Value: value}, RunInfo{}
}
