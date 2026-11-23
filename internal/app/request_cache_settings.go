package app

import (
	"fmt"
	"strconv"
)

func defaultRequestCacheConfig() requestCacheConfig {
	return requestCacheConfig{Enabled: false, TTLSeconds: 60, MaxEntries: 256, MaxBytes: 32 * 1024 * 1024, MaxEntryBytes: 1024 * 1024}
}

// Independent ranges guarantee max_entry_bytes <= max_bytes for every valid
// single/reset/batch combination, without racing cross-setting transactions.
func validateRequestCacheSetting(key, value string) error {
	minimum, maximum := 0, 0
	switch key {
	case "request_cache_enabled":
		if value != "true" && value != "false" && value != "1" && value != "0" {
			return fmt.Errorf("request_cache_enabled must be a boolean")
		}
		return nil
	case "request_cache_ttl_seconds":
		minimum, maximum = 5, 3600
	case "request_cache_max_entries":
		minimum, maximum = 1, 4096
	case "request_cache_max_bytes":
		minimum, maximum = 1024*1024, 128*1024*1024
	case "request_cache_max_entry_bytes":
		minimum, maximum = 4096, 1024*1024
	default:
		return nil
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < minimum || number > maximum {
		return fmt.Errorf("%s must be between %d and %d", key, minimum, maximum)
	}
	return nil
}

func loadRequestCacheConfig(cs *ConfigService) (requestCacheConfig, error) {
	config := defaultRequestCacheConfig()
	if cs == nil {
		return config, nil
	}
	for _, key := range []string{"request_cache_enabled", "request_cache_ttl_seconds", "request_cache_max_entries", "request_cache_max_bytes", "request_cache_max_entry_bytes"} {
		if setting := cs.GetSetting(key); setting != nil {
			if err := validateRequestCacheSetting(key, setting.Value); err != nil {
				return requestCacheConfig{}, err
			}
		}
	}
	config.Enabled = cs.GetBool("request_cache_enabled", config.Enabled)
	config.TTLSeconds = cs.GetInt("request_cache_ttl_seconds", config.TTLSeconds)
	config.MaxEntries = cs.GetInt("request_cache_max_entries", config.MaxEntries)
	config.MaxBytes = cs.GetInt("request_cache_max_bytes", config.MaxBytes)
	config.MaxEntryBytes = cs.GetInt("request_cache_max_entry_bytes", config.MaxEntryBytes)
	return config, nil
}
