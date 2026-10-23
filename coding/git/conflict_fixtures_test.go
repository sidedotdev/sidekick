package git

import (
	"fmt"
	"strings"

	"sidekick/internal/testfixture"
)

var conflictingBranchesFixture = testfixture.Git(`
git init -q -b main
printf '%s' 'base content' > conflict.txt
git add conflict.txt
git commit -qm 'Initial commit'
git checkout -qb feature
printf '%s' 'feature content' > conflict.txt
git commit -qam 'Feature change'
git checkout -q main
printf '%s' 'main content' > conflict.txt
git commit -qam 'Main change'
`)

var farApartConflictFixture = func() *testfixture.Snapshot {
	base := numberedLines(20)
	feature := append([]string{}, base...)
	feature[0] = "feature line1"
	main := append([]string{}, base...)
	main[0] = "main line1"
	content := func(lines []string) string {
		return shellQuote(strings.Join(lines, "\n") + "\n")
	}
	return testfixture.Git(fmt.Sprintf(`
git init -q -b main
printf '%%s' %s > conflict.txt
git add conflict.txt
git commit -qm 'Initial commit'
git checkout -qb feature
printf '%%s' %s > conflict.txt
git commit -qam 'Feature change'
git checkout -q main
printf '%%s' %s > conflict.txt
git commit -qam 'Main change'
if git merge feature; then
	exit 1
fi
test -f .git/MERGE_HEAD
test -n "$(git ls-files --unmerged)"
`, content(base), content(feature), content(main)))
}()

var mergeConflictFixture = testfixture.Git(`
git init -q -b main
printf 'base\n' > conflict.txt
git add conflict.txt
git commit -qm 'initial commit'
git checkout -qb feature
printf 'feature change\n' > conflict.txt
git commit -qam 'feature change'
git checkout -q main
printf 'main change\n' > conflict.txt
git commit -qam 'main change'
if git merge feature; then
	exit 1
fi
test -f .git/MERGE_HEAD
test -n "$(git ls-files --unmerged)"
`)
