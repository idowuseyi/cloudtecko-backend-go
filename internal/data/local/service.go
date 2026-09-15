package local

import (
	"database/sql"

	"github.com/dropbox/godropbox/time2"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/config"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/payment"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/storage"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util/secretbox"
)

type Service struct {
	config  config.Server
	db      *sql.DB
	clock   time2.Clock
	storage storage.Storage
	payment payment.Provider
	secrets *secretbox.Box

	// paymentProviderName is stored on every payment row so a later
	// investigation can tell which provider actually handled it -- including
	// rows created while the mock was active.
	paymentProviderName string
}

func NewService(config config.Server, db *sql.DB, clock time2.Clock, store storage.Storage, paymentProvider payment.Provider, secrets *secretbox.Box) *Service {
	return &Service{
		config:              config,
		db:                  db,
		clock:               clock,
		storage:             store,
		payment:             paymentProvider,
		secrets:             secrets,
		paymentProviderName: paymentProvider.Name(),
	}
}
