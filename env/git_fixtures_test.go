package env

import "sidekick/internal/testfixture"

var initialRepoFixture = testfixture.Git(`
git init -q -b main
git config user.name 'Test User'
git config user.email test@example.com
git config commit.gpgsign false
git commit -qm 'Initial commit' --allow-empty
`)

var hibernationRepoFixture = testfixture.Git(`
git init -q -b main
git config user.name Test
git config user.email test@test.com
git config commit.gpgsign false
export GIT_AUTHOR_NAME=Test GIT_AUTHOR_EMAIL=test@test.com
export GIT_COMMITTER_NAME=Test GIT_COMMITTER_EMAIL=test@test.com
printf '# Test' > README.md
git add README.md
git commit -qm 'initial commit'
`)

var walkSeedFixture = testfixture.Git(`
git init -q -b main
git config user.name Test
git config user.email test@example.com
git config commit.gpgsign false
`)
