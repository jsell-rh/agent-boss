package sqlite_test

import (
	"github.com/ambient/platform/components/boss/internal/adapters/storage/sqlite"
	"github.com/ambient/platform/components/boss/internal/domain/ports"
)

var _ ports.StoragePort = (*sqlite.StorageAdapter)(nil)
