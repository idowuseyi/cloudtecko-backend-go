package local

import (
	"database/sql"

	"github.com/dropbox/godropbox/time2"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/storage"
)

type Service struct {
	config  config.Server
	db      *sql.DB
	clock   time2.Clock
	storage storage.Storage
}

func NewService(config config.Server, db *sql.DB, clock time2.Clock, store storage.Storage) *Service {
	return &Service{
		config:  config,
		db:      db,
		clock:   clock,
		storage: store,
	}
}
