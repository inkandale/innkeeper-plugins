// Command publish builds a release of each given plugin folder: a gzipped
// tarball per platform and an entry.toml for the published index. Run it from
// the repo root; .github/workflows/publish.yml uploads what it writes.
//
//	go run ./internal/publish -out dist -url https://github.com/<owner>/<repo>/releases/download card-codec
//
// With -index it instead updates a published index in place: its categories,
// each plugin's title and recommended mark, and each version's release notes.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/inkandale/innkeeper/protocol"
)

// now is a variable so the test can fix the date.
var now = time.Now

var platforms = []struct{ OS, Arch string }{{"linux", "amd64"}, {"linux", "arm64"}}

type artifact struct {
	OS     string `toml:"os"`
	Arch   string `toml:"arch"`
	URL    string `toml:"url"`
	SHA256 string `toml:"sha256"`
}

// entry is one plugin version in the published index, the format the core's
// plugin sources read.
type entry struct {
	Name         string     `toml:"name"`
	Title        string     `toml:"title,omitempty"`
	Path         string     `toml:"path,omitempty"`
	Category     string     `toml:"category,omitempty"`
	Recommended  bool       `toml:"recommended,omitempty"`
	Version      string     `toml:"version"`
	Released     string     `toml:"released,omitempty"`
	Notes        string     `toml:"notes,omitempty"`
	Protocol     int        `toml:"protocol"`
	Capabilities []string   `toml:"capabilities"`
	Description  string     `toml:"description"`
	Uses         []string   `toml:"uses,omitempty"`
	Artifact     []artifact `toml:"artifact,omitempty"`
}

func main() {
	out := flag.String("out", "dist", "folder to write releases into, one subfolder per tag")
	base := flag.String("url", "", "release download URL, up to /releases/download")
	published := flag.String("index", "", "published index to update with index.toml's categories and plugin details, and release notes from git")
	flag.Parse()
	if *published != "" {
		if err := finishIndex(".", *published); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *base == "" || flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: publish -out <dir> -url <release download URL> <plugin folder>...")
		os.Exit(2)
	}
	for _, folder := range flag.Args() {
		tag, err := publish(".", *out, *base, folder)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", folder, err)
			os.Exit(1)
		}
		fmt.Println(tag)
	}
}

// publish writes folder's release into out/<tag> and returns the tag.
func publish(root, out, base, folder string) (string, error) {
	var index struct {
		Plugin []entry `toml:"plugin"`
	}
	if _, err := toml.DecodeFile(filepath.Join(root, "index.toml"), &index); err != nil {
		return "", err
	}
	var e *entry
	for i := range index.Plugin {
		if index.Plugin[i].Path == folder {
			e = &index.Plugin[i]
		}
	}
	if e == nil {
		return "", fmt.Errorf("not in index.toml")
	}
	m, err := protocol.LoadManifest(filepath.Join(root, folder))
	if err != nil {
		return "", err
	}
	tag := m.Name + "-v" + m.Version
	dir := filepath.Join(out, tag)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	e.Path = ""
	// The publish date, so the setup wizard can show how new a version is.
	e.Released = now().UTC().Format(time.DateOnly)
	for _, p := range platforms {
		tgz, err := tarball(root, folder, m, p.OS, p.Arch)
		if err != nil {
			return "", err
		}
		file := fmt.Sprintf("%s-%s-%s-%s.tar.gz", m.Name, m.Version, p.OS, p.Arch)
		if err := os.WriteFile(filepath.Join(dir, file), tgz, 0o644); err != nil {
			return "", err
		}
		sum := sha256.Sum256(tgz)
		e.Artifact = append(e.Artifact, artifact{p.OS, p.Arch, base + "/" + tag + "/" + file, hex.EncodeToString(sum[:])})
	}
	f, err := os.Create(filepath.Join(dir, "entry.toml"))
	if err != nil {
		return "", err
	}
	defer f.Close()
	return tag, toml.NewEncoder(f).Encode(map[string][]entry{"plugin": {*e}})
}

// finishIndex sets the published index at path's category order, and each
// plugin's title, category, and recommended mark, from index.toml, and each
// version's notes from git. A release's entry.toml is never rebuilt, so
// these are filled in here.
func finishIndex(root, path string) error {
	var repo, published struct {
		Categories []string `toml:"categories,omitempty"`
		Plugin     []entry  `toml:"plugin"`
	}
	if _, err := toml.DecodeFile(filepath.Join(root, "index.toml"), &repo); err != nil {
		return err
	}
	if _, err := toml.DecodeFile(path, &published); err != nil {
		return err
	}
	published.Categories = repo.Categories
	byName := map[string]entry{}
	for _, e := range repo.Plugin {
		byName[e.Name] = e
	}
	for i, e := range published.Plugin {
		if r, ok := byName[e.Name]; ok {
			published.Plugin[i].Title = r.Title
			published.Plugin[i].Category = r.Category
			published.Plugin[i].Recommended = r.Recommended
			notes, err := releaseNotes(root, r.Path, e.Name+"-v"+e.Version)
			if err != nil {
				return fmt.Errorf("%s: %w", e.Name, err)
			}
			published.Plugin[i].Notes = notes
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(published)
}

// conventional splits a Conventional Commits subject into its type and text.
var conventional = regexp.MustCompile(`^(\w+)(?:\([^)]*\))?!?: (.+)$`)

// releaseNotes lists the subjects of the commits that changed folder between
// the release tagged tag and the one before it, one per line. Commits owners
// never notice, such as chores and docs, are left out. A first release has none.
func releaseNotes(root, folder, tag string) (string, error) {
	out, err := git(root, "tag", "--list", tag[:strings.LastIndex(tag, "-v")+2]+"*", "--sort=v:refname")
	if err != nil {
		return "", err
	}
	tags := strings.Fields(out)
	at := slices.Index(tags, tag)
	if at < 1 {
		return "", nil
	}
	out, err = git(root, "log", "--format=%s", tags[at-1]+".."+tag, "--", folder)
	if err != nil {
		return "", err
	}
	var lines []string
	for _, subject := range strings.Split(strings.TrimSpace(out), "\n") {
		if m := conventional.FindStringSubmatch(subject); m != nil {
			if !slices.Contains([]string{"feat", "fix", "perf"}, m[1]) {
				continue
			}
			subject = m[2]
		}
		if subject != "" {
			lines = append(lines, "- "+strings.ToUpper(subject[:1])+subject[1:])
		}
	}
	return strings.Join(lines, "\n"), nil
}

// git runs git in root and returns its output.
func git(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// tarball builds folder for one platform and packs the binary with its
// plugin.toml and icon.
func tarball(root, folder string, m *protocol.Manifest, goos, goarch string) ([]byte, error) {
	files := map[string]string{"plugin.toml": filepath.Join(root, folder, "plugin.toml")}
	if m.Icon != "" {
		files[m.Icon] = filepath.Join(root, folder, m.Icon)
	}
	if len(m.Command) > 0 {
		bin := filepath.Join(os.TempDir(), fmt.Sprintf("publish-%s-%s-%s", folder, goos, goarch))
		defer os.Remove(bin)
		build := exec.Command("go", "build", "-trimpath", "-o", bin, "./"+folder)
		build.Dir = root
		build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
		if b, err := build.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("build %s/%s: %v\n%s", goos, goarch, err, b)
		}
		files[filepath.Base(m.Command[0])] = bin
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, src := range files {
		body, err := os.ReadFile(src)
		if err != nil {
			return nil, err
		}
		mode := int64(0o644)
		if len(m.Command) > 0 && name == filepath.Base(m.Command[0]) {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(body); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
