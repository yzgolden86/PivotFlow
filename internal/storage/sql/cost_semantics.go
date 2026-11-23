package sql

import (
	"fmt"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// costStatusSQL mirrors model.LogCostStatus using only values frozen on the
// log row. Keep the ordering identical: failures, invalid persisted amounts,
// valid version-1 statuses, then historical/unknown metadata.
func costStatusSQL(alias string) string {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return fmt.Sprintf(`CASE
		WHEN %[1]sstatus_code < 200 OR %[1]sstatus_code >= 300 THEN '%[2]s'
		WHEN COALESCE(%[1]scost, 0) < 0
			OR COALESCE(%[1]scost, 0) > 1.7976931348623157e308
			OR COALESCE(%[1]scost, 0) <> COALESCE(%[1]scost, 0)
			OR COALESCE(%[1]scost_multiplier, 1) > 1.7976931348623157e308
			OR COALESCE(%[1]scost_multiplier, 1) < -1.7976931348623157e308
			OR COALESCE(%[1]scost_multiplier, 1) <> COALESCE(%[1]scost_multiplier, 1)
			THEN '%[3]s'
		WHEN COALESCE(%[1]scost_metadata_version, 0) = %[4]d
			AND COALESCE(%[1]scost_status, '') IN ('%[5]s','%[6]s','%[7]s','%[8]s','%[9]s','%[10]s','%[11]s','%[12]s','%[2]s')
			AND COALESCE(%[1]scost_status, '') <> '%[2]s'
			THEN %[1]scost_status
		ELSE '%[13]s'
	END`,
		prefix,
		model.CostStatusNotCharged,
		model.CostStatusInvalid,
		model.CostMetadataVersion,
		model.CostStatusEstimated,
		model.CostStatusZeroPrice,
		model.CostStatusLocalFree,
		model.CostStatusUsageMissing,
		model.CostStatusUnpricedModel,
		model.CostStatusPartialEstimate,
		model.CostStatusHistoricalUnknown,
		model.CostStatusInvalid,
		model.CostStatusHistoricalUnknown,
	)
}

// costBasisSQL mirrors model.LogCostBasis: only a version-1 row with one of
// the four persisted bases has a known basis.
func costBasisSQL(alias string) string {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return fmt.Sprintf(
		"CASE WHEN COALESCE(%[1]scost_metadata_version, 0) = %[2]d AND COALESCE(%[1]scost_basis, '') IN ('%[3]s','%[4]s','%[5]s','%[6]s') THEN %[1]scost_basis ELSE '%[6]s' END",
		prefix,
		model.CostMetadataVersion,
		model.CostBasisToken,
		model.CostBasisPerCall,
		model.CostBasisMixed,
		model.CostBasisUnknown,
	)
}

func (wb *WhereBuilder) applyCostFilter(filter *model.LogFilter) *WhereBuilder {
	if filter == nil {
		return wb
	}
	if filter.CostSource != "" {
		switch filter.CostSource {
		case model.CostSourceSite, model.CostSourceLocal:
			wb.AddCondition("cost_source = ?", filter.CostSource)
		default:
			wb.AddCondition("1=0")
		}
	}
	if filter.CostStatus != "" {
		if !model.IsCostStatus(filter.CostStatus) {
			wb.AddCondition("1=0")
		} else {
			wb.AddCondition(costStatusSQL("")+" = ?", filter.CostStatus)
		}
	}
	if filter.CostBasis != "" {
		if !model.IsCostBasis(filter.CostBasis) {
			wb.AddCondition("1=0")
		} else {
			wb.AddCondition(costBasisSQL("")+" = ?", filter.CostBasis)
		}
	}
	return wb
}
