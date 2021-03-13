package worker2

import (
	"code.cloudfoundry.org/lager"
	"github.com/concourse/concourse/atc/db"
	"github.com/concourse/concourse/atc/runtime"
	"github.com/cppforlife/go-semi-semantic/version"
)

type Pool struct {
	Factory
	DB DB

	WorkerVersion version.Version
}

// TODO: re-implement #6635 (wait for worker matching strategy)
type PoolCallback interface {
	WaitingForWorker(lager.Logger)
}

func (pool Pool) FindOrSelectWorker(
	logger lager.Logger,
	owner db.ContainerOwner,
	containerSpec runtime.ContainerSpec,
	workerSpec Spec,
	strategy PlacementStrategy,
	callback PoolCallback,
) (runtime.Worker, error) {
	worker, compatibleWorkers, found, err := pool.findWorkerForContainer(logger, owner, workerSpec)
	if err != nil {
		return nil, err
	}
	if !found {
		worker, err = strategy.Choose(logger, pool, compatibleWorkers, containerSpec)
		if err != nil {
			return nil, err
		}
	}

	return pool.Factory.NewWorker(logger, pool, worker), nil
}

func (pool Pool) ReleaseWorker(logger lager.Logger, containerSpec runtime.ContainerSpec, worker runtime.Worker, strategy PlacementStrategy) {
}

func (pool Pool) FindWorkerForContainer(logger lager.Logger, owner db.ContainerOwner, workerSpec Spec) (runtime.Worker, bool, error) {
	worker, _, found, err := pool.findWorkerForContainer(logger, owner, workerSpec)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	return pool.Factory.NewWorker(logger, pool, worker), true, nil
}

func (pool Pool) findWorkerForContainer(logger lager.Logger, owner db.ContainerOwner, workerSpec Spec) (db.Worker, []db.Worker, bool, error) {
	workersWithContainer, err := pool.DB.WorkerFactory.FindWorkersForContainerByOwner(owner)
	if err != nil {
		return nil, nil, false, err
	}

	compatibleWorkers, err := pool.allCompatible(logger, workerSpec)
	if err != nil {
		return nil, nil, false, err
	}

	for _, w := range workersWithContainer {
		for _, c := range compatibleWorkers {
			if w.Name() == c.Name() {
				return w, compatibleWorkers, true, nil
			}
		}
	}

	return nil, compatibleWorkers, false, nil
}

func (pool Pool) FindWorker(logger lager.Logger, name string) (runtime.Worker, bool, error) {
	worker, found, err := pool.DB.WorkerFactory.GetWorker(name)
	if err != nil {
		logger.Error("failed-to-get-worker", err)
		return nil, false, err
	}
	if !found {
		logger.Info("worker-not-found", lager.Data{"worker": name})
		return nil, false, nil
	}
	return pool.NewWorker(logger, pool, worker), true, nil
}

func (pool Pool) LocateVolume(logger lager.Logger, teamID int, handle string) (runtime.Volume, runtime.Worker, bool, error) {
	logger = logger.Session("worker-for-volume", lager.Data{"handle": handle, "team-id": teamID})
	team := pool.DB.TeamFactory.GetByID(teamID)

	dbWorker, found, err := team.FindWorkerForVolume(handle)
	if err != nil {
		logger.Error("failed-to-find-worker", err)
		return nil, nil, false, err
	}
	if !found {
		return nil, nil, false, nil
	}
	if !pool.isWorkerVersionCompatible(logger, dbWorker) {
		return nil, nil, false, nil
	}

	logger = logger.WithData(lager.Data{"worker": dbWorker.Name()})
	logger.Debug("found-volume-on-worker")

	worker := pool.NewWorker(logger, pool, dbWorker)

	volume, found, err := worker.LookupVolume(logger, handle)
	if err != nil {
		logger.Error("failed-to-lookup-volume", err)
		return nil, nil, false, err
	}
	if !found {
		logger.Info("volume-disappeared-from-worker")
		return nil, nil, false, nil
	}

	return volume, worker, true, nil
}

func (pool Pool) LocateContainer(logger lager.Logger, teamID int, handle string) (runtime.Container, runtime.Worker, bool, error) {
	logger = logger.Session("worker-for-container", lager.Data{"handle": handle, "team-id": teamID})
	team := pool.DB.TeamFactory.GetByID(teamID)

	dbWorker, found, err := team.FindWorkerForContainer(handle)
	if err != nil {
		logger.Error("failed-to-find-worker", err)
		return nil, nil, false, err
	}
	if !found {
		return nil, nil, false, nil
	}
	if !pool.isWorkerVersionCompatible(logger, dbWorker) {
		return nil, nil, false, nil
	}

	logger = logger.WithData(lager.Data{"worker": dbWorker.Name()})
	logger.Debug("found-volume-on-worker")

	worker := pool.NewWorker(logger, pool, dbWorker)

	container, found, err := worker.LookupContainer(logger, handle)
	if err != nil {
		logger.Error("failed-to-lookup-container", err)
		return nil, nil, false, err
	}
	if !found {
		logger.Info("container-disappeared-from-worker")
		return nil, nil, false, nil
	}

	return container, worker, true, nil
}

func (pool Pool) allCompatible(logger lager.Logger, spec Spec) ([]db.Worker, error) {
	workers, err := pool.DB.WorkerFactory.Workers()
	if err != nil {
		return nil, err
	}

	if len(workers) == 0 {
		return nil, ErrNoWorkers
	}

	var compatibleTeamWorkers []db.Worker
	var compatibleGeneralWorkers []db.Worker
	for _, worker := range workers {
		compatible := pool.isWorkerCompatible(logger, worker, spec)
		if compatible {
			if worker.TeamID() != 0 {
				compatibleTeamWorkers = append(compatibleTeamWorkers, worker)
			} else {
				compatibleGeneralWorkers = append(compatibleGeneralWorkers, worker)
			}
		}
	}

	if len(compatibleTeamWorkers) != 0 {
		// XXX(aoldershaw): if there is a team worker that is compatible but is
		// rejected by the strategy, shouldn't we fallback to general workers?
		return compatibleTeamWorkers, nil
	}

	if len(compatibleGeneralWorkers) != 0 {
		return compatibleGeneralWorkers, nil
	}

	return nil, NoCompatibleWorkersError{
		Spec:          spec,
		WorkerVersion: pool.WorkerVersion,
	}
}

func (pool Pool) isWorkerVersionCompatible(logger lager.Logger, dbWorker db.Worker) bool {
	workerVersion := dbWorker.Version()
	logger = logger.Session("check-version", lager.Data{
		"want-worker-version": pool.WorkerVersion.String(),
		"have-worker-version": workerVersion,
	})

	if workerVersion == nil {
		logger.Info("empty-worker-version")
		return false
	}

	v, err := version.NewVersionFromString(*workerVersion)
	if err != nil {
		logger.Error("failed-to-parse-version", err)
		return false
	}

	switch v.Release.Compare(pool.WorkerVersion.Release) {
	case 0:
		return true
	case -1:
		return false
	default:
		if v.Release.Components[0].Compare(pool.WorkerVersion.Release.Components[0]) == 0 {
			return true
		}

		return false
	}
}

func (pool Pool) isWorkerCompatible(logger lager.Logger, worker db.Worker, spec Spec) bool {
	if !pool.isWorkerVersionCompatible(logger, worker) {
		return false
	}

	if worker.TeamID() != 0 {
		if spec.TeamID != worker.TeamID() {
			return false
		}
	}

	if spec.ResourceType != "" {
		matchedType := false
		for _, t := range worker.ResourceTypes() {
			if t.Type == spec.ResourceType {
				matchedType = true
				break
			}
		}

		if !matchedType {
			return false
		}
	}

	if spec.Platform != "" {
		if spec.Platform != worker.Platform() {
			return false
		}
	}

	if !tagsMatch(worker, spec.Tags) {
		return false
	}

	return true
}

func tagsMatch(worker db.Worker, tags []string) bool {
	if len(worker.Tags()) > 0 && len(tags) == 0 {
		return false
	}

	hasTag := func(tag string) bool {
		for _, wtag := range worker.Tags() {
			if wtag == tag {
				return true
			}
		}
		return false
	}

	for _, tag := range tags {
		if !hasTag(tag) {
			return false
		}
	}
	return true
}
