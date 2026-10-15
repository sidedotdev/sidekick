package git

import "sidekick/internal/testfixture"

var iddRepositoriesFixture = testfixture.Git(`
mkdir host
git -C host init -q -b main
printf 'base intent\n' > host/intent.md
git -C host add intent.md
git -C host commit -qm 'Initial commit'
git -C host checkout -qb side/idd-intent
printf 'authored intent\n' > host/intent.md
git -C host commit -qam 'Authored intent'
git -C host checkout -q main

git clone -q --no-hardlinks host child
git -C child remote set-url origin ../host
git -C child checkout -qb side/idd-child origin/side/idd-intent
printf 'child work\n' > child/child.txt
git -C child add child.txt
git -C child commit -qm 'Child work'

git -C host checkout -q side/idd-intent
printf 'updated intent\n' > host/intent.md
git -C host commit -qam 'Later intent edit'
git -C host checkout -q main
`)
