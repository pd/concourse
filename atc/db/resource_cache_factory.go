package db

import (
	"database/sql"
	"encoding/json"

	sq "github.com/Masterminds/squirrel"
	"github.com/concourse/concourse/atc"
	"github.com/concourse/concourse/atc/db/lock"
)

//go:generate counterfeiter . ResourceCacheFactory

type ResourceCacheFactory interface {
	FindOrCreateResourceCache(
		resourceCacheUser ResourceCacheUser,
		resourceTypeName string,
		version atc.Version,
		source atc.Source,
		params atc.Params,
		customTypeResourceCache UsedResourceCache,
	) (UsedResourceCache, error)

	// changing resource cache to interface to allow updates on object is not feasible.
	// Since we need to pass it recursively in ResourceConfig.
	// Also, metadata will be available to us before we create resource cache so this
	// method can be removed at that point. See  https://github.com/concourse/concourse/issues/534
	UpdateResourceCacheMetadata(UsedResourceCache, []atc.MetadataField) error
	ResourceCacheMetadata(UsedResourceCache) (ResourceConfigMetadataFields, error)

	FindResourceCacheByID(id int) (UsedResourceCache, bool, error)
}

type resourceCacheFactory struct {
	conn        Conn
	lockFactory lock.LockFactory
}

func NewResourceCacheFactory(conn Conn, lockFactory lock.LockFactory) ResourceCacheFactory {
	return &resourceCacheFactory{
		conn:        conn,
		lockFactory: lockFactory,
	}
}

func (f *resourceCacheFactory) FindOrCreateResourceCache(
	resourceCacheUser ResourceCacheUser,
	resourceTypeName string,
	version atc.Version,
	source atc.Source,
	params atc.Params,
	customTypeResourceCache UsedResourceCache,
) (UsedResourceCache, error) {
	rc := &resourceConfig{
		lockFactory: f.lockFactory,
		conn:        f.conn,
	}

	tx, err := f.conn.Begin()
	if err != nil {
		return nil, err
	}
	defer Rollback(tx)

	var parentID int
	var parentColumnName string
	if customTypeResourceCache != nil {
		parentColumnName = "resource_cache_id"
		rc.createdByResourceCache = customTypeResourceCache
		parentID = rc.createdByResourceCache.ID()
	} else {
		// Uses a base resource type
		parentColumnName = "base_resource_type_id"
		var err error
		var found bool
		rc.createdByBaseResourceType, found, err = BaseResourceType{Name: resourceTypeName}.Find(tx)
		if err != nil {
			return nil, err
		}

		if !found {
			return nil, BaseResourceTypeNotFoundError{Name: resourceTypeName}
		}

		parentID = rc.CreatedByBaseResourceType().ID
	}

	found := true
	err = psql.Select("id", "last_referenced").
		From("resource_configs").
		Where(sq.Eq{
			parentColumnName: parentID,
			"source_hash":    mapHash(source),
		}).
		Suffix("FOR UPDATE").
		RunWith(tx).
		QueryRow().
		Scan(&rc.id, &rc.lastReferenced)
	if err != nil {
		if err == sql.ErrNoRows {
			found = false
		} else {
			return nil, err
		}
	}

	if !found {
		hash := mapHash(source)

		err := psql.Insert("resource_configs").
			Columns(
				parentColumnName,
				"source_hash",
			).
			Values(
				parentID,
				hash,
			).
			Suffix(`
				ON CONFLICT (`+parentColumnName+`, source_hash) DO UPDATE SET
					`+parentColumnName+` = ?,
					source_hash = ?
				RETURNING id, last_referenced
			`, parentID, hash).
			RunWith(tx).
			QueryRow().
			Scan(&rc.id, &rc.lastReferenced)
		if err != nil {
			return nil, err
		}
	}

	marshaledVersion, _ := json.Marshal(version)
	cacheVersion := string(marshaledVersion)

	found = true
	var id int
	err = psql.Select("id").
		From("resource_caches").
		Where(sq.Eq{
			"resource_config_id": rc.id,
			"params_hash":        paramsHash(params),
		}).
		Where(sq.Expr("version_md5 = md5(?)", cacheVersion)).
		Suffix("FOR SHARE").
		RunWith(tx).
		QueryRow().
		Scan(&id)
	if err != nil {
		if err == sql.ErrNoRows {
			found = false
		} else {
			return nil, err
		}
	}

	if !found {
		err = psql.Insert("resource_caches").
			Columns(
				"resource_config_id",
				"version",
				"version_md5",
				"params_hash",
			).
			Values(
				rc.id,
				cacheVersion,
				sq.Expr("md5(?)", cacheVersion),
				paramsHash(params),
			).
			Suffix(`
				ON CONFLICT (resource_config_id, version_md5, params_hash) DO UPDATE SET
				resource_config_id = EXCLUDED.resource_config_id,
				version = EXCLUDED.version,
				version_md5 = EXCLUDED.version_md5,
				params_hash = EXCLUDED.params_hash
				RETURNING id
			`).
			RunWith(tx).
			QueryRow().
			Scan(&id)
		if err != nil {
			return nil, err
		}
	}

	cols := resourceCacheUser.SQLMap()
	cols["resource_cache_id"] = id

	found = true
	var resourceCacheUseExists int
	err = psql.Select("1").
		From("resource_cache_uses").
		Where(sq.Eq(cols)).
		RunWith(tx).
		QueryRow().
		Scan(&resourceCacheUseExists)
	if err != nil {
		if err == sql.ErrNoRows {
			found = false
		} else {
			return nil, err
		}
	}

	if !found {
		_, err = psql.Insert("resource_cache_uses").
			SetMap(cols).
			RunWith(tx).
			Exec()
	}

	err = tx.Commit()
	if err != nil {
		return nil, err
	}

	return &usedResourceCache{
		id:             id,
		resourceConfig: rc,
		version:        version,

		lockFactory: f.lockFactory,
		conn:        f.conn,
	}, nil
}

func (f *resourceCacheFactory) UpdateResourceCacheMetadata(resourceCache UsedResourceCache, metadata []atc.MetadataField) error {
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = psql.Update("resource_caches").
		Set("metadata", metadataJSON).
		Where(sq.Eq{"id": resourceCache.ID()}).
		RunWith(f.conn).
		Exec()
	return err
}

func (f *resourceCacheFactory) ResourceCacheMetadata(resourceCache UsedResourceCache) (ResourceConfigMetadataFields, error) {
	var metadataJSON sql.NullString
	err := psql.Select("metadata").
		From("resource_caches").
		Where(sq.Eq{"id": resourceCache.ID()}).
		RunWith(f.conn).
		QueryRow().
		Scan(&metadataJSON)
	if err != nil {
		return nil, err
	}

	var metadata []ResourceConfigMetadataField
	if metadataJSON.Valid {
		err = json.Unmarshal([]byte(metadataJSON.String), &metadata)
		if err != nil {
			return nil, err
		}
	}

	return metadata, nil
}

func (f *resourceCacheFactory) FindResourceCacheByID(id int) (UsedResourceCache, bool, error) {
	tx, err := f.conn.Begin()
	if err != nil {
		return nil, false, err
	}

	defer Rollback(tx)

	return findResourceCacheByID(tx, id, f.lockFactory, f.conn)
}

func findResourceCacheByID(tx Tx, resourceCacheID int, lock lock.LockFactory, conn Conn) (UsedResourceCache, bool, error) {
	var rcID int
	var versionBytes string

	err := psql.Select("resource_config_id", "version").
		From("resource_caches").
		Where(sq.Eq{"id": resourceCacheID}).
		RunWith(tx).
		QueryRow().
		Scan(&rcID, &versionBytes)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}

	var version atc.Version
	err = json.Unmarshal([]byte(versionBytes), &version)
	if err != nil {
		return nil, false, err
	}

	rc, found, err := findResourceConfigByID(tx, rcID, lock, conn)
	if err != nil {
		return nil, false, err
	}

	if !found {
		return nil, false, nil
	}

	usedResourceCache := &usedResourceCache{
		id:             resourceCacheID,
		version:        version,
		resourceConfig: rc,
		lockFactory:    lock,
		conn:           conn,
	}

	return usedResourceCache, true, nil
}
