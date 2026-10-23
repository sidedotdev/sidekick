# Moving-base review fixture

`overlapping_review.json.gz` contains the complete three-diff input from review
result 14 of flow `flow_3KRDXWCc2qkxoxhfjTqBVn3AsgA`:

- Previous base: `cdc8d709ce1673d5f00acbba37773146220f09c3`
- Current base: `4a889a4cd46e88a540c442b88d5dba0c2839cbeb`
- Previous reviewed tree: `fe1a1cf6c498fd822acbdb76d5103de00818c561`
- Current tree: `9a565475d10ad188a1ada96292108245313009a0`

Both review patches were applied with Git to their recorded bases to verify
their contents. The base transition includes all 480 changed files.

The expected baseline was constructed independently of the comparison code,
using Git's three-way merge with the previous reviewed tree favored for
conflicts. Two independently composable upstream changes were then restored:

- The style attribute on the existing `UserRequest.vue` content div, without
  adding that attribute to the feature's new `details` element.
- The return through `queryFlowActions` and closing brace in
  `srv/sqlite/flow_action.go`. Git's line-level conflict includes these adjacent
  upstream additions along with the feature's query replacement.

The baseline retains the reviewed Bedrock error-classifier call and the complete
reviewed `provider_auth_error.go` comment. It does not borrow resolutions from
the current tree. Expected baseline/current file contents cover the union of
paths touched by both reviews. Applying the entire generated comparison to this
baseline must produce exactly the expected current tree; unrelated upstream
file additions or modifications also fail this check.

To render and inspect the comparison:

```sh
go run ./scripts/compare_review_diffs -files \
  coding/diffanalysis/testdata/overlapping_review.json.gz
```
