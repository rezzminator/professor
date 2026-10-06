package deps

import (
	"reflect"
	"strings"
	"testing"
)

func TestEnvironmentWithReplacesKeyOnce(t *testing.T) {
	t.Setenv("PFM_ENVIRONMENT_TEST", "old")
	environment := EnvironmentWith("PFM_ENVIRONMENT_TEST", "new")
	found := 0
	for _, entry := range environment {
		if strings.HasPrefix(entry, "PFM_ENVIRONMENT_TEST=") {
			found++
			if entry != "PFM_ENVIRONMENT_TEST=new" {
				t.Fatalf("entry = %q, want replacement", entry)
			}
		}
	}
	if found != 1 {
		t.Fatalf("replacement count = %d, want 1", found)
	}
}

func TestWithoutGitRepoVars(t *testing.T) {
	for _, row := range []struct {
		name          string
		environ, want []string
	}{
		{
			name: "repository variables dropped, others kept in order",
			environ: []string{
				"GIT_DIR=/x", "PATH=/bin", "GIT_WORK_TREE=/w", "GIT_INDEX_FILE=/i",
				"GIT_OBJECT_DIRECTORY=/o", "GIT_CEILING_DIRECTORIES=/c", "GIT_SSL_CAINFO=/ca",
				"GIT_SSH_COMMAND=ssh", "GIT_DIRX=keep",
			},
			want: []string{"PATH=/bin", "GIT_SSL_CAINFO=/ca", "GIT_SSH_COMMAND=ssh", "GIT_DIRX=keep"},
		},
		{
			name: "remaining repository variables dropped",
			environ: []string{
				"GIT_COMMON_DIR=/c", "GIT_ALTERNATE_OBJECT_DIRECTORIES=/a", "GIT_NAMESPACE=n",
				"GIT_IMPLICIT_WORK_TREE=1", "GIT_PREFIX=p", "GIT_SHALLOW_FILE=/s", "GIT_GRAFT_FILE=/g",
			},
			want: []string{},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			before := append([]string{}, row.environ...)
			got := WithoutGitRepoVars(row.environ)
			if !reflect.DeepEqual(got, row.want) {
				t.Fatalf("WithoutGitRepoVars() = %q, want %q", got, row.want)
			}
			if len(got) > 0 {
				got[0] = "changed"
			}
			if !reflect.DeepEqual(row.environ, before) {
				t.Fatalf("input changed: %q, want %q", row.environ, before)
			}
		})
	}
}
