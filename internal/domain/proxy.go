package domain

import (
	"time"

	"github.com/google/uuid"
)

type Proxy struct {
	ID        uuid.UUID
	Host      string
	Port      int
	IsActive  bool
	CreatedAt time.Time
	UpdatedAt time.Time
}
