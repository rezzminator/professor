package fakegit

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The argv words the model matches in more than one verb.
const (
	gitName = "git"
	gitHead = "HEAD"
	gitTags = "--tags"
	gitAdd  = "add"
	devNull = "/dev/null"
)

// fakeGitDefaultDate stands in for the clock: object ids stay the same from
// run to run, and the contract test dates real git's commits the same way.
const fakeGitDefaultDate = "1700000000 +0000"

// fakeGitHash is git's object id: the SHA-1 of "{kind} {size}\0{body}".
func fakeGitHash(kind string, body []byte) string {
	hash := sha1.New()
	fmt.Fprintf(hash, "%s %d\x00", kind, len(body))
	hash.Write(body)
	return hex.EncodeToString(hash.Sum(nil))
}

// fakeGitTreeID hashes the tree object git would write for the flat
// path → entry map, one tree per directory under prefix.
func fakeGitTreeID(tree map[string]fakeGitEntry, prefix string) string {
	type item struct{ mode, id string }
	children := map[string]item{}
	for path, entry := range tree {
		if !strings.HasPrefix(path, prefix) {
			continue
		}
		rest := path[len(prefix):]
		if name, _, nested := strings.Cut(rest, "/"); nested {
			children[name+"/"] = item{mode: "40000"}
		} else {
			children[rest] = item{mode: entry.Mode, id: entry.Blob}
		}
	}
	keys := make([]string, 0, len(children))
	for key := range children {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var body []byte
	for _, key := range keys {
		child, name := children[key], strings.TrimSuffix(key, "/")
		if child.mode == "40000" {
			child.id = fakeGitTreeID(tree, prefix+key)
		}
		raw, _ := hex.DecodeString(child.id)
		body = append(append(body, child.mode+" "+name+"\x00"...), raw...)
	}
	return fakeGitHash("tree", body)
}

// identity is "Name <email> date" for the author or committer role.
func (c *fakeGitCall) identity(r *fakeGitRepo, role string) (string, bool) {
	name, email := c.getenv("GIT_"+role+"_NAME"), c.getenv("GIT_"+role+"_EMAIL")
	if name == "" {
		name, _ = c.configGet(r, "user.name")
	}
	if email == "" {
		email, _ = c.configGet(r, "user.email")
	}
	date := strings.TrimPrefix(c.getenv("GIT_"+role+"_DATE"), "@")
	if date == "" {
		date = fakeGitDefaultDate
	}
	return fmt.Sprintf("%s <%s> %s", name, email, date), name != "" && email != ""
}

// writeCommit records a commit of tree on parents and returns its id.
func (c *fakeGitCall) writeCommit(
	r *fakeGitRepo,
	tree map[string]fakeGitEntry,
	parents []string,
	message string,
) (string, int) {
	author, okAuthor := c.identity(r, "AUTHOR")
	committer, okCommitter := c.identity(r, "COMMITTER")
	if !okAuthor || !okCommitter {
		return "", c.fatal("unable to auto-detect email address")
	}
	body := "tree " + fakeGitTreeID(tree, "") + "\n"
	for _, parent := range parents {
		body += "parent " + parent + "\n"
	}
	message = strings.TrimRight(message, " \n") + "\n"
	body += "author " + author + "\ncommitter " + committer + "\n\n" + message
	id := fakeGitHash("commit", []byte(body))
	subject, _, _ := strings.Cut(message, "\n")
	copied := map[string]fakeGitEntry{}
	for path, entry := range tree {
		copied[path] = entry
	}
	r.state.Commits[id] = fakeGitCommit{Tree: copied, Parents: parents, Subject: subject}
	return id, 0
}

var fakeGitHex = regexp.MustCompile(`^[0-9a-f]{4,40}$`)

// resolve names the object rev points at (an annotated tag stays a tag): HEAD,
// a full ref, a tag, a branch, a remote-tracking ref or a unique id prefix,
// with an optional ^{commit} peel. Any other revision syntax (HEAD~1, HEAD^,
// @{u}, {rev}:{path}) is outside the model: it answers not found and records
// the refusal the call returns.
func (r *fakeGitRepo) resolve(rev string) (string, bool) {
	rev, peel := strings.CutSuffix(rev, "^{commit}")
	if rev == "@" || strings.ContainsAny(rev, "~^:") || strings.Contains(rev, "@{") {
		if r.refused == nil {
			r.refused = fmt.Errorf("revision syntax %q is not modelled", rev)
		}
		return "", false
	}
	id, ok := r.resolveName(rev)
	if ok && peel {
		return r.commitOf(id)
	}
	return id, ok
}

func (r *fakeGitRepo) resolveName(rev string) (string, bool) {
	if rev == gitHead {
		head := r.head()
		if strings.HasPrefix(head, "refs/") {
			id, ok := r.state.Refs[head]
			return id, ok
		}
		return head, head != ""
	}
	for _, ref := range []string{rev, "refs/tags/" + rev, "refs/heads/" + rev, "refs/remotes/" + rev} {
		if id, ok := r.state.Refs[ref]; ok {
			return id, true
		}
	}
	if !fakeGitHex.MatchString(rev) {
		return "", false
	}
	var found []string
	for id := range r.state.Commits {
		if strings.HasPrefix(id, rev) {
			found = append(found, id)
		}
	}
	for id := range r.state.Tags {
		if strings.HasPrefix(id, rev) {
			found = append(found, id)
		}
	}
	if len(found) != 1 {
		return "", false
	}
	return found[0], true
}

// commitOf peels an annotated tag to its commit.
func (r *fakeGitRepo) commitOf(id string) (string, bool) {
	if target, ok := r.state.Tags[id]; ok {
		id = target
	}
	_, ok := r.state.Commits[id]
	return id, ok
}

func (r *fakeGitRepo) resolveCommit(rev string) (string, bool) {
	id, ok := r.resolve(rev)
	if !ok {
		return "", false
	}
	return r.commitOf(id)
}

// ancestors maps every commit reachable from id to its distance from id.
func (r *fakeGitRepo) ancestors(id string) map[string]int {
	distance := map[string]int{id: 0}
	queue := []string{id}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, parent := range r.state.Commits[current].Parents {
			if _, seen := distance[parent]; !seen {
				distance[parent] = distance[current] + 1
				queue = append(queue, parent)
			}
		}
	}
	return distance
}

// headTree is the tree HEAD's commit holds, empty on an unborn branch.
func (r *fakeGitRepo) headTree() map[string]fakeGitEntry {
	id, ok := r.resolve(gitHead)
	if !ok {
		return map[string]fakeGitEntry{}
	}
	return r.state.Commits[id].Tree
}

// short abbreviates an id to git's default seven characters.
func fakeGitShort(id string) string {
	if len(id) < 7 {
		return id
	}
	return id[:7]
}

// copyObjects carries every object of from into r, the way fetch and push
// carry the ones the other side lacks.
func (r *fakeGitRepo) copyObjects(from *fakeGitRepo) {
	for id, body := range from.state.Blobs {
		r.state.Blobs[id] = body
	}
	for id, commit := range from.state.Commits {
		r.state.Commits[id] = commit
	}
	for id, target := range from.state.Tags {
		r.state.Tags[id] = target
	}
}
