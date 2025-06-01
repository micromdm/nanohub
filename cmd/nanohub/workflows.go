package main

import (
	"fmt"

	"github.com/micromdm/nanocmd/workflow"
	"github.com/micromdm/nanocmd/workflow/certprof"
	"github.com/micromdm/nanocmd/workflow/cmdplan"
	"github.com/micromdm/nanocmd/workflow/devinfolog"
	"github.com/micromdm/nanocmd/workflow/fvenable"
	"github.com/micromdm/nanocmd/workflow/fvrotate"
	"github.com/micromdm/nanocmd/workflow/inventory"
	"github.com/micromdm/nanocmd/workflow/lock"
	"github.com/micromdm/nanocmd/workflow/profile"
	"github.com/micromdm/nanohub/nanohub"
	"github.com/micromdm/nanolib/log"
)

func workflows(logger log.Logger, s *subsystemStorage) (opts []nanohub.Option) {
	if s.inventory != nil {
		opts = append(opts, nanohub.WithWorkflow(
			func(e workflow.StepEnqueuer) (w workflow.Workflow, err error) {
				if w, err = inventory.New(e, s.inventory); err != nil {
					err = fmt.Errorf("creating inventory workflow: %w", err)
				}
				return
			},
		))

		opts = append(opts, nanohub.WithWorkflow(
			func(e workflow.StepEnqueuer) (w workflow.Workflow, err error) {
				if w, err = lock.New(e, s.inventory, lock.WithLogger(logger)); err != nil {
					err = fmt.Errorf("creating lock workflow: %w", err)
				}
				return
			},
		))
	}

	if s.profile != nil {
		opts = append(opts, nanohub.WithWorkflow(
			func(e workflow.StepEnqueuer) (w workflow.Workflow, err error) {
				if w, err = profile.New(e, s.profile, profile.WithLogger(logger)); err != nil {
					err = fmt.Errorf("creating profile workflow: %w", err)
				}
				return
			},
		))

		opts = append(opts, nanohub.WithWorkflow(
			func(e workflow.StepEnqueuer) (w workflow.Workflow, err error) {
				if w, err = certprof.New(e, s.profile, certprof.WithLogger(logger)); err != nil {
					err = fmt.Errorf("creating certprof workflow: %w", err)
				}
				return
			},
		))
	}

	if s.filevault != nil && s.profile != nil {
		opts = append(opts, nanohub.WithWorkflow(
			func(e workflow.StepEnqueuer) (w workflow.Workflow, err error) {
				if w, err = fvenable.New(e, s.filevault, s.profile, fvenable.WithLogger(logger)); err != nil {
					err = fmt.Errorf("creating fvenable workflow: %w", err)
				}
				return
			},
		))

		// technically does not require s.profile but they're a package deal
		opts = append(opts, nanohub.WithWorkflow(
			func(e workflow.StepEnqueuer) (w workflow.Workflow, err error) {
				if w, err = fvrotate.New(e, s.filevault, fvrotate.WithLogger(logger)); err != nil {
					err = fmt.Errorf("creating fvrotate workflow: %w", err)
				}
				return
			},
		))
	}

	if s.cmdplan != nil && s.profile != nil {
		opts = append(opts, nanohub.WithWorkflow(
			func(e workflow.StepEnqueuer) (w workflow.Workflow, err error) {
				if w, err = cmdplan.New(e, s.cmdplan, s.profile, cmdplan.WithLogger(logger)); err != nil {
					err = fmt.Errorf("creating cmdplan workflow: %w", err)
				}
				return
			},
		))
	}

	opts = append(opts, nanohub.WithWorkflow(
		func(e workflow.StepEnqueuer) (w workflow.Workflow, err error) {
			if w, err = devinfolog.New(e, logger); err != nil {
				err = fmt.Errorf("creating devinfolog workflow: %w", err)
			}
			return
		},
	))

	return
}
