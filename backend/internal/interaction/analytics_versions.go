package interaction

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var versionDimensions = []string{"agent", "prompts", "tools", "knowledge", "judges", "evaluator"}

const versionKnowledge = "knowledge"

const versionBaselineLookback = 90 * 24 * time.Hour

type AnalyticsVersions struct {
	InEffect []VersionInEffect
	Changes  []VersionChange
}

type VersionInEffect struct {
	Dimension  string
	Version    string
	LocationID string
}

type VersionChange struct {
	Dimension       string
	Version         string
	PreviousVersion string
	FirstSeenAt     time.Time
	Date            string
	LocationID      string
}

type callVersions struct {
	locationID string
	values     map[string]string
}

type versionStream struct {
	current string
	seen    map[string]struct{}
}

type versionAccumulator struct {
	streams map[string]*versionStream
	result  AnalyticsVersions
}

func newVersionAccumulator() *versionAccumulator {
	return &versionAccumulator{
		streams: map[string]*versionStream{},
		result:  AnalyticsVersions{InEffect: []VersionInEffect{}, Changes: []VersionChange{}},
	}
}

func versionStreamKey(dimension, locationID string) string {
	if dimension != versionKnowledge {
		locationID = ""
	}
	return dimension + "|" + locationID
}

func (v *versionAccumulator) seed(dimension, locationID, version string) {
	if dimension != versionKnowledge {
		locationID = ""
	}
	v.streams[versionStreamKey(dimension, locationID)] = &versionStream{current: version, seen: map[string]struct{}{version: {}}}
	v.result.InEffect = append(v.result.InEffect, VersionInEffect{Dimension: dimension, Version: version, LocationID: locationID})
}

func (v *versionAccumulator) add(startedAt time.Time, call callVersions) {
	for _, dimension := range versionDimensions {
		version := call.values[dimension]
		if version == "" {
			continue
		}
		key := versionStreamKey(dimension, call.locationID)
		stream := v.streams[key]
		if stream == nil {
			v.streams[key] = &versionStream{current: version, seen: map[string]struct{}{version: {}}}
			continue
		}
		if _, seen := stream.seen[version]; !seen {
			change := VersionChange{
				Dimension:       dimension,
				Version:         version,
				PreviousVersion: stream.current,
				FirstSeenAt:     startedAt,
				Date:            startedAt.UTC().Format(time.DateOnly),
			}
			if dimension == versionKnowledge {
				change.LocationID = call.locationID
			}
			v.result.Changes = append(v.result.Changes, change)
			stream.seen[version] = struct{}{}
		}
		stream.current = version
	}
}

func (v *versionAccumulator) finish() AnalyticsVersions {
	return v.result
}

func versionColumn(dimension string) string {
	return "version_" + dimension
}

func queryVersionBaseline(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
	locationIDs []string,
	from time.Time,
	versions *versionAccumulator,
) error {
	latest := func(column, location string) string {
		return fmt.Sprintf(`(SELECT %[1]s FROM ai_interactions
			WHERE practice_id = $1 AND location_id = %[2]s
				AND status <> 'IN_PROGRESS' AND lifecycle_stage = 3
				AND started_at < $3 AND started_at >= $4 AND %[1]s IS NOT NULL
			ORDER BY started_at DESC, id DESC LIMIT 1)`, column, location)
	}
	parts := []string{}
	for _, dimension := range versionDimensions {
		if dimension == versionKnowledge {
			parts = append(parts, fmt.Sprintf(`SELECT '%s', location.id::text, %s FROM unnest($2::uuid[]) AS location(id)`,
				dimension, latest(versionColumn(dimension), "location.id")))
			continue
		}
		parts = append(parts, fmt.Sprintf(`SELECT '%s', '', %s`, dimension, latest(versionColumn(dimension), "ANY($2::uuid[])")))
	}
	rows, err := tx.Query(ctx, `SELECT dimension, location_id, version FROM (`+strings.Join(parts, " UNION ALL ")+`) AS baseline(dimension, location_id, version) WHERE version IS NOT NULL`,
		practiceID, locationIDs, from, from.Add(-versionBaselineLookback))
	if err != nil {
		return fmt.Errorf("query AI version baseline: %w", err)
	}
	defer rows.Close()
	type baseline struct{ dimension, locationID, version string }
	found := []baseline{}
	for rows.Next() {
		var value baseline
		if err := rows.Scan(&value.dimension, &value.locationID, &value.version); err != nil {
			return fmt.Errorf("scan AI version baseline: %w", err)
		}
		found = append(found, value)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate AI version baseline: %w", err)
	}
	for _, dimension := range versionDimensions {
		for _, value := range found {
			if value.dimension == dimension {
				versions.seed(value.dimension, value.locationID, value.version)
			}
		}
	}
	return nil
}
