package diffanalysis

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInterdiffWithBaseChange_CompetingReplacement(t *testing.T) {
	t.Parallel()

	base := "func request() {\n\treturn nil, err\n}\n"
	prior := "func request() {\n\treturn nil, classifyBedrockError(fmt.Errorf(\"bedrock: ConverseStream: %w\", err))\n}\n"
	upstream := "func request() {\n\treturn nil, classifyBadRequestError(err, fmt.Errorf(\"bedrock: ConverseStream: %w\", err))\n}\n"

	for _, tc := range []struct {
		name    string
		current string
	}{
		{name: "reviewed replacement retained", current: prior},
		{name: "new resolution", current: strings.Replace(prior, "classifyBedrockError", "classifyProviderError", 1)},
		{name: "upstream replacement accepted", current: upstream},
		{name: "reviewed replacement reverted", current: base},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo := newInterdiffFuzzRepo(t)
			tree := func(content string) string {
				return repo.tree(interdiffFuzzTree{"provider.go": {mode: "100644", content: content}})
			}
			diff := func(from, to string) string {
				return repo.git("", "diff-tree", "-r", "-p", "--no-commit-id", "--no-ext-diff",
					"--no-textconv", "--no-color", "--full-index", from, to)
			}
			oldBase, reviewed, newBase, current := tree(base), tree(prior), tree(upstream), tree(tc.current)
			oldReview, newReview, baseChange := diff(oldBase, reviewed), diff(newBase, current), diff(oldBase, newBase)
			repo.assertApplies(oldBase, reviewed, oldReview)
			repo.assertApplies(newBase, current, newReview)
			repo.assertApplies(oldBase, newBase, baseChange)

			patch, err := InterdiffWithBaseChange(oldReview, newReview, baseChange)
			require.NoError(t, err)
			repo.assertApplies(reviewed, current, patch)
			if tc.current == prior {
				require.Empty(t, patch)
			} else {
				require.NotEmpty(t, patch)
			}
		})
	}
}

func TestInterdiffWithBaseChange_CapturedReview(t *testing.T) {
	t.Parallel()

	file, err := os.Open("testdata/overlapping_review.json.gz")
	require.NoError(t, err)
	defer file.Close()
	reader, err := gzip.NewReader(file)
	require.NoError(t, err)
	defer reader.Close()
	type fileContent struct {
		Mode    string `json:"mode"`
		Content string `json:"content"`
	}
	var fixture struct {
		Prior            string                 `json:"prior"`
		Current          string                 `json:"current"`
		BaseChange       string                 `json:"baseChange"`
		ExpectedBaseline map[string]fileContent `json:"expectedBaseline"`
		ExpectedCurrent  map[string]fileContent `json:"expectedCurrent"`
	}
	require.NoError(t, json.NewDecoder(reader).Decode(&fixture))
	require.NotEmpty(t, fixture.Prior)
	require.NotEmpty(t, fixture.Current)
	require.NotEmpty(t, fixture.BaseChange)
	require.NotEmpty(t, fixture.ExpectedBaseline)
	require.NotEmpty(t, fixture.ExpectedCurrent)
	baseline, current := interdiffFuzzTree{}, interdiffFuzzTree{}
	for path, file := range fixture.ExpectedBaseline {
		baseline[path] = interdiffFuzzFile{mode: file.Mode, content: file.Content}
	}
	for path, file := range fixture.ExpectedCurrent {
		current[path] = interdiffFuzzFile{mode: file.Mode, content: file.Content}
	}

	patch, err := InterdiffWithBaseChange(fixture.Prior, fixture.Current, fixture.BaseChange)
	require.NoError(t, err)
	require.NotEmpty(t, patch)
	repo := newInterdiffFuzzRepo(t)
	baselineTree, currentTree := repo.tree(baseline), repo.tree(current)
	repo.assertApplies(baselineTree, currentTree, patch)

	files, err := ParseUnifiedDiff(patch)
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, file := range files {
		if before, exists := baseline[file.NewPath]; exists {
			require.NotEqual(t, before, current[file.NewPath],
				"unchanged file %s must not appear in the comparison", file.NewPath)
		}
	}
	for _, path := range []string{
		"frontend/src/components/FlowActionItem.vue",
		"frontend/src/lib/models.ts",
		"llm2/bedrock_provider.go",
		"persisted_ai/chat_stream.go",
	} {
		require.Equal(t, baseline[path], current[path], "unchanged overlap: %s", path)
		require.NotContains(t, patch, "diff --git a/"+path+" ")
	}

	changed := strings.Replace(fixture.Current, "+\t\treturn nil, classifyBedrockError(",
		"+\t\treturn nil, classifyAfterReviewError(", 1)
	require.NotEqual(t, fixture.Current, changed)
	revised, err := InterdiffWithBaseChange(fixture.Prior, changed, fixture.BaseChange)
	require.NoError(t, err)
	require.NotEqual(t, patch, revised)
	provider := current["llm2/bedrock_provider.go"]
	provider.content = strings.Replace(provider.content, "return nil, classifyBedrockError(",
		"return nil, classifyAfterReviewError(", 1)
	require.NotEqual(t, current["llm2/bedrock_provider.go"], provider)
	current["llm2/bedrock_provider.go"] = provider
	repo.assertApplies(baselineTree, repo.tree(current), revised)
}
