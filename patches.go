package tory

import (
	"cmp"
	"context"
	"embed"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

//go:embed patches.sql data_migrations.sql
var sqlFiles embed.FS

type DBVersion struct {
	Version   int       `db:"version"`
	CreatedAt time.Time `db:"created_at"`
}

type Patch struct {
	Version int
	Name    string
}

type ApplyPatchesOptions struct {
	Prefix   string
	OnSkip   func(Patch)
	OnStart  func(Patch)
	OnFinish func(Patch)
}

// ApplyPatches advances the schema through the patches loaded under
// opts.Prefix, in the order of the number each name carries, and records the
// version reached. A database that has never been patched is baselined to the
// latest version without running the bodies, so the schema a fresh database is
// created with is taken to be current. Everything runs in one transaction.
func ApplyPatches(ctx context.Context, db Tory, opts ApplyPatchesOptions) (*DBVersion, error) {
	patches := make([]Patch, 0)
	versions := make(map[int]struct{})

	for _, q := range db.AllQueries() {
		if !strings.HasPrefix(q.Name(), opts.Prefix) {
			continue
		}

		version := parseVersion(strings.TrimPrefix(q.Name(), opts.Prefix))
		if version == -1 {
			return nil, fmt.Errorf("invalid patch name: `%s`", q.Name())
		}

		if _, ok := versions[version]; ok {
			return nil, fmt.Errorf("duplicate patch version: `%s`", q.Name())
		}
		versions[version] = struct{}{}

		patches = append(patches, Patch{
			Version: version,
			Name:    q.Name(),
		})
	}

	if len(patches) == 0 {
		return nil, fmt.Errorf("no patches found")
	}

	slices.SortFunc(patches, func(a, b Patch) int {
		return cmp.Compare(a.Version, b.Version)
	})

	latestVersion := patches[len(patches)-1].Version

	if err := db.Load(sqlFiles); err != nil {
		return nil, err
	}

	return db.Atomic(ctx, func(tx Tx) (*DBVersion, error) {
		if err := tx.Exec(ctx, "tory.create-table-db-version", nil); err != nil {
			return nil, err
		}

		currentVersion, err := tx.Get[DBVersion](ctx, "tory.upsert-db-version", Args{
			"version": latestVersion,
		})
		if err != nil {
			return currentVersion, err
		}

		for _, patch := range patches {
			if patch.Version <= currentVersion.Version {
				if opts.OnSkip != nil {
					opts.OnSkip(patch)
				}
				continue
			}

			if opts.OnStart != nil {
				opts.OnStart(patch)
			}

			if err := tx.Exec(ctx, patch.Name, nil); err != nil {
				return currentVersion, err
			}

			currentVersion.Version = patch.Version
			if err := tx.Exec(ctx, "tory.update-db-version", Args{"version": currentVersion.Version}); err != nil {
				return currentVersion, err
			}

			if opts.OnFinish != nil {
				opts.OnFinish(patch)
			}
		}

		return currentVersion, nil
	})
}

func parseVersion(str string) int {
	number, _, found := strings.Cut(str, "-")
	if !found {
		return -1
	}

	version, err := strconv.ParseInt(strings.TrimLeft(number, "0"), 10, 64)
	if err != nil {
		return -1
	}

	return int(version)
}
