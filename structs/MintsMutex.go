package structs

import (
	"polymorphs-rarity-v1/models"
	"sync"
)

type MintsMutex struct {
	Mutex        sync.Mutex
	Mints        []models.PolymorphEntity
	TokensMap    map[string]bool
	Documents    []interface{}
	Transactions []interface{}
}
