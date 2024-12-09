package sidekick

import (
	"context"
	"errors"
	"sidekick/secret_manager"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
)

const GithubAccessTokenSecretName = "GITHUB_ACCESS_TOKEN"

type GithubCloneRepoActivityInput struct {
	RepoURL   string
	ClonePath string
	Secrets   secret_manager.SecretManagerContainer
}

func GithubCloneRepoActivity(ctx context.Context, input GithubCloneRepoActivityInput) error {
	token, err := input.Secrets.SecretManager.GetSecret(GithubAccessTokenSecretName)
	if err != nil {
		return err
	}

	if input.RepoURL == "" || input.ClonePath == "" {
		return errors.New("both RepoURL and ClonePath are required")
	}

	_, err = git.PlainClone(input.ClonePath, false, &git.CloneOptions{
		URL: input.RepoURL,
		Auth: &http.BasicAuth{
			Username: "dummy", // This can be anything except an empty string
			Password: token,
		},
	})

	if err != nil {
		return err
	}

	return nil
}
