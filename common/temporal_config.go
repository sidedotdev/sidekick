package common

import (
	"fmt"
	"os"
	"strconv"
)

const defaultTemporalRetentionDays = 90

func GetTemporalRetentionDays() int {
	retentionDaysStr := os.Getenv("SIDE_TEMPORAL_RETENTION_DAYS")
	if retentionDaysStr == "" {
		return defaultTemporalRetentionDays
	}

	retentionDays, err := strconv.Atoi(retentionDaysStr)
	if err != nil || retentionDays <= 0 {
		fmt.Fprintf(os.Stderr, "Warning: invalid SIDE_TEMPORAL_RETENTION_DAYS value '%s', using default %d days\n",
			retentionDaysStr, defaultTemporalRetentionDays)
		return defaultTemporalRetentionDays
	}

	return retentionDays
}
