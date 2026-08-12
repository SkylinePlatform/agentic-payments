package bootstrap

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const docGo = "../../backend/internal/core/generated/doc.go"

// What the two generators write, reduced to the two declarations doc.go names.
// ErrorCode comes from model.go (generate-go), Disclosable from disclosure.go
// (generate-disclosure). Both already existed; neither generator was changed.
const generatedStub = "package generated\n\n" +
	"type ErrorCode string\n\n" +
	"func Disclosable(string) []string { return nil }\n"

func buildScratch(t *testing.T, files map[string]string) (string, error) {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module scratch\n\ngo 1.26.0\n"), 0o644))
	for name, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestAnUngeneratedModelFailsInTheFileThatSaysWhy compiles the real doc.go
// twice.
//
// Once alone, which is a fresh clone: the build must fail, and the failure must
// name doc.go and both identifiers, because the whole claim of that file is
// that the error arrives there rather than in whoever imports it.
//
// Once beside a stub declaring both symbols, which is a generated tree: the
// build must succeed. Without that half the test passes just as well against a
// doc.go that can never compile — the same file with the property removed.
func TestAnUngeneratedModelFailsInTheFileThatSaysWhy(t *testing.T) {
	t.Parallel()

	body, err := os.ReadFile(docGo)
	require.NoError(t, err)
	require.NotEmpty(t, body, "doc.go is empty, so this test is checking nothing")

	out, err := buildScratch(t, map[string]string{"doc.go": string(body)})
	require.Error(t, err,
		"a package with nothing generated in it compiled clean, so the missing half "+
			"would be reported against the packages that import it — an unresolvable "+
			"module path in a hand-written file, and gopls losing the standard library with it")
	assert.Contains(t, out, "doc.go", "the error has to arrive in the file whose comment is the fix")
	assert.Contains(t, out, "ErrorCode", "and name the half `make generate-go` writes")
	assert.Contains(t, out, "Disclosable", "and the half `make generate-disclosure` writes")

	out, err = buildScratch(t, map[string]string{
		"doc.go":  string(body),
		"stub.go": generatedStub,
	})
	assert.NoError(t, err, "doc.go must compile once the package has been generated: %s", out)
}
