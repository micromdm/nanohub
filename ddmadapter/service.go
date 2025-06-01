package ddmadapter

import (
	"github.com/jessepeterson/kmfddm/storage"
	"github.com/micromdm/nanomdm/mdm"
	"github.com/micromdm/nanomdm/service"
)

// SetsRemover is a NanoMDM service that removes DM enrollment set
// associations when an enrollment is started (Authentication check-in
// message).
type SetsRemover struct {
	service.CheckinAndCommandService

	store storage.EnrollmentSetRemover
	sets  []string
}

// NewSetsRemover creates a new [SetsRemover] which dissociates enrollment sets.
// Specify the set names in sets.
// If sets is nil or empty all enrollment sets will be removed.
func NewSetsRemover(store storage.EnrollmentSetRemover, sets []string) *SetsRemover {
	if store == nil {
		panic("nil store")
	}

	return &SetsRemover{
		CheckinAndCommandService: new(service.NopService),
		store:                    store,
		sets:                     sets,
	}
}

// Authenticate disassociats enrollment sets for the enrollment ID in r.
func (s *SetsRemover) Authenticate(r *mdm.Request, msg *mdm.Authenticate) error {
	err := s.CheckinAndCommandService.Authenticate(r, msg)
	if err != nil {
		return err
	}

	if len(s.sets) < 1 {
		if _, err = s.store.RemoveAllEnrollmentSets(r.Context(), r.ID); err != nil {
			return err
		}
	} else {
		for _, set := range s.sets {
			if _, err = s.store.RemoveEnrollmentSet(r.Context(), r.ID, set); err != nil {
				return err
			}
		}
	}

	return nil
}
