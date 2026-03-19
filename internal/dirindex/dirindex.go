// Package dirindex generates Apache-style HTML directory index pages.
// It walks an output directory tree and writes an index.html at every
// directory level, listing subdirectories and files with sizes and
// modification times.
package dirindex

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
)

const indexTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>Index of {{.Title}}</title>
<style>
body { font-family: monospace; margin: 2em; }
h1 { font-size: 1.2em; }
table { border-collapse: collapse; width: 100%; }
th, td { text-align: left; padding: 0.25em 1em 0.25em 0; white-space: nowrap; }
th { border-bottom: 1px solid #999; }
a { text-decoration: none; }
a:hover { text-decoration: underline; }
.size { text-align: right; padding-right: 2em; }
</style>
</head>
<body>
<h1>Index of {{.Title}}</h1>
<table>
<thead><tr><th>Name</th><th class="size">Size</th><th>Last Modified</th></tr></thead>
<tbody>
{{- if .Parent}}
<tr><td><a href="../">../</a></td><td class="size">-</td><td>-</td></tr>
{{- end}}
{{- range .Entries}}
<tr>
  <td><a href="{{.Href}}">{{.Name}}</a></td>
  <td class="size">{{.Size}}</td>
  <td>{{.Modified}}</td>
</tr>
{{- end}}
</tbody>
</table>
</body>
</html>
`

var tmpl = template.Must(template.New("dirindex").Parse(indexTemplate))

type entry struct {
	Name     string
	Href     string
	Size     string
	Modified string
}

type pageData struct {
	Title   string
	Parent  bool
	Entries []entry
}

// Generate writes an index.html at every directory level under outputDir.
// Existing index.html files are overwritten.
func Generate(outputDir string) error {
	absRoot, err := filepath.Abs(outputDir)
	if err != nil {
		return fmt.Errorf("dirindex: resolve output dir: %w", err)
	}

	return filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		return writeIndex(absRoot, path)
	})
}

func writeIndex(root, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("dirindex: read dir %s: %w", dir, err)
	}

	// Relative path from root for the page title
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return fmt.Errorf("dirindex: rel path: %w", err)
	}
	title := "/" + filepath.ToSlash(rel)
	if title != "/" {
		title += "/"
	}

	data := pageData{
		Title:  title,
		Parent: dir != root,
	}

	for _, e := range entries {
		name := e.Name()
		if name == "index.html" {
			continue
		}

		info, err := e.Info()
		if err != nil {
			logrus.Debugf("dirindex: stat %s: %v", filepath.Join(dir, name), err)
			continue
		}

		ent := entry{Name: name}
		if e.IsDir() {
			ent.Href = name + "/"
			ent.Name = name + "/"
			ent.Size = "-"
			ent.Modified = "-"
		} else {
			ent.Href = name
			ent.Size = humanSize(info.Size())
			ent.Modified = info.ModTime().UTC().Format(time.RFC1123)
		}
		data.Entries = append(data.Entries, ent)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("dirindex: render template for %s: %w", dir, err)
	}

	outPath := filepath.Join(dir, "index.html")
	if err := os.WriteFile(outPath, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("dirindex: write %s: %w", outPath, err)
	}

	logrus.Debugf("dirindex: wrote %s", outPath)
	return nil
}

// humanSize formats a byte count as a human-readable string.
func humanSize(n int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
		gb = 1024 * mb
	)
	switch {
	case n >= gb:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(gb))
	case n >= mb:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(mb))
	case n >= kb:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(kb))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
