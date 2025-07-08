package main

import (
	cmdplanhttp "github.com/micromdm/nanocmd/subsystem/cmdplan/http"
	fvenablehttp "github.com/micromdm/nanocmd/subsystem/filevault/http"
	invhttp "github.com/micromdm/nanocmd/subsystem/inventory/http"
	profhttp "github.com/micromdm/nanocmd/subsystem/profile/http"
	"github.com/micromdm/nanolib/log"
)

// handleSubsystemAPIs registers the subsystem APIs
func handleSubsystemAPIs(prefix string, mux fvenablehttp.Mux, logger log.Logger, storage *subsystemStorage) {
	if storage.inventory != nil {
		logger.Debug("msg", "registered subsystem endpoints", "name", "inventory")
		invhttp.HandleAPIv1(prefix, mux, logger, storage.inventory)
	}
	if storage.profile != nil {
		logger.Debug("msg", "registered subsystem endpoints", "name", "profile")
		profhttp.HandleAPIv1(prefix, mux, logger, storage.profile)
	}
	fvenablehttp.HandleAPIv1(prefix, mux)
	if storage.cmdplan != nil {
		logger.Debug("msg", "registered subsystem endpoints", "name", "cmdplan")
		cmdplanhttp.HandleAPIv1(prefix, mux, logger, storage.cmdplan)
	}
}
