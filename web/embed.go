// Package web embarque l'UI construite (web/dist) dans le binaire Go.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist renvoie le contenu de web/dist (peut ne contenir que .gitkeep si l'UI n'est pas construite).
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
