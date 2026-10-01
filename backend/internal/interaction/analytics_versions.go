package interaction

import (
	"context"
	"fmt"
	"sort"
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
	dimension  string
	locationID string
	current    string
	day        string
	served     map[string]*versionDayCount
}

type versionDayCount struct {
	calls int
	first time.Time
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

func (v *versionAccumulator) stream(dimension, locationID string) *versionStream {
	if dimension != versionKnowledge {
		locationID = ""
	}
	key := dimension + "|" + locationID
	if v.streams[key] == nil {
		v.streams[key] = &versionStream{dimension: dimension, locationID: locationID}
	}
	return v.streams[key]
}

func (v *versionAccumulator) seed(dimension, locationID, version string) {
	stream := v.stream(dimension, locationID)
	stream.current = version
	v.result.InEffect = append(v.result.InEffect, VersionInEffect{Dimension: dimension, Version: version, LocationID: stream.locationID})
}

func (v *versionAccumulator) add(startedAt time.Time, call callVersions) {
	day := startedAt.UTC().Format(time.DateOnly)
	for _, dimension := range versionDimensions {
		version := call.values[dimension]
		if version == "" {
			continue
		}
		stream := v.stream(dimension, call.locationID)
		if stream.day != day {
			v.flush(stream)
			stream.day, stream.served = day, map[string]*versionDayCount{}
		}
		if stream.served[version] == nil {
			stream.served[version] = &versionDayCount{first: startedAt}
		}
		stream.served[version].calls++
	}
}

func (v *versionAccumulator) flush(stream *versionStream) {
	most := 0
	for _, count := range stream.served {
		most = max(most, count.calls)
	}
	if count := stream.served[stream.current]; most == 0 || count != nil && count.calls == most {
		return
	}
	majority := ""
	for version, count := range stream.served {
		if count.calls != most {
			continue
		}
		if leader := stream.served[majority]; leader == nil || count.first.Before(leader.first) || count.first.Equal(leader.first) && version < majority {
			majority = version
		}
	}
	if stream.current != "" {
		first := stream.served[majority].first
		v.result.Changes = append(v.result.Changes, VersionChange{
			Dimension:       stream.dimension,
			Version:         majority,
			PreviousVersion: stream.current,
			FirstSeenAt:     first,
			Date:            first.UTC().Format(time.DateOnly),
			LocationID:      stream.locationID,
		})
	}
	stream.current = majority
}

func (v *versionAccumulator) finish() AnalyticsVersions {
	for _, stream := range v.streams {
		v.flush(stream)
		stream.served = nil
	}
	order := map[string]int{}
	for i, dimension := range versionDimensions {
		order[dimension] = i
	}
	sort.SliceStable(v.result.Changes, func(i, j int) bool {
		a, b := v.result.Changes[i], v.result.Changes[j]
		if !a.FirstSeenAt.Equal(b.FirstSeenAt) {
			return a.FirstSeenAt.Before(b.FirstSeenAt)
		}
		if a.Dimension != b.Dimension {
			return order[a.Dimension] < order[b.Dimension]
		}
		return a.LocationID < b.LocationID
	})
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
