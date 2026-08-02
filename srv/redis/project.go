package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"

	"sidekick/domain"
	"sidekick/srv"

	"github.com/redis/go-redis/v9"
)

var _ domain.ProjectStorage = (*Storage)(nil)

func projectsSetKey(workspaceId string) string {
	return fmt.Sprintf("%s:projects", workspaceId)
}

func (s Storage) PersistProject(ctx context.Context, project domain.Project) error {
	if project.WorkspaceId == "" {
		return errors.New("workspaceId cannot be empty")
	}
	if project.Id == "" {
		return errors.New("project.Id cannot be empty")
	}

	projectJson, err := json.Marshal(project)
	if err != nil {
		log.Println("Failed to convert project record to JSON: ", err)
		return err
	}

	key := fmt.Sprintf("%s:%s", project.WorkspaceId, project.Id)
	err = s.Client.Set(ctx, key, projectJson, 0).Err()
	if err != nil {
		log.Println("Failed to persist project to Redis: ", err)
		return err
	}

	err = s.Client.SAdd(ctx, projectsSetKey(project.WorkspaceId), project.Id).Err()
	if err != nil {
		log.Println("Failed to add project id to workspace projects set: ", err)
		return err
	}

	return nil
}

func (s Storage) GetProject(ctx context.Context, workspaceId, projectId string) (domain.Project, error) {
	key := fmt.Sprintf("%s:%s", workspaceId, projectId)
	projectRecord, err := s.Client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return domain.Project{}, srv.ErrNotFound
		}
		return domain.Project{}, err
	}
	var project domain.Project
	err = json.Unmarshal([]byte(projectRecord), &project)
	if err != nil {
		return domain.Project{}, err
	}
	return project, nil
}

// GetProjects retrieves all Projects in a workspace, sorted by priority
// bucket (most urgent first) then rank.
func (s Storage) GetProjects(ctx context.Context, workspaceId string) ([]domain.Project, error) {
	projectIds, err := s.Client.SMembers(ctx, projectsSetKey(workspaceId)).Result()
	if err != nil {
		return nil, err
	}

	projectKeys := make([]string, len(projectIds))
	for i, projectId := range projectIds {
		projectKeys[i] = fmt.Sprintf("%s:%s", workspaceId, projectId)
	}

	var projectJsons []interface{}
	if len(projectKeys) > 0 {
		projectJsons, err = s.Client.MGet(ctx, projectKeys...).Result()
		if err != nil {
			log.Println("Failed to get projects from Redis: ", err)
			return nil, err
		}
	}

	var projects []domain.Project
	for _, projectJson := range projectJsons {
		if projectJson == nil {
			continue
		}
		var project domain.Project
		err = json.Unmarshal([]byte(projectJson.(string)), &project)
		if err != nil {
			log.Println("Failed to unmarshal project: ", err)
			continue
		}
		projects = append(projects, project)
	}

	sort.Slice(projects, func(i, j int) bool {
		bucketI, bucketJ := projects[i].Priority.SortBucket(), projects[j].Priority.SortBucket()
		if bucketI != bucketJ {
			return bucketI < bucketJ
		}
		if projects[i].Rank != projects[j].Rank {
			return projects[i].Rank < projects[j].Rank
		}
		return projects[i].Created.Before(projects[j].Created)
	})

	return projects, nil
}

func (s Storage) DeleteProject(ctx context.Context, workspaceId, projectId string) error {
	key := fmt.Sprintf("%s:%s", workspaceId, projectId)
	deleted, err := s.Client.Del(ctx, key).Result()
	if err != nil {
		log.Println("Failed to delete project from Redis: ", err)
		return err
	}
	if deleted == 0 {
		return srv.ErrNotFound
	}

	err = s.Client.SRem(ctx, projectsSetKey(workspaceId), projectId).Err()
	if err != nil {
		log.Println("Failed to remove project id from workspace projects set: ", err)
		return err
	}

	return nil
}
