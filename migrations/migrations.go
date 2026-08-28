// Package migrations embeds the SQL schema migrations. The embedded files
// are the single source of schema truth. database.Migrate applies them on
// app startup, and testdb applies them when it builds its template database.
package migrations

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
)

// FS holds the migration files. database.Migrate applies them in lexical
// filename order, so keep the numeric prefix (just makemigration adds it).
//
//go:embed *.sql
var FS embed.FS

// Hash returns a short digest of the migration files. testdb bakes it into
// the template database names, so a schema change invalidates old templates.
func Hash() string {
	h := sha256.New()
	_ = fs.WalkDir(FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := FS.ReadFile(path)
		if err != nil {
			return err
		}
		h.Write([]byte(path))
		h.Write(content)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:12]
}
