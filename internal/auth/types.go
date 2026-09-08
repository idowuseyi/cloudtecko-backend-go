package auth

import (
	"time"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
)

type Result struct {
	Token      string
	User       *dto.User
	ValidUntil time.Time
	Scopes     []string
}
