package main

import (
	"errors"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"strings"

	"github.com/cespare/xxhash"
	dmstorage "github.com/jessepeterson/kmfddm/storage"
	dmfile "github.com/jessepeterson/kmfddm/storage/diskv"
	dminmem "github.com/jessepeterson/kmfddm/storage/inmem"
	dmmysql "github.com/jessepeterson/kmfddm/storage/mysql"
	cmdstorage "github.com/micromdm/nanocmd/engine/storage"
	cmdfile "github.com/micromdm/nanocmd/engine/storage/diskv"
	cmdinmem "github.com/micromdm/nanocmd/engine/storage/inmem"
	cmdmysql "github.com/micromdm/nanocmd/engine/storage/mysql"
	"github.com/micromdm/nanolib/log"
	mdmstorage "github.com/micromdm/nanomdm/storage"
	mdmfile "github.com/micromdm/nanomdm/storage/diskv"
	mdminmem "github.com/micromdm/nanomdm/storage/inmem"
	mdmmysql "github.com/micromdm/nanomdm/storage/mysql"

	stgcmdplan "github.com/micromdm/nanocmd/subsystem/cmdplan/storage"
	stgcmdplandiskv "github.com/micromdm/nanocmd/subsystem/cmdplan/storage/diskv"
	stgcmdplaninmem "github.com/micromdm/nanocmd/subsystem/cmdplan/storage/inmem"
	stgfv "github.com/micromdm/nanocmd/subsystem/filevault/storage"
	stgfvdiskv "github.com/micromdm/nanocmd/subsystem/filevault/storage/diskv"
	stgfvinmem "github.com/micromdm/nanocmd/subsystem/filevault/storage/inmem"
	stgfvinvprk "github.com/micromdm/nanocmd/subsystem/filevault/storage/invprk"
	stginv "github.com/micromdm/nanocmd/subsystem/inventory/storage"
	stginvdiskv "github.com/micromdm/nanocmd/subsystem/inventory/storage/diskv"
	stginvinmem "github.com/micromdm/nanocmd/subsystem/inventory/storage/inmem"
	stgprof "github.com/micromdm/nanocmd/subsystem/profile/storage"
	stgprofdiskv "github.com/micromdm/nanocmd/subsystem/profile/storage/diskv"
	stgprofinmem "github.com/micromdm/nanocmd/subsystem/profile/storage/inmem"

	_ "github.com/go-sql-driver/mysql"
)

var ErrOptionsNotSupported = errors.New("options not supported")

type nhdmstore interface {
	// DDM storage
	dmstorage.EnrollmentDeclarationStorage
	dmstorage.EnrollmentDeclarationDataStorage
	dmstorage.StatusStorer

	// notifier storage
	dmstorage.TokensJSONRetriever
	dmstorage.EnrollmentIDRetriever

	// API storage

	// declarations
	dmstorage.DeclarationAPIStorage

	// sets
	dmstorage.SetRetreiver

	// set declarations
	// declaration sets
	dmstorage.SetDeclarationStorage

	// enrollment sets
	dmstorage.EnrollmentSetStorage

	// status queries
	dmstorage.StatusAPIStorage
}

var hasher func() hash.Hash = func() hash.Hash { return xxhash.New() }

func NewStore(storage, dsn, options string, logger log.Logger) (mdmstorage.AllStorage, nhdmstore, cmdstorage.AllStorage, error) {
	switch storage {
	case "file":
		if options != "" {
			return nil, nil, nil, ErrOptionsNotSupported
		}
		if dsn == "" {
			dsn = "db"
		} else {
			dsn = strings.TrimRight(dsn, string(os.PathSeparator))
		}
		if err := os.Mkdir(dsn, 0755); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, nil, nil, err
		}
		mdmstore := mdmfile.New(filepath.Join(dsn, "mdm"))
		dmstore := dmfile.New(filepath.Join(dsn, "dm"), hasher)
		cmdstore := cmdfile.New(filepath.Join(dsn, "cmd"))
		return mdmstore, dmstore, cmdstore, nil
	case "mysql":
		if options != "" {
			return nil, nil, nil, ErrOptionsNotSupported
		}
		mdmStore, err := mdmmysql.New(
			mdmmysql.WithDSN(dsn),
			mdmmysql.WithLogger(logger.With("storgae", storage)),
		)
		if err != nil {
			return nil, nil, nil, err
		}
		dmStore, err := dmmysql.New(hasher, dmmysql.WithDSN(dsn))
		if err != nil {
			return nil, nil, nil, err
		}
		cmdStore, err := cmdmysql.New(cmdmysql.WithDSN(dsn))
		if err != nil {
			return nil, nil, nil, err
		}
		return mdmStore, dmStore, cmdStore, nil
	case "inmem":
		if options != "" {
			return nil, nil, nil, ErrOptionsNotSupported
		}
		return mdminmem.New(), dminmem.New(hasher), cmdinmem.New(), nil
	default:
		return nil, nil, nil, fmt.Errorf("unknown storage type: %s", storage)
	}
}

type subsystemStorage struct {
	inventory stginv.Storage
	profile   stgprof.Storage
	cmdplan   stgcmdplan.Storage
	filevault stgfv.FVRotate
}

func SubsystemStorage(storage, dsn string) (*subsystemStorage, error) {
	switch storage {
	case "inmem":
		inv := stginvinmem.New()
		fv, err := stgfvinmem.New(stgfvinvprk.NewInvPRK(inv))
		if err != nil {
			return nil, fmt.Errorf("creating filevault inmem storage: %w", err)
		}
		return &subsystemStorage{
			inventory: inv,
			profile:   stgprofinmem.New(),
			cmdplan:   stgcmdplaninmem.New(),
			filevault: fv,
		}, nil
	case "file":
		if dsn == "" {
			dsn = "db"
		}

		inv := stginvdiskv.New(filepath.Join(dsn, "subsys-inventory"))
		fv, err := stgfvdiskv.New(filepath.Join(dsn, "subsys-fvkey"), stgfvinvprk.NewInvPRK(inv))
		if err != nil {
			return nil, fmt.Errorf("creating filevault diskv storage: %w", err)
		}

		return &subsystemStorage{
			inventory: inv,
			profile:   stgprofdiskv.New(filepath.Join(dsn, "subsys-profile")),
			cmdplan:   stgcmdplandiskv.New(filepath.Join(dsn, "subsys-cmdplan")),
			filevault: fv,
		}, nil
	}

	return &subsystemStorage{}, nil
}
