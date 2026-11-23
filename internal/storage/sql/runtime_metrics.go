package sql

import "github.com/yzgolden86/PivotFlow/internal/model"

// StorageRuntimeStats exposes the storage category without touching the
// database or returning its address, credentials, or connection settings.
func (s *SQLStore) StorageRuntimeStats() model.StorageRuntimeStats {
	return model.StorageRuntimeStats{Mode: s.driverName, Primary: s.driverName}
}
