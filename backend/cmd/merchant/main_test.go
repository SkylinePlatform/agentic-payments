package main

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOnlyTheRealShopCanBeAskedForALiveCatalogue is the whole of what
// -catalogue-live accepts, and the two refusals matter more than the acceptance.
//
// **A value this build does not know stops the process.** Read as "off" instead,
// `-catalogue-live dummyjsn` would start a merchant selling the committed file
// under a label saying it was live — the same class of mistake `-interpreter
// gemini` refuses to make when it has no key, and for the same reason: a
// demonstration nobody can attribute a screenshot to.
//
// **The recording is not a value.** shop.Snapshot satisfies the same interface
// and exists so that tests of a live catalogue can run without a socket, and a
// run that served it while calling itself live would be indistinguishable from
// one that reached the shop. Naming it here is the one way that could happen, so
// this asserts it cannot — which is a claim about the switch and not about
// whether somebody remembered.
func TestOnlyTheRealShopCanBeAskedForALiveCatalogue(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		flag    string
		wantErr bool
		live    bool
		why     string
	}{
		{
			name: "the default", flag: "", live: false,
			why: "`make demo` reaches no network, which is what makes the golden numbers in every " +
				"screenshot attributable",
		},
		{
			name: "the shop", flag: "dummyjson", live: true,
			why: "a table where every value was refused would prove the refusals below and nothing else",
		},
		{
			name: "a typo", flag: "dummyjsn", wantErr: true,
			why: "read as off, this would start a merchant selling the committed file while a " +
				"person watching had typed the command that says otherwise",
		},
		{
			name: "the recording", flag: "snapshot", wantErr: true,
			why: "a run that said live and served bytes committed in this repository is the " +
				"screenshot nobody can attribute; the fixture is for tests and nothing else",
		},
		{
			name: "a shop by URL", flag: "https://dummyjson.com", wantErr: true,
			why: "a URL would make this a way to point a merchant at anything speaking the same " +
				"dialect, which is a demonstration against a shop nobody reviewed. A name is " +
				"reviewable; adding one is a source change with a fetcher behind it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fetcher, err := fetcherFor(tc.flag)
			if tc.wantErr {
				require.Error(t, err, tc.why)
				assert.Nil(t, fetcher, "an error and a fetcher is a caller being invited to use one it was just told about")
				return
			}

			require.NoError(t, err, tc.why)
			if !tc.live {
				assert.Nil(t, fetcher, tc.why)
				return
			}
			require.NotNil(t, fetcher, tc.why)
			assert.Contains(t, fetcher.Name(), "dummyjson.com",
				"the merchant prints this at start-up, and a viewer looking at stock that is in no "+
					"file here has nothing else telling them where it came from")
		})
	}
}

// TestMakeDemoLiveNamesAShopThisBuildAccepts is cmd/agent's
// TestMakeDemoLiveNamesAFlagThisBuildAccepts, one process along.
//
// Nothing else ties `dummyjson`, spelled out as a literal in the Makefile, to
// the constant this binary switches on. A typo in either place would reach
// nobody until a person ran `make demo-live` by hand and read a refusal instead
// of a banner — and the refusal is the *good* outcome of that mistake; the bad
// one was a silent fallback, which is exactly what fetcherFor refuses to do.
//
// It goes one step past comparing two strings: the extracted value is handed to
// fetcherFor itself, the same call run makes, so a value that matched the
// constant's spelling but that fetcherFor no longer accepted would still fail.
//
// Reading a Makefile is a text operation, not an invocation of make, so this
// costs nothing beyond `go test` and keeps `make check` Go-only.
func TestMakeDemoLiveNamesAShopThisBuildAccepts(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../Makefile")
	require.NoError(t, err, "this test has to read the actual Makefile, or it is pinning nothing")

	recipe := regexp.MustCompile(`(?m)^demo-live:.*\n(?:\t.*\n)*`).FindString(string(raw))
	require.NotEmpty(t, recipe, "no demo-live target found in the Makefile — has it been renamed?")

	value := regexp.MustCompile(`-append\s+merchant=-catalogue-live,(\S+)`).FindStringSubmatch(recipe)
	require.Len(t, value, 2,
		"demo-live no longer passes -append merchant=-catalogue-live,<shop>, so the live catalogue "+
			"half of that target has been dropped and the interpreter half is proving half the point")

	assert.Equal(t, catalogueLiveDummyJSON, value[1],
		"the Makefile's -append value has drifted from the constant this build switches on")

	fetcher, err := fetcherFor(value[1])
	require.NoError(t, err,
		"the Makefile names a shop that matches the constant's spelling but that fetcherFor refuses")
	assert.NotNil(t, fetcher,
		"the Makefile's value was accepted and produced no shop, so `make demo-live` would run the "+
			"committed file under a label saying it did not")
}

// TestMakeDemoDoesNotReachAShop is the other half, and it is the one `make demo`
// depends on.
//
// The plain target has to stay reproducible on every machine: no process it
// starts reads a key or reaches a network, which is what makes the golden
// numbers in every screenshot in this repository attributable. A
// -catalogue-live that arrived in `demo`'s recipe, or in deploy/demo.json's
// merchant entry, would end that quietly — the demonstration would still run,
// and would show different stock depending on whether a shop was up.
func TestMakeDemoDoesNotReachAShop(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../Makefile")
	require.NoError(t, err, "this test has to read the actual Makefile, or it is pinning nothing")

	recipe := regexp.MustCompile(`(?m)^demo:.*\n(?:\t.*\n)*`).FindString(string(raw))
	require.NotEmpty(t, recipe, "no demo target found in the Makefile — has it been renamed?")
	assert.NotContains(t, recipe, "-catalogue-live",
		"`make demo` would fetch a shop, so the numbers in every committed screenshot would depend "+
			"on somebody else's stock on the day")

	raw, err = os.ReadFile("../../../deploy/demo.json")
	require.NoError(t, err, "the manifest is the other place the flag could arrive")

	// Decoded rather than grepped, and the difference is not fastidiousness:
	// that file's $comment explains at length why this flag is *not* in it, so a
	// search of the text finds the argument and reports it as the thing the
	// argument forbids. What matters is what a process would be started with.
	//
	// Decoded here rather than through demo.Load, which would put internal/demo
	// in this binary's test import graph to learn one fact about a JSON file.
	var manifest struct {
		Processes []struct {
			Name string   `json:"name"`
			Args []string `json:"args"`
		} `json:"processes"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest), "the manifest has to parse, or this test is reading nothing")
	require.NotEmpty(t, manifest.Processes, "no processes were decoded, so the loop below would pass over an empty list")

	for _, p := range manifest.Processes {
		assert.NotContains(t, p.Args, "-catalogue-live",
			"%s is started with a live catalogue by the manifest itself, which reaches `make demo` "+
				"as well — that is the drift `-append` exists to avoid, and that file's own $comment "+
				"argues it", p.Name)
	}
}
